package analysis

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"runtime"
	"slices"
	"sync"
)

// Fisher-linear-discriminant ensemble classifier: Kodovský, Fridrich, Holub,
// "Ensemble Classifiers for Steganalysis of Digital Media", IEEE TIFS 7(2),
// 2012.
//
// Each base learner l = 0 … L−1:
//
//  1. draws, from its own PCG stream seeded with (Seed, l), a random subspace
//     of Subspace feature indices without replacement, and a bootstrap sample
//     of each class (n_c draws with replacement from the n_c cover vectors,
//     n_s from the n_s stego vectors, so class sizes are kept). Duplicates are
//     handled as integer weights rather than copies.
//  2. computes on that bootstrap sample the class means μ_c, μ_s and the
//     within-class scatter S_W = Σ_c + Σ_s (each the biased covariance of its
//     class), and solves (S_W + λI)·w = μ_s − μ_c by Cholesky factorisation.
//     λ = ensembleRidge · tr(S_W)/d_sub, i.e. a fixed fraction of the mean
//     feature variance, so it is invariant to feature scaling; if the
//     factorisation still fails λ is multiplied by 10, up to
//     ensembleRidgeRetries times. tr(S_W) = 0 uses λ = 1 (w = μ_s − μ_c).
//  3. projects the bootstrap sample onto w and sets the threshold that
//     minimises the (multiplicity-weighted) training error on it, placed at
//     the midpoint between the two adjacent projected values; a sample is
//     voted stego when its projection exceeds the threshold.
//
// The ensemble votes stego when strictly more than half of the base learners
// do. The paper's out-of-bag search for L and d_sub is not implemented: L and
// d_sub are fixed (defaults 51 and min(300, dim)). Learners are trained in
// parallel, but each owns its random stream and output slot, so the model
// depends only on the inputs and options.

const (
	defaultLearners = 51
	defaultSubspace = 300

	// ensembleRidge is λ relative to the mean diagonal of S_W. 1e-4 is large
	// enough to make S_W invertible when d_sub exceeds the bootstrap sample
	// size (it then has rank ≤ n_c + n_s − 2) and to damp directions of
	// near-zero variance (SPAM bins that almost never occur), and small
	// enough to leave well-sampled directions essentially unregularised.
	ensembleRidge        = 1e-4
	ensembleRidgeRetries = 8
)

// EnsembleOptions configures TrainEnsemble. Zero values mean defaults.
type EnsembleOptions struct {
	Learners int   // number of base learners L; default 51 (odd, so majority votes never tie)
	Subspace int   // features per base learner d_sub; default min(300, dim); larger values are clamped to dim
	Seed     int64 // seeds the subspace and bootstrap draws; same seed, same model
}

// Ensemble is a trained FLD ensemble.
type Ensemble struct {
	dim      int
	learners []fldLearner
}

// fldLearner is one base learner: x is voted stego when
// Σ_k w[k]·x[idx[k]] > thr.
type fldLearner struct {
	idx []int
	w   []float64
	thr float64
}

// TrainEnsemble trains on feature vectors of cover (label 0) and stego
// (label 1) images. All vectors must have the same length; both classes must
// be non-empty.
//
// It also rejects non-finite feature values and negative options. A Subspace
// larger than the feature dimension is clamped to it. An even Learners count
// is accepted; ties then count as cover.
func TrainEnsemble(cover, stego [][]float64, opts EnsembleOptions) (*Ensemble, error) {
	if len(cover) == 0 || len(stego) == 0 {
		return nil, errors.New("ensemble: both classes must be non-empty")
	}
	dim := len(cover[0])
	if dim == 0 {
		return nil, errors.New("ensemble: feature vectors are empty")
	}
	for ci, class := range [2][][]float64{cover, stego} {
		for i, v := range class {
			if len(v) != dim {
				return nil, fmt.Errorf("ensemble: %s vector %d has length %d, want %d",
					className(ci), i, len(v), dim)
			}
			for k, f := range v {
				if math.IsNaN(f) || math.IsInf(f, 0) {
					return nil, fmt.Errorf("ensemble: %s vector %d feature %d is %v",
						className(ci), i, k, f)
				}
			}
		}
	}
	if opts.Learners < 0 {
		return nil, fmt.Errorf("ensemble: Learners = %d is negative", opts.Learners)
	}
	if opts.Subspace < 0 {
		return nil, fmt.Errorf("ensemble: Subspace = %d is negative", opts.Subspace)
	}
	nLearners := opts.Learners
	if nLearners == 0 {
		nLearners = defaultLearners
	}
	dsub := opts.Subspace
	if dsub == 0 {
		dsub = defaultSubspace
	}
	dsub = min(dsub, dim)

	e := &Ensemble{dim: dim, learners: make([]fldLearner, nLearners)}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(runtime.GOMAXPROCS(0), nLearners) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t := newFLDTrainer(len(cover), len(stego), dim, dsub)
			for l := range jobs {
				rng := rand.New(rand.NewPCG(uint64(opts.Seed), uint64(l)))
				e.learners[l] = t.train(cover, stego, rng)
			}
		}()
	}
	for l := range nLearners {
		jobs <- l
	}
	close(jobs)
	wg.Wait()
	return e, nil
}

func className(c int) string {
	if c == 0 {
		return "cover"
	}
	return "stego"
}

// Dim returns the feature-vector length the ensemble was trained on.
func (e *Ensemble) Dim() int { return e.dim }

// Learners returns the number of base learners.
func (e *Ensemble) Learners() int { return len(e.learners) }

// Predict reports whether x is classified as stego (majority vote).
// It panics if len(x) differs from the training dimension.
func (e *Ensemble) Predict(x []float64) bool {
	return 2*e.stegoVotes(x) > len(e.learners)
}

// Votes returns the fraction of base learners voting stego, in [0, 1].
// It panics if len(x) differs from the training dimension.
func (e *Ensemble) Votes(x []float64) float64 {
	return float64(e.stegoVotes(x)) / float64(len(e.learners))
}

func (e *Ensemble) stegoVotes(x []float64) int {
	if len(x) != e.dim {
		panic(fmt.Sprintf("ensemble: vector has length %d, model expects %d", len(x), e.dim))
	}
	n := 0
	for i := range e.learners {
		l := &e.learners[i]
		s := 0.0
		for k, j := range l.idx {
			s += l.w[k] * x[j]
		}
		if s > l.thr {
			n++
		}
	}
	return n
}

// fldTrainer holds the per-worker scratch buffers for training base learners.
type fldTrainer struct {
	dim, dsub      int
	perm           []int
	countC, countS []int32
	scatter        []float64 // dsub×dsub, row-major
	work           []float64 // dsub×dsub, ridge-regularised copy for solving
	muC, muS, x    []float64
	proj           []projected
}

type projected struct {
	v              float64
	wCover, wStego int32 // bootstrap multiplicity, split by class
}

func newFLDTrainer(nc, ns, dim, dsub int) *fldTrainer {
	return &fldTrainer{
		dim: dim, dsub: dsub,
		perm:    make([]int, dim),
		countC:  make([]int32, nc),
		countS:  make([]int32, ns),
		scatter: make([]float64, dsub*dsub),
		work:    make([]float64, dsub*dsub),
		muC:     make([]float64, dsub),
		muS:     make([]float64, dsub),
		x:       make([]float64, dsub),
		proj:    make([]projected, 0, nc+ns),
	}
}

func (t *fldTrainer) train(cover, stego [][]float64, rng *rand.Rand) fldLearner {
	d := t.dsub

	// Random subspace: partial Fisher-Yates, then sorted for memory locality.
	for i := range t.perm {
		t.perm[i] = i
	}
	for i := range d {
		j := i + rng.IntN(t.dim-i)
		t.perm[i], t.perm[j] = t.perm[j], t.perm[i]
	}
	idx := slices.Clone(t.perm[:d])
	slices.Sort(idx)

	// Per-class bootstrap, kept as multiplicities.
	bootstrap(t.countC, rng)
	bootstrap(t.countS, rng)

	clear(t.scatter)
	t.addClass(cover, t.countC, idx, t.muC)
	t.addClass(stego, t.countS, idx, t.muS)
	S := t.scatter
	for a := range d { // mirror the accumulated upper triangle
		for b := a + 1; b < d; b++ {
			S[b*d+a] = S[a*d+b]
		}
	}

	delta := make([]float64, d)
	for k := range d {
		delta[k] = t.muS[k] - t.muC[k]
	}
	w := t.solveRidge(delta)

	return fldLearner{idx: idx, w: w, thr: t.threshold(cover, stego, idx, w)}
}

// bootstrap draws len(count) samples with replacement and stores how many
// times each index was drawn.
func bootstrap(count []int32, rng *rand.Rand) {
	clear(count)
	n := len(count)
	for range n {
		count[rng.IntN(n)]++
	}
}

// addClass writes the weighted mean of the class (restricted to idx) into mu
// and adds its biased covariance to the upper triangle of t.scatter.
func (t *fldTrainer) addClass(vecs [][]float64, count []int32, idx []int, mu []float64) {
	d := t.dsub
	clear(mu)
	total := 0.0
	for i, c := range count {
		if c == 0 {
			continue
		}
		cf := float64(c)
		total += cf
		v := vecs[i]
		for k, j := range idx {
			mu[k] += cf * v[j]
		}
	}
	for k := range mu {
		mu[k] /= total
	}
	inv := 1 / total
	x := t.x
	for i, c := range count {
		if c == 0 {
			continue
		}
		v := vecs[i]
		for k, j := range idx {
			x[k] = v[j] - mu[k]
		}
		cf := float64(c) * inv
		for a := range d {
			ya := cf * x[a]
			if ya == 0 {
				continue
			}
			row := t.scatter[a*d+a : a*d+d]
			xs := x[a:d]
			for b, xb := range xs {
				row[b] += ya * xb
			}
		}
	}
}

// solveRidge solves (S_W + λI)·w = delta, raising λ on failure.
func (t *fldTrainer) solveRidge(delta []float64) []float64 {
	d := t.dsub
	tr := 0.0
	for k := range d {
		tr += t.scatter[k*d+k]
	}
	lambda := ensembleRidge * tr / float64(d)
	if !(lambda > 0) {
		lambda = 1
	}
	for range ensembleRidgeRetries + 1 {
		copy(t.work, t.scatter)
		for k := range d {
			t.work[k*d+k] += lambda
		}
		if err := choleskyFactor(t.work, d); err == nil {
			w := slices.Clone(delta)
			choleskySolve(t.work, d, w)
			return w
		}
		lambda *= 10
	}
	// Unreachable for finite input (λ ends ≥ 1e4·mean variance), but fall
	// back to the nearest-mean direction rather than failing training.
	return slices.Clone(delta)
}

// threshold returns the cut on the projection onto w that minimises the
// weighted training error on the bootstrap sample.
func (t *fldTrainer) threshold(cover, stego [][]float64, idx []int, w []float64) float64 {
	t.proj = t.proj[:0]
	var nCover int32
	for ci, class := range [2][][]float64{cover, stego} {
		count := t.countC
		if ci == 1 {
			count = t.countS
		}
		for i, c := range count {
			if c == 0 {
				continue
			}
			s := 0.0
			v := class[i]
			for k, j := range idx {
				s += w[k] * v[j]
			}
			p := projected{v: s}
			if ci == 0 {
				p.wCover = c
				nCover += c
			} else {
				p.wStego = c
			}
			t.proj = append(t.proj, p)
		}
	}
	slices.SortFunc(t.proj, func(a, b projected) int {
		switch {
		case a.v < b.v:
			return -1
		case a.v > b.v:
			return 1
		}
		return 0
	})

	// thr = −∞: everything is stego, so every cover sample is an error.
	errs, best := nCover, nCover
	bestThr := math.Inf(-1)
	p := t.proj
	for i := 0; i < len(p); {
		j := i
		for j < len(p) && p[j].v == p[i].v { // group equal projections
			errs += p[j].wStego - p[j].wCover
			j++
		}
		if errs < best {
			best = errs
			if j < len(p) {
				bestThr = p[i].v + (p[j].v-p[i].v)/2
				if bestThr >= p[j].v { // adjacent floats: midpoint rounded up
					bestThr = p[i].v
				}
			} else {
				bestThr = p[i].v
			}
		}
		i = j
	}
	return bestThr
}
