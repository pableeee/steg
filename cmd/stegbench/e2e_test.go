package main

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// synthPlane returns a photo-like w × h plane: a smooth gradient with a
// low-frequency ripple, plus Gaussian noise, clamped to 0..255.
func synthPlane(w, h int, seed uint64) []uint8 {
	rng := rand.New(rand.NewPCG(seed, 99))
	gx, gy := rng.Float64()*2-1, rng.Float64()*2-1
	base := 60 + rng.Float64()*120
	fx, fy := 0.02+rng.Float64()*0.05, 0.02+rng.Float64()*0.05
	sigma := 1.5 + rng.Float64()*2
	pix := make([]uint8, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := base + gx*float64(x)*0.6 + gy*float64(y)*0.6 +
				20*math.Sin(fx*float64(x))*math.Cos(fy*float64(y)) + rng.NormFloat64()*sigma
			pix[y*w+x] = uint8(max(0, min(255, math.Round(v))))
		}
	}
	return pix
}

func writePGM(t *testing.T, path string, w, h int, pix []uint8) {
	t.Helper()
	data := append([]byte(fmt.Sprintf("P5\n# synthetic\n%d %d\n255\n", w, h)), pix...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// synthCorpus writes n synthetic covers to a new directory: PGM, grayscale
// PNG and colour PNG (the plane in R, other channels different), plus one
// corrupt PGM. It returns the directory.
func synthCorpus(t *testing.T, n, size int) string {
	dir := t.TempDir()
	for i := 0; i < n; i++ {
		pix := synthPlane(size, size, uint64(i+1))
		switch i % 3 {
		case 0:
			writePGM(t, filepath.Join(dir, fmt.Sprintf("img%03d.pgm", i)), size, size, pix)
		case 1:
			g := image.NewGray(image.Rect(0, 0, size, size))
			copy(g.Pix, pix)
			writePNG(t, filepath.Join(dir, fmt.Sprintf("img%03d.png", i)), g)
		case 2:
			c := image.NewNRGBA(image.Rect(0, 0, size, size))
			for k, v := range pix {
				c.Pix[4*k], c.Pix[4*k+1], c.Pix[4*k+2], c.Pix[4*k+3] = v, 255-v, v/2, 255
			}
			writePNG(t, filepath.Join(dir, fmt.Sprintf("img%03d.png", i)), c)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.pgm"), []byte("P5\n10 10\n255\nshort"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end benchmark run skipped in -short mode")
	}
	const n, size = 40, 96
	dir := synthCorpus(t, n, size)
	csvPath := filepath.Join(t.TempDir(), "out.csv")
	args := []string{"-dir", dir, "-rates", "0.2,1", "-splits", "2", "-seed", "3",
		"-methods", "replacement,matching,tool", "-workers", "4", "-learners", "11",
		"-csv", csvPath, "-progress=false"}
	cfg, err := parseFlags(args)
	if err != nil {
		t.Fatal(err)
	}
	var out, warn bytes.Buffer
	rep, err := run(cfg, &out, &warn)
	if err != nil {
		t.Fatalf("run: %v\nstderr:\n%s", err, warn.String())
	}
	t.Logf("\n%s", out.String())

	if rep.found != n+1 || rep.loaded != n || rep.skipped != 1 {
		t.Fatalf("found %d, loaded %d, skipped %d; want %d, %d, 1", rep.found, rep.loaded, rep.skipped, n+1, n)
	}
	if !strings.Contains(warn.String(), "broken.pgm") {
		t.Fatalf("no warning for the broken file:\n%s", warn.String())
	}
	if len(rep.rows) != 6 {
		t.Fatalf("%d rows, want 3 methods × 2 rates", len(rep.rows))
	}
	for _, r := range rep.rows {
		if len(r.splits) != 2 {
			t.Fatalf("%s @ %v: %d splits", r.method, r.rate, len(r.splits))
		}
		for _, s := range r.splits {
			if s.nTrain != n/2 || s.nTest != n-n/2 {
				t.Fatalf("%s @ %v: train %d, test %d", r.method, r.rate, s.nTrain, s.nTest)
			}
			for _, v := range []float64{s.pfa, s.pmd, s.pe} {
				if !(v >= 0 && v <= 1) {
					t.Fatalf("%s @ %v: rate %v outside [0, 1]", r.method, r.rate, v)
				}
			}
		}
		// A test set of 20 pairs: allow a detector slightly worse than chance.
		if !(r.peMean >= 0 && r.peMean <= 0.5+0.15) {
			t.Fatalf("%s @ %v: P_E %v outside [0, 0.65]", r.method, r.rate, r.peMean)
		}
		if r.method != methodTool && math.Abs(r.effRate-math.Round(r.rate*size*size)/(size*size)) > 1e-12 {
			t.Fatalf("%s @ %v: effective rate %v", r.method, r.rate, r.effRate)
		}
		if math.Abs(r.chgRate-r.effRate/2) > 0.05 {
			t.Fatalf("%s @ %v: changed %v per pixel, embedded %v", r.method, r.rate, r.chgRate, r.effRate)
		}
		if !strings.Contains(out.String(), fmt.Sprintf("%-12s %9s", r.method, rateKey(r.rate))) {
			t.Fatalf("table has no row for %s @ %v", r.method, r.rate)
		}
	}
	// Full-rate replacement is what RS was built for: the stego planes must
	// reach the detectors (20 training pairs are too few to demand much of
	// the ensemble, so its P_E is only range-checked above).
	if r := rep.rows[1]; r.method != methodReplacement || r.rate != 1 || r.rsStego < 0.9 {
		t.Fatalf("replacement @ 1 bpp: RS flagged %v of stegos, want >= 0.9", r.rsStego)
	}
	if !strings.Contains(out.String(), "NOT reproducible") {
		t.Fatal("output does not warn that the tool method is not reproducible")
	}

	f, err := os.Open(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1+6*2 {
		t.Fatalf("CSV has %d records, want header + 12", len(recs))
	}

	// Same flags, same numbers (simulated methods only).
	cfg.methods = []string{methodReplacement, methodMatching}
	cfg.csvPath = ""
	a, err := run(cfg, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := run(cfg, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range a.rows {
		ra, rb := a.rows[i], b.rows[i]
		for s := range ra.splits {
			if ra.splits[s] != rb.splits[s] {
				t.Fatalf("%s @ %v split %d: %+v vs %+v", ra.method, ra.rate, s, ra.splits[s], rb.splits[s])
			}
		}
		if ra.effRate != rb.effRate || ra.chgRate != rb.chgRate || ra.chiStego != rb.chiStego || ra.rsStego != rb.rsStego {
			t.Fatalf("%s @ %v: rows differ between identical runs", ra.method, ra.rate)
		}
	}
}

func TestListCoversDeterministic(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 30; i++ {
		writePGM(t, filepath.Join(dir, fmt.Sprintf("f%02d.pgm", i)), 4, 4, make([]uint8, 16))
	}
	os.WriteFile(filepath.Join(dir, "x.jpg"), nil, 0o644)
	os.Mkdir(filepath.Join(dir, "sub.pgm"), 0o755)
	a, found, err := listCovers(dir, 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	if found != 30 || len(a) != 10 {
		t.Fatalf("found %d, selected %d", found, len(a))
	}
	b, _, _ := listCovers(dir, 10, 5)
	c, _, _ := listCovers(dir, 10, 6)
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Fatal("same seed gave a different selection")
	}
	if fmt.Sprint(a) == fmt.Sprint(c) {
		t.Fatal("different seeds gave the same selection")
	}
	all, _, _ := listCovers(dir, 0, 5)
	if len(all) != 30 || fmt.Sprint(all[:10]) != fmt.Sprint(a) {
		t.Fatal("-n should take a prefix of the full shuffled list")
	}
}

func TestLoadPlane(t *testing.T) {
	dir := t.TempDir()
	pix := synthPlane(20, 10, 1)
	pgmPath := filepath.Join(dir, "a.pgm")
	writePGM(t, pgmPath, 20, 10, pix)

	g := image.NewGray16(image.Rect(0, 0, 20, 10))
	for i, v := range pix {
		g.SetGray16(i%20, i/20, color.Gray16{Y: uint16(v)<<8 | 0x7f})
	}
	g16Path := filepath.Join(dir, "g16.png")
	writePNG(t, g16Path, g)

	c := image.NewNRGBA(image.Rect(0, 0, 20, 10))
	for i, v := range pix {
		c.Pix[4*i], c.Pix[4*i+1], c.Pix[4*i+2], c.Pix[4*i+3] = v, v^0x55, 255-v, 255
	}
	rgbPath := filepath.Join(dir, "c.png")
	writePNG(t, rgbPath, c)

	for _, tc := range []struct {
		path    string
		channel int
		want    func(v uint8) uint8
	}{
		{pgmPath, 0, func(v uint8) uint8 { return v }},
		{g16Path, 2, func(v uint8) uint8 { return v }},
		{rgbPath, 0, func(v uint8) uint8 { return v }},
		{rgbPath, 1, func(v uint8) uint8 { return v ^ 0x55 }},
		{rgbPath, 2, func(v uint8) uint8 { return 255 - v }},
	} {
		p, err := loadPlane(tc.path, tc.channel, 0)
		if err != nil {
			t.Fatal(err)
		}
		if p.width != 20 || p.height != 10 {
			t.Fatalf("%s: %dx%d", tc.path, p.width, p.height)
		}
		for i, v := range pix {
			if p.pix[i] != tc.want(v) {
				t.Fatalf("%s channel %d: pixel %d = %d, want %d", tc.path, tc.channel, i, p.pix[i], tc.want(v))
			}
		}
	}

	p, err := loadPlane(pgmPath, 0, 6)
	if err != nil {
		t.Fatal(err)
	}
	// Central 6x6 of 20x10 starts at (7, 2).
	for y := 0; y < 6; y++ {
		for x := 0; x < 6; x++ {
			if p.pix[y*6+x] != pix[(y+2)*20+x+7] {
				t.Fatalf("crop pixel (%d,%d) wrong", x, y)
			}
		}
	}
	if _, err := loadPlane(pgmPath, 0, 11); err == nil {
		t.Fatal("crop larger than the image accepted")
	}
	bad := filepath.Join(dir, "bad.png")
	os.WriteFile(bad, []byte("\x89PNG\r\n\x1a\nnope"), 0o644)
	if _, err := loadPlane(bad, 0, 0); err == nil {
		t.Fatal("corrupt PNG accepted")
	}
}

func TestParseFlags(t *testing.T) {
	cfg, err := parseFlags([]string{"-dir", "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.methods) != 2 || len(cfg.rates) != 5 || cfg.splits != 5 || cfg.channel != 0 {
		t.Fatalf("defaults: %+v", cfg)
	}
	for _, bad := range [][]string{
		{},
		{"-dir", "x", "-methods", "lsb"},
		{"-dir", "x", "-rates", "0"},
		{"-dir", "x", "-rates", "1.5"},
		{"-dir", "x", "-rates", "0.1,0.1"},
		{"-dir", "x", "-channel", "a"},
		{"-dir", "x", "-splits", "0"},
		{"-dir", "x", "-crop", "2"},
		{"-dir", "x", "extra"},
	} {
		if _, err := parseFlags(bad); err == nil {
			t.Fatalf("parseFlags(%q) accepted", bad)
		}
	}
}
