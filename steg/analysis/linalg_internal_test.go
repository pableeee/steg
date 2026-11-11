package analysis

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// randomMatrix returns an r×c row-major matrix of standard normal entries.
func randomMatrix(rng *rand.Rand, r, c int) []float64 {
	m := make([]float64, r*c)
	for i := range m {
		m[i] = rng.NormFloat64()
	}
	return m
}

// gram returns BᵀB for an r×c matrix B (a c×c PSD matrix of rank ≤ min(r, c)).
func gram(b []float64, r, c int) []float64 {
	g := make([]float64, c*c)
	for k := range r {
		row := b[k*c : k*c+c]
		for i := range c {
			for j := range c {
				g[i*c+j] += row[i] * row[j]
			}
		}
	}
	return g
}

func matVec(a []float64, n int, x []float64) []float64 {
	y := make([]float64, n)
	for i := range n {
		for j := range n {
			y[i] += a[i*n+j] * x[j]
		}
	}
	return y
}

func TestSolveSPDRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for _, n := range []int{1, 2, 7, 50, 300} {
		// BᵀB with B (2n)×n is SPD with condition number of order 10.
		a := gram(randomMatrix(rng, 2*n, n), 2*n, n)
		want := randomMatrix(rng, n, 1)
		b := matVec(a, n, want)
		aCopy, bCopy := append([]float64(nil), a...), append([]float64(nil), b...)

		x, err := solveSPD(a, n, b)
		require.NoError(t, err, "n=%d", n)
		maxErr := 0.0
		for i := range n {
			maxErr = math.Max(maxErr, math.Abs(x[i]-want[i]))
		}
		assert.Less(t, maxErr, 1e-10, "n=%d", n)
		assert.Equal(t, aCopy, a, "solveSPD must not modify a")
		assert.Equal(t, bCopy, b, "solveSPD must not modify b")
	}
}

// TestSolveSPDRejectsSingular: the solver does not regularise; a singular or
// indefinite matrix is reported so the caller can add a ridge.
func TestSolveSPDRejectsSingular(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	cases := map[string][]float64{
		"zero":           make([]float64, 9),
		"rank deficient": gram(randomMatrix(rng, 3, 6), 3, 6), // 6×6 of rank 3
		"indefinite":     {1, 2, 2, 1},
		"negative":       {-1},
		"NaN":            {math.NaN()},
	}
	for name, a := range cases {
		t.Run(name, func(t *testing.T) {
			n := int(math.Sqrt(float64(len(a))))
			_, err := solveSPD(a, n, make([]float64, n))
			assert.ErrorIs(t, err, errNotPositiveDefinite)
		})
	}

	// Adding a ridge to the rank-deficient matrix makes it solvable, which is
	// what the FLD learner relies on.
	a := gram(randomMatrix(rng, 3, 6), 3, 6)
	for i := range 6 {
		a[i*6+i] += 1e-3
	}
	_, err := solveSPD(a, 6, make([]float64, 6))
	assert.NoError(t, err)
}
