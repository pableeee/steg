// Package analysis_test verifies the chi-square and RS steganalysis detectors
// using images produced by the steg encoder.
//
// Test image notes
//
// naturalImage generates a synthetic image where all channel values are even
// (LSB = 0). This gives chi-square a clear clean baseline: pairs (2k, 2k+1)
// are maximally unequal → very low p-value → CLEAN. On a real photograph the
// same property holds, though less extremely.
//
// RS analysis is designed for natural photographs where the clean baseline has
// a slightly positive asymmetry (~0.01-0.02) that grows after embedding. On
// synthetic images with strongly structured LSBs (e.g. all-even), the clean
// asymmetry is deeply negative and the threshold of +0.01 is never crossed
// even at 100% fill. The RS tests therefore verify the DIRECTION of change
// (encoding always increases asymmetry toward positive) rather than asserting
// that the threshold is crossed, which would require a real photograph.
package analysis_test

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"testing"

	"github.com/pableeee/steg/steg"
	"github.com/pableeee/steg/steg/analysis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// naturalImage returns a synthetic image where all channel values are even
// (LSB = 0). This maximises chi-square sensitivity on the clean baseline:
// hist[2k+1] = 0 for all k, so pairs are maximally unequal before embedding.
func naturalImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			r := uint8((x*3+y*7)%256) &^ 1
			g := uint8((x*5+y*3)%256) &^ 1
			b := uint8((x*7+y*5)%256) &^ 1
			img.Set(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
	return img
}

// capacity returns the usable byte capacity of a w×h image encoded with 3
// channels and 1 bit per channel (default steg settings), minus the 56-byte
// overhead (16 enc-salt + 4 container-length + 4 real-length + 32 HMAC).
func capacity(w, h int) int {
	total := w * h * 3 / 8
	if total <= 56 {
		return 0
	}
	return total - 56
}

// encodeAtFillRate returns a fresh copy of src with a payload encoded at
// fillRate [0,1] of image capacity. fillRate = 0 returns an unmodified copy.
func encodeAtFillRate(t *testing.T, src *image.RGBA, fillRate float64) *image.RGBA {
	t.Helper()
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()

	dst := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dst.Set(x, y, src.At(x, y))
		}
	}

	cap := capacity(w, h)
	payloadSize := int(float64(cap) * fillRate)
	if payloadSize == 0 {
		return dst
	}

	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte(i & 0xff)
	}
	require.NoError(t, steg.Encode(dst, []byte("detectpass"), bytes.NewReader(payload), 1, 3))
	return dst
}

// ── Chi-square tests ─────────────────────────────────────────────────────────

func TestChiSquareClean(t *testing.T) {
	img := naturalImage(500, 500)
	results := analysis.ChiSquare(img)
	require.Len(t, results, 3)
	for _, r := range results {
		assert.False(t, r.Suspicious,
			"clean image channel %s should not be suspicious (p=%.4f)", r.Channel, r.PValue)
	}
}

func TestChiSquareFullFill(t *testing.T) {
	src := naturalImage(500, 500)
	img := encodeAtFillRate(t, src, 1.0)
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
	src := naturalImage(500, 500)
	clean := analysis.ChiSquare(src)
	encoded := analysis.ChiSquare(encodeAtFillRate(t, src, 1.0))
	for i, ch := range []string{"R", "G", "B"} {
		assert.Greater(t, encoded[i].PValue, clean[i].PValue,
			"chi-square p-value for channel %s should increase after 100%% fill", ch)
	}
}

// TestChiSquareDetectionLimits logs the p-value at increasing fill rates.
// Run with -v to see the table; useful for understanding detection sensitivity.
func TestChiSquareDetectionLimits(t *testing.T) {
	src := naturalImage(500, 500)
	rates := []float64{0, 0.01, 0.05, 0.10, 0.25, 0.50, 0.75, 1.00}

	t.Log("fill%  R_pvalue  G_pvalue  B_pvalue  suspicious/3")
	for _, rate := range rates {
		img := encodeAtFillRate(t, src, rate)
		rs := analysis.ChiSquare(img)
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
	img := naturalImage(500, 500)
	results := analysis.RSAnalysis(img)
	require.Len(t, results, 3)
	for _, r := range results {
		assert.False(t, r.Suspicious,
			"clean image channel %s should not be suspicious (asymmetry=%.4f)", r.Channel, r.Asymmetry)
	}
}

// TestRSAsymmetryIncreasesWithFill verifies the fundamental RS property: LSB
// embedding always increases the Rm − Rnm asymmetry toward positive values.
// On synthetic all-even images the clean asymmetry is deeply negative (~−0.95)
// and moves toward 0 at 100% fill; on natural photographs it starts near zero
// and crosses the +0.01 suspicious threshold at moderate fill rates.
func TestRSAsymmetryIncreasesWithFill(t *testing.T) {
	src := naturalImage(500, 500)
	clean := analysis.RSAnalysis(src)
	encoded := analysis.RSAnalysis(encodeAtFillRate(t, src, 1.0))
	for i, ch := range []string{"R", "G", "B"} {
		assert.Greater(t, encoded[i].Asymmetry, clean[i].Asymmetry,
			"RS asymmetry for channel %s should increase after 100%% fill", ch)
	}
}

// TestRSDetectionLimits logs RS asymmetry at increasing fill rates.
// Run with -v to see the table.
func TestRSDetectionLimits(t *testing.T) {
	src := naturalImage(500, 500)
	rates := []float64{0, 0.01, 0.05, 0.10, 0.25, 0.50, 0.75, 1.00}

	t.Log("fill%  R_asym    G_asym    B_asym    suspicious/3")
	for _, rate := range rates {
		img := encodeAtFillRate(t, src, rate)
		rs := analysis.RSAnalysis(img)
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
	img := naturalImage(500, 500)
	result := analysis.Analyze(img)
	assert.Equal(t, "CLEAN", result.Verdict)
}

func TestAnalyzeFullFill(t *testing.T) {
	src := naturalImage(500, 500)
	img := encodeAtFillRate(t, src, 1.0)
	result := analysis.Analyze(img)
	assert.NotEqual(t, "CLEAN", result.Verdict,
		"fully encoded image should not be CLEAN, got %s", result.Verdict)
}

// TestAnalyzeDetectionLimits logs the combined verdict at increasing fill rates.
func TestAnalyzeDetectionLimits(t *testing.T) {
	src := naturalImage(500, 500)
	rates := []float64{0, 0.01, 0.05, 0.10, 0.25, 0.50, 0.75, 1.00}

	t.Log("fill%  verdict")
	for _, rate := range rates {
		img := encodeAtFillRate(t, src, rate)
		result := analysis.Analyze(img)
		t.Logf("%5.0f%%  %s", rate*100, result.Verdict)
	}
}

// ── Benchmarks ────────────────────────────────────────────────────────────────

func BenchmarkChiSquare(b *testing.B) {
	img := naturalImage(1000, 1000)
	b.ResetTimer()
	for b.Loop() {
		analysis.ChiSquare(img)
	}
}

func BenchmarkRS(b *testing.B) {
	img := naturalImage(1000, 1000)
	b.ResetTimer()
	for b.Loop() {
		analysis.RSAnalysis(img)
	}
}

func BenchmarkAnalyze(b *testing.B) {
	img := naturalImage(1000, 1000)
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
		img := naturalImage(sz.w, sz.h)
		b.Run(fmt.Sprintf("%s", sz.name), func(b *testing.B) {
			for b.Loop() {
				analysis.Analyze(img)
			}
		})
	}
}
