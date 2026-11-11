package analysis

import (
	"image"
	"math"
)

// RSAnalysis runs the Regular-Singular (RS) steganalysis on all three channels
// of img. It applies a flipping mask to groups of 4 adjacent horizontal pixels
// and measures the asymmetry between the positive and negative mask responses.
//
// In a clean photograph Rm ≈ Rnm, so Asymmetry = Rm − Rnm sits near zero.
// LSB embedding pulls Rm down toward Sm while pushing Rnm up, so embedding
// drives Asymmetry negative (Fridrich, Goljan, Du). A channel is flagged when
// Asymmetry < RSSuspiciousAsymmetry.
func RSAnalysis(img image.Image) []RSResult {
	b := img.Bounds()
	w := b.Dx()
	names := []string{"R", "G", "B"}
	results := make([]RSResult, 3)
	for ch := 0; ch < 3; ch++ {
		results[ch] = channelRS(names[ch], extractChannel(img, ch), w)
	}
	return results
}

// flipPos applies the positive flipping function: toggles the LSB (XOR 1).
// Swaps pairs: 0↔1, 2↔3, 4↔5, ...
func flipPos(x uint8) uint8 { return x ^ 1 }

// flipNeg applies the negative flipping function: shifts pairs left by one.
// Swaps: 1↔2, 3↔4, 5↔6, ...; 0 wraps to 255.
func flipNeg(x uint8) uint8 {
	if x%2 == 0 {
		return x - 1 // even → odd (0 wraps to 255 in uint8 arithmetic)
	}
	return x + 1 // odd → even (255 wraps to 0 in uint8 arithmetic)
}

// RSSuspiciousAsymmetry is the Asymmetry (Rm − Rnm) below which a channel is
// flagged. Calibrated on a 1024×1536 photograph: clean channels measured
// −0.0017 to −0.0055, and the same photo after a default full-capacity encode
// measured −0.0398 to −0.0484. −0.02 sits between the two with a margin of
// about 0.015 on the clean side and 0.02 on the stego side.
//
// The statistic is a fraction over w×h/4 pixel groups, so its variance grows
// as the image shrinks; on small images (roughly below 512×512) clean
// channels can cross this threshold by chance.
const RSSuspiciousAsymmetry = -0.02

// roughness returns the sum of absolute differences between adjacent pixels.
func roughness(a, b, c, d uint8) float64 {
	return math.Abs(float64(a)-float64(b)) +
		math.Abs(float64(b)-float64(c)) +
		math.Abs(float64(c)-float64(d))
}

// channelRS computes RS statistics for one channel. vals is a row-major slice
// of pixel values; width is the image width in pixels.
//
// Mask M = [1,0,1,0]: the flipping function is applied to positions 0 and 2
// in each group of 4 adjacent horizontal pixels.
func channelRS(name string, vals []uint8, width int) RSResult {
	height := len(vals) / width
	var rm, sm, rnm, snm, total float64

	for y := 0; y < height; y++ {
		for x := 0; x+3 < width; x += 4 {
			i := y*width + x
			p0, p1, p2, p3 := vals[i], vals[i+1], vals[i+2], vals[i+3]

			orig := roughness(p0, p1, p2, p3)

			// Positive mask M=[1,0,1,0]: flipPos on positions 0 and 2.
			mp := roughness(flipPos(p0), p1, flipPos(p2), p3)

			// Negative mask -M=[−1,0,−1,0]: flipNeg on positions 0 and 2.
			mn := roughness(flipNeg(p0), p1, flipNeg(p2), p3)

			total++
			if mp > orig {
				rm++
			} else if mp < orig {
				sm++
			}
			if mn > orig {
				rnm++
			} else if mn < orig {
				snm++
			}
		}
	}

	if total == 0 {
		return RSResult{Channel: name}
	}

	res := RSResult{
		Channel: name,
		Rm:      rm / total,
		Sm:      sm / total,
		Rnm:     rnm / total,
		Snm:     snm / total,
	}
	res.Asymmetry = res.Rm - res.Rnm
	res.Suspicious = res.Asymmetry < RSSuspiciousAsymmetry
	return res
}
