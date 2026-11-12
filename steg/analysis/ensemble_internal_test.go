package analysis

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gaussians returns n vectors of dimension dim with independent N(mean, 1)
// entries.
func gaussians(rng *rand.Rand, n, dim int, mean float64) [][]float64 {
	out := make([][]float64, n)
	for i := range out {
		v := make([]float64, dim)
		for k := range v {
			v[k] = mean + rng.NormFloat64()
		}
		out[i] = v
	}
	return out
}

// testError is the fraction of misclassified vectors, counting both classes
// equally (the minimum-error criterion P_E of the steganalysis literature).
func testError(e *Ensemble, cover, stego [][]float64) float64 {
	fa, md := 0, 0
	for _, x := range cover {
		if e.Predict(x) {
			fa++
		}
	}
	for _, x := range stego {
		if !e.Predict(x) {
			md++
		}
	}
	return (float64(fa)/float64(len(cover)) + float64(md)/float64(len(stego))) / 2
}

// TestEnsembleSeparable: means 1 apart in each of 30 dimensions are √30 ≈ 5.5
// standard deviations apart along μ_s − μ_c, so the Bayes error is
// Φ(−2.74) ≈ 0.3%; even a 10-dimensional subspace learner sits at
// Φ(−1.58) ≈ 6%, and the majority vote of 51 of them is near zero.
func TestEnsembleSeparable(t *testing.T) {
	rng := rand.New(rand.NewPCG(10, 1))
	const dim = 30
	trC, trS := gaussians(rng, 300, dim, 0), gaussians(rng, 300, dim, 1)
	teC, teS := gaussians(rng, 1000, dim, 0), gaussians(rng, 1000, dim, 1)
	e, err := TrainEnsemble(trC, trS, EnsembleOptions{Subspace: 10, Seed: 1})
	require.NoError(t, err)
	pe := testError(e, teC, teS)
	t.Logf("separable: test error %.4f", pe)
	assert.Less(t, pe, 0.02)
}

// TestEnsembleIdentical: with no difference between the classes the ensemble
// can only fit noise, so on fresh data it must be close to guessing. With
// 1000 test vectors per class the standard error of P_E is about 0.011.
func TestEnsembleIdentical(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 1))
	const dim = 40
	trC, trS := gaussians(rng, 300, dim, 0), gaussians(rng, 300, dim, 0)
	teC, teS := gaussians(rng, 1000, dim, 0), gaussians(rng, 1000, dim, 0)
	e, err := TrainEnsemble(trC, trS, EnsembleOptions{Seed: 1})
	require.NoError(t, err)
	pe := testError(e, teC, teS)
	t.Logf("identical: test error %.4f", pe)
	assert.InDelta(t, 0.5, pe, 0.06)
}

func TestEnsembleDeterministic(t *testing.T) {
	rng := rand.New(rand.NewPCG(12, 1))
	const dim = 50
	trC, trS := gaussians(rng, 100, dim, 0), gaussians(rng, 100, dim, 0.2)
	te := gaussians(rng, 200, dim, 0.1)
	opts := EnsembleOptions{Learners: 25, Subspace: 20, Seed: 99}

	a, err := TrainEnsemble(trC, trS, opts)
	require.NoError(t, err)
	b, err := TrainEnsemble(trC, trS, opts)
	require.NoError(t, err)
	assert.Equal(t, a.learners, b.learners, "same seed must give an identical model")
	for _, x := range te {
		assert.Equal(t, a.Votes(x), b.Votes(x))
		assert.Equal(t, a.Predict(x), b.Predict(x))
	}

	opts.Seed = 100
	c, err := TrainEnsemble(trC, trS, opts)
	require.NoError(t, err)
	assert.NotEqual(t, a.learners[0].idx, c.learners[0].idx, "a different seed should draw a different subspace")
}

func TestEnsembleVotesAndPredict(t *testing.T) {
	rng := rand.New(rand.NewPCG(13, 1))
	trC, trS := gaussians(rng, 50, 8, 0), gaussians(rng, 50, 8, 0.5)
	e, err := TrainEnsemble(trC, trS, EnsembleOptions{Learners: 7, Seed: 1})
	require.NoError(t, err)
	assert.Equal(t, 7, e.Learners())
	assert.Equal(t, 8, e.Dim())
	for _, x := range gaussians(rng, 100, 8, 0.25) {
		v := e.Votes(x)
		require.True(t, v >= 0 && v <= 1)
		k := v * 7
		assert.InDelta(t, math.Round(k), k, 1e-12, "votes are multiples of 1/L")
		assert.Equal(t, v > 0.5, e.Predict(x))
	}
	assert.Panics(t, func() { e.Predict(make([]float64, 7)) })
}

func TestEnsembleDefaultsAndClamp(t *testing.T) {
	rng := rand.New(rand.NewPCG(14, 1))

	big, err := TrainEnsemble(gaussians(rng, 20, SPAMDim, 0), gaussians(rng, 20, SPAMDim, 0.1), EnsembleOptions{})
	require.NoError(t, err)
	assert.Equal(t, 51, big.Learners())
	assert.Len(t, big.learners[0].idx, 300, "default Subspace is min(300, dim)")

	// Subspace larger than dim is clamped to dim (every learner sees every
	// feature). Fewer samples than dimensions also exercises the ridge: S_W
	// has rank ≤ 8 here, yet the weights must come out finite.
	small, err := TrainEnsemble(gaussians(rng, 5, 12, 0), gaussians(rng, 5, 12, 1), EnsembleOptions{Subspace: 1000, Learners: 5})
	require.NoError(t, err)
	for _, l := range small.learners {
		assert.Equal(t, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, l.idx)
		for _, w := range l.w {
			assert.False(t, math.IsNaN(w) || math.IsInf(w, 0))
		}
	}

	// Constant features: S_W = 0, so λ falls back to 1 and w = μ_s − μ_c.
	cst, err := TrainEnsemble([][]float64{{0, 0}, {0, 0}}, [][]float64{{1, 0}, {1, 0}}, EnsembleOptions{Learners: 1})
	require.NoError(t, err)
	assert.True(t, cst.Predict([]float64{1, 0}))
	assert.False(t, cst.Predict([]float64{0, 0}))
}

func TestEnsembleValidation(t *testing.T) {
	ok := [][]float64{{1, 2}, {3, 4}}
	cases := []struct {
		name         string
		cover, stego [][]float64
		opts         EnsembleOptions
	}{
		{"empty cover", nil, ok, EnsembleOptions{}},
		{"empty stego", ok, [][]float64{}, EnsembleOptions{}},
		{"zero-length vectors", [][]float64{{}}, [][]float64{{}}, EnsembleOptions{}},
		{"ragged cover", [][]float64{{1, 2}, {3}}, ok, EnsembleOptions{}},
		{"ragged stego", ok, [][]float64{{1, 2, 3}}, EnsembleOptions{}},
		{"NaN feature", ok, [][]float64{{1, math.NaN()}}, EnsembleOptions{}},
		{"Inf feature", [][]float64{{math.Inf(1), 0}}, ok, EnsembleOptions{}},
		{"negative Learners", ok, ok, EnsembleOptions{Learners: -1}},
		{"negative Subspace", ok, ok, EnsembleOptions{Subspace: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := TrainEnsemble(tc.cover, tc.stego, tc.opts)
			assert.Error(t, err)
		})
	}
}

// TestThresholdMinimisesTrainingError checks the threshold sweep directly on
// a 1-D problem whose optimum is known: covers at 0, 1, 2, 5 and stegos at
// 3, 4, 6, 7. The best cut is between 2 and 3 (one error: the cover at 5),
// so the threshold is 2.5.
func TestThresholdMinimisesTrainingError(t *testing.T) {
	cover := [][]float64{{0}, {1}, {2}, {5}}
	stego := [][]float64{{3}, {4}, {6}, {7}}
	tr := newFLDTrainer(4, 4, 1, 1)
	for i := range tr.countC {
		tr.countC[i], tr.countS[i] = 1, 1
	}
	assert.Equal(t, 2.5, tr.threshold(cover, stego, []int{0}, []float64{1}))

	// Bootstrap multiplicities are weights. With five copies of the cover at
	// 5, three of the stego at 4 and none of the stego at 3, the cut at 2.5
	// costs 5 errors (the cover at 5) and the cut at 5.5 costs 3 (the stego
	// at 4), so the threshold moves to 5.5.
	tr.countS = []int32{0, 3, 1, 1}
	tr.countC = []int32{1, 1, 1, 5}
	assert.Equal(t, 5.5, tr.threshold(cover, stego, []int{0}, []float64{1}))
}

// BenchmarkTrainEnsemble2000 measures training at the size of a typical
// stegbench run: 2,000 SPAM-sized vectors per class, default options.
func BenchmarkTrainEnsemble2000(b *testing.B) {
	rng := rand.New(rand.NewPCG(15, 1))
	cover, stego := gaussians(rng, 2000, SPAMDim, 0), gaussians(rng, 2000, SPAMDim, 0.05)
	b.ResetTimer()
	for b.Loop() {
		if _, err := TrainEnsemble(cover, stego, EnsembleOptions{Seed: 1}); err != nil {
			b.Fatal(err)
		}
	}
}
