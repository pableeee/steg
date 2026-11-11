package analysis

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chi2SurvivalEven is the closed form of P(X ≥ x) for even df = 2m:
// e^{-x/2} Σ_{i<m} (x/2)^i / i!.
func chi2SurvivalEven(x float64, df int) float64 {
	m := df / 2
	h := x / 2
	term, sum := 1.0, 1.0
	for i := 1; i < m; i++ {
		term *= h / float64(i)
		sum += term
	}
	return math.Exp(-h) * sum
}

// chi2SurvivalOdd is the closed form of P(X ≥ x) for odd df = 2m+1:
// erfc(√(x/2)) + e^{-x/2} Σ_{i=1..m} (x/2)^{i-1/2} / Γ(i+1/2).
func chi2SurvivalOdd(x float64, df int) float64 {
	h := x / 2
	sum := math.Erfc(math.Sqrt(h))
	for i := 1; i <= (df-1)/2; i++ {
		lg, _ := math.Lgamma(float64(i) + 0.5)
		sum += math.Exp(-h + (float64(i)-0.5)*math.Log(h) - lg)
	}
	return sum
}

func TestChi2PValueMatchesClosedForm(t *testing.T) {
	xs := []float64{0.01, 0.5, 1, 3.841, 9, 18.307, 30, 63, 100, 127, 160, 250, 500}
	for _, df := range []int{1, 2, 3, 4, 5, 9, 10, 29, 30, 63, 100, 127} {
		for _, x := range xs {
			want := chi2SurvivalOdd(x, df)
			if df%2 == 0 {
				want = chi2SurvivalEven(x, df)
			}
			got := chi2PValue(x, df)
			assert.InDeltaf(t, want, got, 1e-12, "df=%d x=%v", df, x)
			if want > 1e-200 {
				assert.InEpsilonf(t, want, got, 1e-9, "df=%d x=%v (relative)", df, x)
			}
		}
	}
}

// TestChi2PValueReferencePoints checks textbook critical values: each x is
// the 0.05 upper-tail point for its df.
func TestChi2PValueReferencePoints(t *testing.T) {
	cases := []struct {
		df int
		x  float64
	}{
		{1, 3.841459}, {2, 5.991465}, {9, 16.918978}, {10, 18.307038}, {30, 43.772972}, {127, 154.301516},
	}
	for _, c := range cases {
		assert.InDeltaf(t, 0.05, chi2PValue(c.x, c.df), 1e-6, "df=%d", c.df)
	}
}

func TestChi2PValueEdges(t *testing.T) {
	assert.Equal(t, 1.0, chi2PValue(0, 5))
	assert.Equal(t, 1.0, chi2PValue(-1, 5))
	p := chi2PValue(1e7, 127)
	assert.False(t, math.IsNaN(p))
	assert.GreaterOrEqual(t, p, 0.0)
	assert.Less(t, p, 1e-100)
}

// histVals builds a channel with counts[i] occurrences of values[i].
func histVals(values []uint8, counts []int) []uint8 {
	var out []uint8
	for i, v := range values {
		for j := 0; j < counts[i]; j++ {
			out = append(out, v)
		}
	}
	return out
}

// TestChannelChiSquareFewLevels uses 10 occupied pairs, each with counts
// (30, 20): χ² = 10 × 100/50 = 20. With df = 9 that is p ≈ 0.018, not
// suspicious. The old fixed df = 127 reported p ≈ 1 for the same statistic.
func TestChannelChiSquareFewLevels(t *testing.T) {
	var values []uint8
	var counts []int
	for k := 0; k < 10; k++ {
		base := uint8(20 * k) // even: pair (20k, 20k+1)
		values = append(values, base, base+1)
		counts = append(counts, 30, 20)
	}
	r := channelChiSquare("R", histVals(values, counts))
	assert.InDelta(t, 20.0, r.ChiSq, 1e-9)
	assert.Equal(t, 9, r.DF)
	assert.InDelta(t, chi2SurvivalOdd(20, 9), r.PValue, 1e-12)
	assert.InDelta(t, 0.0179, r.PValue, 0.0005)
	assert.False(t, r.Suspicious)
	assert.Greater(t, chi2PValue(20, 127), 0.9999, "sanity: the old df would have flagged this")
}

// TestChannelChiSquareFewLevelsUniform: the same 10 pairs with equal counts
// (what full LSB embedding converges to) are still flagged.
func TestChannelChiSquareFewLevelsUniform(t *testing.T) {
	var values []uint8
	var counts []int
	for k := 0; k < 10; k++ {
		values = append(values, uint8(20*k), uint8(20*k+1))
		counts = append(counts, 25, 25)
	}
	r := channelChiSquare("G", histVals(values, counts))
	assert.Equal(t, 9, r.DF)
	assert.Equal(t, 1.0, r.PValue)
	assert.True(t, r.Suspicious)
}

func TestChannelChiSquareNoUsablePairs(t *testing.T) {
	cases := map[string][]uint8{
		"empty":         nil,
		"single value":  histVals([]uint8{7}, []int{1000}),
		"one full pair": histVals([]uint8{6, 7}, []int{500, 500}),
	}
	for name, vals := range cases {
		t.Run(name, func(t *testing.T) {
			r := channelChiSquare("B", vals)
			require.Less(t, r.DF, 1)
			assert.False(t, math.IsNaN(r.PValue), fmt.Sprintf("p=%v", r.PValue))
			assert.False(t, math.IsInf(r.PValue, 0))
			assert.False(t, r.Suspicious)
		})
	}
}
