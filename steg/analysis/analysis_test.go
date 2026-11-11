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
// The detector tests do not use steg.Encode. They model LSB *replacement*
// (the classic embedding these detectors are built for) with replaceLSBs,
// which overwrites each channel LSB with a random bit with probability rate
// from a seeded RNG, so results are deterministic. steg.Encode itself uses LSB
// matching at 1 bit per channel and bounded padding, and is designed not to be
// flagged; TestEncoderOutput* check that separately.
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

// encodeWith returns a copy of src encoded by steg.Encode with a random
// payload of payloadLen bytes (payloadLen < 0 means the carrier's full
// capacity for the settings).
func encodeWith(t testing.TB, src *image.RGBA, payloadLen, bitsPerChannel, channels int) *image.RGBA {
	t.Helper()
	dst := cloneRGBA(src)
	if payloadLen < 0 {
		payloadLen = steg.CapacityBytes(dst, bitsPerChannel, channels)
	}
	payload := make([]byte, payloadLen)
	rand.New(rand.NewSource(3)).Read(payload)
	require.NoError(t, steg.Encode(dst, []byte("detectpass"), bytes.NewReader(payload), bitsPerChannel, channels))
	return dst
}

// replaceLSBs returns a copy of src in which the LSB of each of the first
// nChannels channels (R, then G, then B) is replaced by a random bit with
// probability rate (deterministic seed): LSB replacement embedding.
func replaceLSBs(src *image.RGBA, rate float64, nChannels int) *image.RGBA {
	dst := cloneRGBA(src)
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < len(dst.Pix); i += 4 {
		for c := range nChannels {
			if rng.Float64() < rate {
				dst.Pix[i+c] = dst.Pix[i+c]&^1 | uint8(rng.Intn(2))
			}
		}
	}
	return dst
}

// randomizeLSBs is replaceLSBs over all three channels.
func randomizeLSBs(src *image.RGBA, rate float64) *image.RGBA {
	return replaceLSBs(src, rate, 3)
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

// TestChiSquareFullFill uses seeded LSB randomisation rather than a real
// encode: each encode draws a fresh salt, so its LSBs (and the per-channel
// p-values, roughly uniform at full fill) change from run to run.
func TestChiSquareFullFill(t *testing.T) {
	img := randomizeLSBs(photoLikeImage(500, 500), 1.0)
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
	encoded := analysis.ChiSquare(randomizeLSBs(src, 1.0))
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

// tenLevelImage returns a w×h image whose channels each take only 10 distinct
// values, every one in a different (2k, 2k+1) pair, like a posterised or
// palette image.
func tenLevelImage(w, h int) *image.RGBA {
	rng := rand.New(rand.NewSource(3))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		for c := range 3 {
			img.Pix[i+c] = uint8(10 + 24*rng.Intn(10)) // 10, 34, ..., 226: all even
		}
		img.Pix[i+3] = 255
	}
	return img
}

// TestChiSquareFewLevels checks that the degrees of freedom follow the number
// of occupied pairs. With df fixed at 127, the stego image's χ² (around 9 for
// 10 pairs) gave p = 1.0000 regardless of the data.
func TestChiSquareFewLevels(t *testing.T) {
	src := tenLevelImage(300, 300)
	for _, r := range analysis.ChiSquare(src) {
		assert.Equal(t, 9, r.DF, "channel %s", r.Channel)
		assert.False(t, r.Suspicious, "clean channel %s (p=%.4f)", r.Channel, r.PValue)
	}
	for _, r := range analysis.ChiSquare(randomizeLSBs(src, 1)) {
		assert.Equal(t, 9, r.DF, "channel %s", r.Channel)
		assert.Less(t, r.PValue, 0.9999,
			"channel %s: χ²=%.2f should not be reported as p≈1", r.Channel, r.ChiSq)
		t.Logf("stego %s: χ²=%.2f df=%d p=%.4f", r.Channel, r.ChiSq, r.DF, r.PValue)
	}
}

// TestChiSquareSingleLevel: a flat image occupies one pair per channel, which
// leaves no degrees of freedom. It must not be flagged, and must not be NaN.
func TestChiSquareSingleLevel(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{R: 7, G: 128, B: 255, A: 255}}, image.Point{}, draw.Src)
	for _, r := range analysis.ChiSquare(img) {
		assert.Equal(t, 0, r.DF, "channel %s", r.Channel)
		assert.False(t, math.IsNaN(r.PValue), "channel %s", r.Channel)
		assert.False(t, r.Suspicious, "channel %s", r.Channel)
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
	img := randomizeLSBs(photoLikeImage(500, 500), 1.0)
	results := analysis.RSAnalysis(img)
	require.Len(t, results, 3)
	for _, r := range results {
		assert.True(t, r.Suspicious,
			"fully encoded channel %s should be suspicious (asymmetry=%+.4f)", r.Channel, r.Asymmetry)
	}
}

// TestRSUntouchedChannelsStayClean replaces R's LSBs only and checks that RS
// flags R but not the unmodified G and B channels.
func TestRSUntouchedChannelsStayClean(t *testing.T) {
	img := replaceLSBs(photoLikeImage(500, 500), 1.0, 1)
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

// TestAnalyzeFullFill: full LSB replacement on every channel trips both
// detectors everywhere.
func TestAnalyzeFullFill(t *testing.T) {
	result := analysis.Analyze(randomizeLSBs(photoLikeImage(500, 500), 1.0))
	assert.Equal(t, "LIKELY_STEGO", result.Verdict)
}

// ── What steg.Encode actually produces ───────────────────────────────────────

// TestEncoderOutputEvadesDetectors: at 1 bit per channel the encoder uses LSB
// matching, so even a payload that fills the whole carrier leaves the pair
// structure chi-square looks for and the RS asymmetry intact. A small payload
// touches only a bounded prefix of the pixel order. Both must read CLEAN.
// If this fails, check that the encode paths still enable cursors.WithLSBMatching.
func TestEncoderOutputEvadesDetectors(t *testing.T) {
	src := photoLikeImage(500, 500)
	for _, tc := range []struct {
		name       string
		payloadLen int
	}{
		{"small payload", 100},
		{"full capacity", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := analysis.Analyze(encodeWith(t, src, tc.payloadLen, 1, 3))
			assert.Equal(t, "CLEAN", result.Verdict)
			for _, r := range result.RS {
				t.Logf("RS %s asymmetry %+.4f", r.Channel, r.Asymmetry)
			}
			for _, r := range result.ChiSquare {
				t.Logf("chi-square %s p %.4f", r.Channel, r.PValue)
			}
		})
	}
}

// TestEncoderOutputReplacementAboveOneBit: with more than 1 bit per channel the
// encoder still uses replacement, so a full-capacity encode stays detectable.
// Documents the limit of LSB matching rather than a goal.
func TestEncoderOutputReplacementAboveOneBit(t *testing.T) {
	result := analysis.Analyze(encodeWith(t, photoLikeImage(500, 500), -1, 2, 3))
	assert.NotEqual(t, "CLEAN", result.Verdict)
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
