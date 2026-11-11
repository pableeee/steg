// Package analysis_test verifies the chi-square and RS steganalysis detectors.
//
// # Test image notes
//
// photoLikeImage builds a deterministic synthetic carrier that behaves like a
// photograph for both detectors: smooth low-frequency structure (gradients and
// sinusoids) plus low-amplitude Gaussian sensor-style noise from a seeded RNG.
//
//   - Chi-square: the smooth histogram leaves the (2k, 2k+1) pair counts
//     unequal, so the clean p-value is near 0.
//   - RS: the noise makes the LSB plane look natural, so Rm ≈ R-m and the clean
//     asymmetry Rm − R-m is near zero. Embedding pushes it negative.
//
// The encoder always fills the whole carrier (real payload plus encrypted
// random padding), so steg.Encode can only produce a 100% fill. Fill-rate
// sweeps therefore use randomizeLSBs, which replaces each channel LSB with a
// random bit with probability rate — the statistical effect of embedding
// encrypted data in that fraction of the carrier.
package analysis_test

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"math/rand"
	"testing"

	"github.com/pableeee/steg/steg"
	"github.com/pableeee/steg/steg/analysis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var channelNames = []string{"R", "G", "B"}

// photoLikeImage returns a deterministic w×h image with smooth per-channel
// structure plus Gaussian noise (σ = 2), clamped to [0, 255].
func photoLikeImage(w, h int) *image.RGBA {
	rng := rand.New(rand.NewSource(1))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			fx, fy := float64(x)/float64(w), float64(y)/float64(h)
			base := [3]float64{
				60 + 120*fx + 25*math.Sin(7*fy+3*fx),
				80 + 90*fy + 30*math.Cos(5*fx-2*fy),
				110 + 50*fx*fy + 35*math.Sin(6*(fx+fy)),
			}
			var c [3]uint8
			for i, v := range base {
				v += 2 * rng.NormFloat64()
				c[i] = uint8(math.Max(0, math.Min(255, math.Round(v))))
			}
			img.Set(x, y, color.RGBA{R: c[0], G: c[1], B: c[2], A: 255})
		}
	}
	return img
}

func cloneRGBA(src *image.RGBA) *image.RGBA {
	dst := image.NewRGBA(src.Bounds())
	draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Src)
	return dst
}

// encodeFull returns a copy of src encoded by steg.Encode with default
// settings (3 channels, 1 bit/channel). The payload is tiny, but the encoder
// pads it to the full carrier capacity, so every channel LSB is rewritten.
func encodeFull(t testing.TB, src *image.RGBA) *image.RGBA {
	t.Helper()
	dst := cloneRGBA(src)
	require.NoError(t, steg.Encode(dst, []byte("detectpass"), bytes.NewReader([]byte("hidden message")), 1, 3))
	return dst
}

// randomizeLSBs returns a copy of src in which each R, G and B LSB is replaced
// by a random bit with probability rate (deterministic seed).
func randomizeLSBs(src *image.RGBA, rate float64) *image.RGBA {
	dst := cloneRGBA(src)
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < len(dst.Pix); i += 4 {
		for c := range 3 {
			if rng.Float64() < rate {
				dst.Pix[i+c] = dst.Pix[i+c]&^1 | uint8(rng.Intn(2))
			}
		}
	}
	return dst
}

var fillRates = []float64{0, 0.01, 0.05, 0.10, 0.25, 0.50, 0.75, 1.00}

// ── Chi-square tests ─────────────────────────────────────────────────────────

func TestChiSquareClean(t *testing.T) {
	img := photoLikeImage(500, 500)
	results := analysis.ChiSquare(img)
	require.Len(t, results, 3)
	for _, r := range results {
		assert.False(t, r.Suspicious,
			"clean image channel %s should not be suspicious (p=%.4f)", r.Channel, r.PValue)
	}
}

func TestChiSquareFullFill(t *testing.T) {
	img := encodeFull(t, photoLikeImage(500, 500))
	results := analysis.ChiSquare(img)
	require.Len(t, results, 3)

	suspicious := 0
	for _, r := range results {
		if r.Suspicious {
			suspicious++
		}
	}
	assert.Greater(t, suspicious, 0,
		"at least one channel should be suspicious at 100%% fill")
}

// TestChiSquarePValueIncreasesWithFill verifies that the p-value rises as more
// pixels are encoded — the fundamental property of the chi-square detector.
func TestChiSquarePValueIncreasesWithFill(t *testing.T) {
	src := photoLikeImage(500, 500)
	clean := analysis.ChiSquare(src)
	encoded := analysis.ChiSquare(encodeFull(t, src))
	for i, ch := range channelNames {
		assert.Greater(t, encoded[i].PValue, clean[i].PValue,
			"chi-square p-value for channel %s should increase after 100%% fill", ch)
	}
}

// TestChiSquareDetectionLimits logs the p-value at increasing fill rates.
// Run with -v to see the table; useful for understanding detection sensitivity.
func TestChiSquareDetectionLimits(t *testing.T) {
	src := photoLikeImage(500, 500)

	t.Log("fill%  R_pvalue  G_pvalue  B_pvalue  suspicious/3")
	for _, rate := range fillRates {
		rs := analysis.ChiSquare(randomizeLSBs(src, rate))
		hits := 0
		for _, r := range rs {
			if r.Suspicious {
				hits++
			}
		}
		t.Logf("%5.0f%%  %.4f    %.4f    %.4f    %d/3",
			rate*100, rs[0].PValue, rs[1].PValue, rs[2].PValue, hits)
	}
}

// ── RS analysis tests ─────────────────────────────────────────────────────────

func TestRSClean(t *testing.T) {
	img := photoLikeImage(500, 500)
	results := analysis.RSAnalysis(img)
	require.Len(t, results, 3)
	for _, r := range results {
		assert.False(t, r.Suspicious,
			"clean image channel %s should not be suspicious (asymmetry=%+.4f)", r.Channel, r.Asymmetry)
		assert.InDelta(t, 0, r.Asymmetry, 0.01,
			"clean photo-like channel %s should have near-zero asymmetry", r.Channel)
	}
}

func TestRSFullFill(t *testing.T) {
	img := encodeFull(t, photoLikeImage(500, 500))
	results := analysis.RSAnalysis(img)
	require.Len(t, results, 3)
	for _, r := range results {
		assert.True(t, r.Suspicious,
			"fully encoded channel %s should be suspicious (asymmetry=%+.4f)", r.Channel, r.Asymmetry)
	}
}

// TestRSUntouchedChannelsStayClean encodes into R only (channels=1) and checks
// that RS flags R but not the unmodified G and B channels.
func TestRSUntouchedChannelsStayClean(t *testing.T) {
	img := cloneRGBA(photoLikeImage(500, 500))
	require.NoError(t, steg.Encode(img, []byte("detectpass"), bytes.NewReader([]byte("hidden message")), 1, 1))
	results := analysis.RSAnalysis(img)
	assert.True(t, results[0].Suspicious, "R carries the payload (asymmetry=%+.4f)", results[0].Asymmetry)
	assert.False(t, results[1].Suspicious, "G is untouched (asymmetry=%+.4f)", results[1].Asymmetry)
	assert.False(t, results[2].Suspicious, "B is untouched (asymmetry=%+.4f)", results[2].Asymmetry)
}

// TestRSAsymmetryDecreasesWithFill verifies the fundamental RS property: LSB
// embedding drives Rm − R-m negative, and further negative as more of the
// carrier is embedded.
func TestRSAsymmetryDecreasesWithFill(t *testing.T) {
	src := photoLikeImage(500, 500)
	prev := analysis.RSAnalysis(src)
	for _, rate := range []float64{0.25, 0.50, 0.75, 1.00} {
		cur := analysis.RSAnalysis(randomizeLSBs(src, rate))
		for i, ch := range channelNames {
			assert.Less(t, cur[i].Asymmetry, prev[i].Asymmetry,
				"RS asymmetry for channel %s should decrease at %.0f%% fill", ch, rate*100)
		}
		prev = cur
	}
}

// TestRSDetectionLimits logs RS asymmetry at increasing fill rates.
// Run with -v to see the table.
func TestRSDetectionLimits(t *testing.T) {
	src := photoLikeImage(500, 500)

	t.Log("fill%  R_asym    G_asym    B_asym    suspicious/3")
	for _, rate := range fillRates {
		rs := analysis.RSAnalysis(randomizeLSBs(src, rate))
		hits := 0
		for _, r := range rs {
			if r.Suspicious {
				hits++
			}
		}
		t.Logf("%5.0f%%  %+.4f    %+.4f    %+.4f    %d/3",
			rate*100, rs[0].Asymmetry, rs[1].Asymmetry, rs[2].Asymmetry, hits)
	}
}

// ── Combined Analyze tests ────────────────────────────────────────────────────

func TestAnalyzeClean(t *testing.T) {
	result := analysis.Analyze(photoLikeImage(500, 500))
	assert.Equal(t, "CLEAN", result.Verdict)
}

// TestAnalyzeFullFill: a default full-capacity encode trips both detectors on
// every channel, so all six checks fire.
func TestAnalyzeFullFill(t *testing.T) {
	result := analysis.Analyze(encodeFull(t, photoLikeImage(500, 500)))
	assert.Equal(t, "LIKELY_STEGO", result.Verdict)
}

// TestAnalyzeDetectionLimits logs the combined verdict at increasing fill rates.
func TestAnalyzeDetectionLimits(t *testing.T) {
	src := photoLikeImage(500, 500)

	t.Log("fill%  verdict")
	for _, rate := range fillRates {
		result := analysis.Analyze(randomizeLSBs(src, rate))
		t.Logf("%5.0f%%  %s", rate*100, result.Verdict)
	}
}

// ── Benchmarks ────────────────────────────────────────────────────────────────

func BenchmarkChiSquare(b *testing.B) {
	img := photoLikeImage(1000, 1000)
	b.ResetTimer()
	for b.Loop() {
		analysis.ChiSquare(img)
	}
}

func BenchmarkRS(b *testing.B) {
	img := photoLikeImage(1000, 1000)
	b.ResetTimer()
	for b.Loop() {
		analysis.RSAnalysis(img)
	}
}

func BenchmarkAnalyze(b *testing.B) {
	img := photoLikeImage(1000, 1000)
	b.ResetTimer()
	for b.Loop() {
		analysis.Analyze(img)
	}
}

var imageSizes = []struct {
	name string
	w, h int
}{
	{"100x100", 100, 100},
	{"500x500", 500, 500},
	{"1000x1000", 1000, 1000},
	{"2000x2000", 2000, 2000},
	{"3840x2160", 3840, 2160},
}

func BenchmarkAnalyzeBySize(b *testing.B) {
	for _, sz := range imageSizes {
		img := photoLikeImage(sz.w, sz.h)
		b.Run(fmt.Sprintf("%s", sz.name), func(b *testing.B) {
			for b.Loop() {
				analysis.Analyze(img)
			}
		})
	}
}
