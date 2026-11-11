// stegbench: steganalysis benchmark for LSB embedding.
//
// For a corpus of cover images it measures how detectable each embedding
// method is at each embedding rate (bits per pixel), with
//
//   - a trained detector: second-order SPAM features (686-D) and a Fisher
//     linear discriminant ensemble (steg/analysis), evaluated over random
//     train/test splits by image; reported as P_E = (P_FA + P_MD) / 2 on the
//     test half, mean ± standard deviation over the splits (0.5 = guessing);
//   - the untrained detectors of `steg detect` (chi-square and RS from
//     steg/analysis): the fraction of covers and of stegos each flags.
//
// Methods:
//
//	replacement  simulated LSB replacement: bit 0 := message bit
//	matching     simulated LSB matching: ±1 at random when bit 0 differs
//	             (only up at 0, only down at 255); what steg encode does
//	tool         the real steg.Encode with 1 channel × 1 bit, payload sized so
//	             the padded block gives the rate closest to the requested one
//
// The simulated methods choose round(rate × N) distinct pixels uniformly at
// random and embed a uniform random bit in each, which is statistically what
// the encoder does (its keyed order picks a pseudorandom subset and its bits
// are ciphertext). Every random stream is derived from -seed, the image file
// name, the method and the rate, so the same flags give the same numbers;
// except for the tool method, whose salt and ±1 directions come from
// crypto/rand inside the encoder.
//
// Covers are .pgm (binary P5, maxval 255) and .png files directly in -dir. A
// grayscale PNG contributes its gray plane; a colour PNG the -channel plane.
//
// Usage:
//
//	go run ./cmd/stegbench -dir ~/datasets/bossbase/extracted/BOSSbase_1.01 -n 2000
package main

import (
	"encoding/binary"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"image"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pableeee/steg/steg/analysis"
)

type config struct {
	dir       string
	n         int
	seed      int64
	crop      int
	channel   int // 0 = R, 1 = G, 2 = B
	methods   []string
	rates     []float64
	splits    int
	workers   int
	trainJobs int
	learners  int
	subspace  int
	csvPath   string
	progress  bool
}

func main() {
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "stegbench: %v\n", err)
		os.Exit(2)
	}
	if _, err := run(cfg, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "stegbench: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags(args []string) (config, error) {
	fs := flag.NewFlagSet("stegbench", flag.ContinueOnError)
	var cfg config
	var methods, rates, channel string
	fs.StringVar(&cfg.dir, "dir", "", "directory of cover images (.pgm P5 8-bit, .png); not searched recursively")
	fs.IntVar(&cfg.n, "n", 0, "use at most this many images (0 = all), chosen by a seeded shuffle of the sorted names")
	fs.Int64Var(&cfg.seed, "seed", 1, "seed for image selection, embedding, splits and the ensemble")
	fs.IntVar(&cfg.crop, "crop", 0, "use the central WxW region of each image (0 = whole image)")
	fs.StringVar(&channel, "channel", "r", "channel of colour PNGs to use: r, g or b (grayscale images use their gray plane)")
	fs.StringVar(&methods, "methods", "replacement,matching", "comma-separated methods: replacement, matching, tool")
	fs.StringVar(&rates, "rates", "0.05,0.1,0.2,0.4,1.0", "comma-separated embedding rates in bits per pixel, each in (0, 1]")
	fs.IntVar(&cfg.splits, "splits", 5, "number of random train/test splits")
	fs.IntVar(&cfg.workers, "workers", runtime.GOMAXPROCS(0), "parallel workers for feature extraction")
	fs.IntVar(&cfg.trainJobs, "train-jobs", 4, "ensembles trained concurrently (each is also parallel internally)")
	fs.IntVar(&cfg.learners, "learners", 0, "ensemble base learners (0 = library default)")
	fs.IntVar(&cfg.subspace, "subspace", 0, "ensemble subspace dimension (0 = library default)")
	fs.StringVar(&cfg.csvPath, "csv", "", "write per-split results to this CSV file")
	fs.BoolVar(&cfg.progress, "progress", true, "report extraction progress on stderr")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if fs.NArg() > 0 {
		return cfg, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if cfg.dir == "" {
		return cfg, errors.New("-dir is required")
	}
	switch strings.ToLower(channel) {
	case "r":
		cfg.channel = 0
	case "g":
		cfg.channel = 1
	case "b":
		cfg.channel = 2
	default:
		return cfg, fmt.Errorf("-channel must be r, g or b, got %q", channel)
	}
	for _, m := range strings.Split(methods, ",") {
		m = strings.TrimSpace(m)
		if !slices.Contains(knownMethods, m) {
			return cfg, fmt.Errorf("unknown method %q (want %s)", m, strings.Join(knownMethods, ", "))
		}
		if slices.Contains(cfg.methods, m) {
			return cfg, fmt.Errorf("method %q listed twice", m)
		}
		cfg.methods = append(cfg.methods, m)
	}
	for _, s := range strings.Split(rates, ",") {
		r, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil || !(r > 0 && r <= 1) {
			return cfg, fmt.Errorf("rate %q must be a number in (0, 1]", s)
		}
		if slices.Contains(cfg.rates, r) {
			return cfg, fmt.Errorf("rate %v listed twice", r)
		}
		cfg.rates = append(cfg.rates, r)
	}
	switch {
	case cfg.n < 0:
		return cfg, errors.New("-n must be >= 0")
	case cfg.crop != 0 && cfg.crop < 4:
		return cfg, errors.New("-crop must be 0 or at least 4")
	case cfg.splits < 1:
		return cfg, errors.New("-splits must be >= 1")
	case cfg.workers < 1 || cfg.trainJobs < 1:
		return cfg, errors.New("-workers and -train-jobs must be >= 1")
	case cfg.learners < 0 || cfg.subspace < 0:
		return cfg, errors.New("-learners and -subspace must be >= 0")
	}
	return cfg, nil
}

// cell identifies one (method, rate) pair; cells are numbered method-major.
type cell struct {
	method string
	rate   float64
}

func (c config) cells() []cell {
	var out []cell
	for _, m := range c.methods {
		for _, r := range c.rates {
			out = append(out, cell{m, r})
		}
	}
	return out
}

// stegoOut is what is kept of one stego plane: its features and statistics.
type stegoOut struct {
	feat    []float64
	chi, rs bool
	effRate float64 // bits embedded per pixel
	chgRate float64 // pixels changed per pixel
}

// imageOut is what is kept of one cover image.
type imageOut struct {
	path    string
	w, h    int
	err     error
	cover   []float64
	chi, rs bool
	stego   []stegoOut // indexed like config.cells()
}

// row is one line of the result table.
type row struct {
	cell
	effRate, chgRate   float64
	peMean, peStd      float64
	pfaMean, pmdMean   float64
	chiCover, chiStego float64
	rsCover, rsStego   float64
	splits             []splitResult
}

// report is the outcome of a run.
type report struct {
	found, selected, loaded, skipped int
	rows                             []row
}

// phaseTimes accumulates worker time spent in each extraction step.
type phaseTimes struct{ load, embed, spam, detect atomic.Int64 }

func timed(acc *atomic.Int64, f func()) {
	t := time.Now()
	f()
	acc.Add(int64(time.Since(t)))
}

func run(cfg config, out, warn io.Writer) (*report, error) {
	start := time.Now()
	paths, found, err := listCovers(cfg.dir, cfg.n, cfg.seed)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no .pgm or .png files in %s", cfg.dir)
	}
	tList := time.Since(start)

	// Phase 1: load, embed, extract features and detector flags.
	cells := cfg.cells()
	t1 := time.Now()
	var pt phaseTimes
	results := make([]imageOut, len(paths))
	jobs := make(chan int)
	var done atomic.Int64
	var wg sync.WaitGroup
	for range min(cfg.workers, len(paths)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var buf []uint8
			var perm []int32
			for i := range jobs {
				results[i] = processImage(paths[i], cfg, cells, &pt, &buf, &perm)
				d := done.Add(1)
				if cfg.progress && (d%100 == 0 || int(d) == len(paths)) {
					fmt.Fprintf(warn, "extract: %d/%d images (%s)\n", d, len(paths), time.Since(t1).Round(time.Second))
				}
			}
		}()
	}
	for i := range paths {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	tExtract := time.Since(t1)

	var ok []*imageOut
	rep := &report{found: found, selected: len(paths)}
	for i := range results {
		if results[i].err != nil {
			fmt.Fprintf(warn, "warning: skipping %s: %v\n", results[i].path, results[i].err)
			rep.skipped++
			continue
		}
		ok = append(ok, &results[i])
	}
	rep.loaded = len(ok)
	if len(ok) < 4 {
		return nil, fmt.Errorf("only %d usable images (%d skipped); need at least 4", len(ok), rep.skipped)
	}

	// Phase 2: train and evaluate the ensemble per (cell, split).
	t2 := time.Now()
	coverFeats := make([][]float64, len(ok))
	for i, im := range ok {
		coverFeats[i] = im.cover
	}
	splits := make([]split, cfg.splits)
	for s := range splits {
		splits[s] = makeSplit(len(ok), cfg.seed, s)
	}
	rep.rows = make([]row, len(cells))
	type trainJob struct{ c, s int }
	tjobs := make(chan trainJob)
	var mu sync.Mutex
	var firstErr error
	for c := range cells {
		rep.rows[c].cell = cells[c]
		rep.rows[c].splits = make([]splitResult, cfg.splits)
	}
	stegoFeats := make([][][]float64, len(cells))
	for c := range cells {
		stegoFeats[c] = make([][]float64, len(ok))
		for i, im := range ok {
			stegoFeats[c][i] = im.stego[c].feat
		}
	}
	var twg sync.WaitGroup
	for range min(cfg.trainJobs, len(cells)*cfg.splits) {
		twg.Add(1)
		go func() {
			defer twg.Done()
			for j := range tjobs {
				cl := cells[j.c]
				seedBytes := deriveSeed("ensemble", cfg.seed, cl.method, rateKey(cl.rate), strconv.Itoa(j.s))
				opts := analysis.EnsembleOptions{
					Learners: cfg.learners,
					Subspace: cfg.subspace,
					Seed:     int64(binary.LittleEndian.Uint64(seedBytes[:8])),
				}
				r, err := evaluate(coverFeats, stegoFeats[j.c], splits[j.s], opts)
				r.split = j.s
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = fmt.Errorf("training %s @ %v, split %d: %w", cl.method, cl.rate, j.s, err)
				}
				rep.rows[j.c].splits[j.s] = r
				mu.Unlock()
			}
		}()
	}
	for c := range cells {
		for s := 0; s < cfg.splits; s++ {
			tjobs <- trainJob{c, s}
		}
	}
	close(tjobs)
	twg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	tTrain := time.Since(t2)

	// Aggregate.
	n := float64(len(ok))
	var chiCover, rsCover float64
	for _, im := range ok {
		chiCover += b2f(im.chi)
		rsCover += b2f(im.rs)
	}
	for c := range cells {
		r := &rep.rows[c]
		r.chiCover, r.rsCover = chiCover/n, rsCover/n
		for _, im := range ok {
			st := im.stego[c]
			r.effRate += st.effRate / n
			r.chgRate += st.chgRate / n
			r.chiStego += b2f(st.chi) / n
			r.rsStego += b2f(st.rs) / n
		}
		var pes, pfas, pmds []float64
		for _, s := range r.splits {
			pes = append(pes, s.pe)
			pfas = append(pfas, s.pfa)
			pmds = append(pmds, s.pmd)
		}
		r.peMean, r.peStd = meanStd(pes)
		r.pfaMean, _ = meanStd(pfas)
		r.pmdMean, _ = meanStd(pmds)
	}

	printReport(out, cfg, rep, ok)
	if cfg.csvPath != "" {
		if err := writeCSV(cfg.csvPath, rep); err != nil {
			return nil, err
		}
		fmt.Fprintf(out, "\nper-split results written to %s\n", cfg.csvPath)
	}
	w := cfg.workers
	fmt.Fprintf(out, "\ntiming: list %s, extract %s (worker time: load %s, embed %s, SPAM %s, chi-square+RS %s; %d workers), train+test %s, total %s\n",
		tList.Round(time.Millisecond), tExtract.Round(time.Millisecond),
		dur(&pt.load), dur(&pt.embed), dur(&pt.spam), dur(&pt.detect), w,
		tTrain.Round(time.Millisecond), time.Since(start).Round(time.Millisecond))
	return rep, nil
}

func dur(a *atomic.Int64) time.Duration {
	return time.Duration(a.Load()).Round(time.Millisecond)
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// processImage loads one cover, extracts its features and flags, and for
// every cell embeds into a scratch plane and extracts the stego features and
// flags. Stego planes are not kept. buf and perm are the worker's scratch.
func processImage(path string, cfg config, cells []cell, pt *phaseTimes, buf *[]uint8, perm *[]int32) (res imageOut) {
	res.path = path
	var p *plane
	var err error
	timed(&pt.load, func() { p, err = loadPlane(path, cfg.channel, cfg.crop) })
	if err != nil {
		res.err = err
		return res
	}
	res.w, res.h = p.width, p.height
	n := len(p.pix)
	if cap(*buf) < n {
		*buf = make([]uint8, n)
		*perm = make([]int32, n)
	}
	stego := (*buf)[:n]

	timed(&pt.spam, func() { res.cover, err = analysis.SPAM(p.pix, p.width, p.height) })
	if err != nil {
		res.err = err
		return res
	}
	timed(&pt.detect, func() { res.chi, res.rs = detectFlags(p.pix, p.width, p.height) })

	name := filepath.Base(path)
	res.stego = make([]stegoOut, len(cells))
	for c, cl := range cells {
		rng := rand.New(rand.NewChaCha8(deriveSeed("embed", cfg.seed, name, cl.method, rateKey(cl.rate))))
		so := &res.stego[c]
		timed(&pt.embed, func() {
			if cl.method == methodTool {
				var plan toolPlan
				plan, err = planTool(p.width, p.height, cl.rate)
				if err != nil {
					return
				}
				var changed int
				changed, err = embedTool(stego, p.pix, p.width, p.height, plan, rng)
				so.effRate = plan.effRate
				so.chgRate = float64(changed) / float64(n)
				return
			}
			written, changed := embedSim(stego, p.pix, cl.rate, ruleFor(cl.method), rng, *perm)
			so.effRate = float64(written) / float64(n)
			so.chgRate = float64(changed) / float64(n)
		})
		if err != nil {
			res.err = fmt.Errorf("%s @ %v: %w", cl.method, cl.rate, err)
			return res
		}
		timed(&pt.spam, func() { so.feat, err = analysis.SPAM(stego, p.width, p.height) })
		if err != nil {
			res.err = err
			return res
		}
		timed(&pt.detect, func() { so.chi, so.rs = detectFlags(stego, p.width, p.height) })
	}
	return res
}

// detectFlags runs the steg/analysis chi-square and RS tests on the plane,
// wrapped (not copied) as an *image.Gray, and returns their verdicts for the
// R channel. A Gray image reports its level as R = G = B, so the R result is
// the plane's; G and B are identical and ignored.
func detectFlags(pix []uint8, w, h int) (chi, rs bool) {
	g := &image.Gray{Pix: pix, Stride: w, Rect: image.Rect(0, 0, w, h)}
	return analysis.ChiSquare(g)[0].Suspicious, analysis.RSAnalysis(g)[0].Suspicious
}

func printReport(out io.Writer, cfg config, rep *report, ok []*imageOut) {
	fmt.Fprintf(out, "=== stegbench: steganalysis benchmark ===\n\n")
	fmt.Fprintf(out, "corpus:    %s\n", cfg.dir)
	fmt.Fprintf(out, "images:    %d found, %d selected (-n %d, seed %d), %d used, %d skipped\n",
		rep.found, rep.selected, cfg.n, cfg.seed, rep.loaded, rep.skipped)
	fmt.Fprintf(out, "size:      %s", sizeSummary(ok))
	if cfg.crop > 0 {
		fmt.Fprintf(out, " (central crop -crop %d)", cfg.crop)
	}
	fmt.Fprintf(out, "\nplane:     gray level (PGM, grayscale PNG); colour PNG channel %c\n", "RGB"[cfg.channel])
	fmt.Fprintf(out, "methods:   %s\n", strings.Join(cfg.methods, ", "))
	fmt.Fprintf(out, "rates:     %s bits per pixel\n", joinFloats(cfg.rates))
	fmt.Fprintf(out, "detector:  SPAM (%d-D, T=3) + FLD ensemble, learners %s, subspace %s; %d split(s), half train / half test by image\n",
		analysis.SPAMDim, orDefault(cfg.learners), orDefault(cfg.subspace), cfg.splits)
	fmt.Fprintf(out, "also:      steg/analysis chi-square (p > %v) and RS (asym < %v) on the plane as *image.Gray, R-channel verdict\n",
		analysis.ChiSquareSuspiciousP, analysis.RSSuspiciousAsymmetry)
	fmt.Fprintf(out, "seeds:     all derived from -seed %d (selection, embedding per image/method/rate, splits, ensembles)\n", cfg.seed)
	if slices.Contains(cfg.methods, methodTool) {
		fmt.Fprintf(out, "note:      the tool method runs steg.Encode, whose salt and ±1 directions come from crypto/rand: its rows are NOT reproducible\n")
	}

	fmt.Fprintf(out, "\nP_E = (P_FA + P_MD)/2 on the test half, mean ± std over splits; 0.5 = detector is guessing.\n")
	fmt.Fprintf(out, "eff = bits embedded per pixel (mean over images); chg = pixels changed per pixel.\n")
	fmt.Fprintf(out, "chi / RS = fraction of covers / stegos flagged.\n\n")
	const hdr = "%-12s %9s %7s %7s  %-15s %6s %6s  %-13s %-13s\n"
	fmt.Fprintf(out, hdr, "method", "rate", "eff", "chg", "P_E", "P_FA", "P_MD", "chi cov/stg", "RS cov/stg")
	for _, r := range rep.rows {
		fmt.Fprintf(out, hdr, r.method, rateKey(r.rate),
			fmt.Sprintf("%.4f", r.effRate), fmt.Sprintf("%.4f", r.chgRate),
			fmt.Sprintf("%.4f ± %.4f", r.peMean, r.peStd),
			fmt.Sprintf("%.4f", r.pfaMean), fmt.Sprintf("%.4f", r.pmdMean),
			fmt.Sprintf("%.3f/%.3f", r.chiCover, r.chiStego),
			fmt.Sprintf("%.3f/%.3f", r.rsCover, r.rsStego))
	}
}

func orDefault(v int) string {
	if v == 0 {
		return "default"
	}
	return strconv.Itoa(v)
}

func joinFloats(fs []float64) string {
	s := make([]string, len(fs))
	for i, f := range fs {
		s[i] = rateKey(f)
	}
	return strings.Join(s, ", ")
}

// sizeSummary describes the image sizes: "512x512" when uniform, otherwise
// the distinct sizes with their counts.
func sizeSummary(ok []*imageOut) string {
	counts := map[string]int{}
	for _, im := range ok {
		counts[fmt.Sprintf("%dx%d", im.w, im.h)]++
	}
	if len(counts) == 1 {
		for k := range counts {
			return k
		}
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for i, k := range keys {
		if i == 8 {
			parts = append(parts, fmt.Sprintf("... (%d sizes)", len(keys)))
			break
		}
		parts = append(parts, fmt.Sprintf("%s ×%d", k, counts[k]))
	}
	return "mixed: " + strings.Join(parts, ", ")
}

func writeCSV(path string, rep *report) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	_ = w.Write([]string{"method", "rate", "eff_rate", "split", "n_train", "n_test", "ensemble_seed", "p_fa", "p_md", "p_e"})
	ff := func(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
	for _, r := range rep.rows {
		for _, s := range r.splits {
			_ = w.Write([]string{r.method, rateKey(r.rate), ff(r.effRate), strconv.Itoa(s.split),
				strconv.Itoa(s.nTrain), strconv.Itoa(s.nTest), strconv.FormatInt(s.ensembleSeed, 10),
				ff(s.pfa), ff(s.pmd), ff(s.pe)})
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
