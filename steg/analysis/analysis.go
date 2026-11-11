// Package analysis implements statistical steganalysis detectors for LSB
// steganography. It provides three detectors:
//
//   - Chi-square: detects global LSB histogram uniformity caused by embedding.
//   - RS analysis: detects local pixel smoothness asymmetry caused by embedding.
//   - SPAM + FLD ensemble (spam.go, ensemble.go): a trained detector that
//     also sees LSB matching (±1), which the two tests above cannot.
//
// Analyze (used by steg detect) runs only the first two; the ensemble must be
// trained on cover and stego features first (see cmd/stegbench).
package analysis

import "image"

// ChiSquareResult holds the per-channel result of the chi-square test.
// A PValue above ChiSquareSuspiciousP means the LSB distribution is
// suspiciously uniform.
//
// DF is the number of (2k, 2k+1) value pairs present in the channel minus one.
// When DF < 1 the channel has too few distinct values to test: PValue is 0 and
// Suspicious is false.
type ChiSquareResult struct {
	Channel    string
	ChiSq      float64
	DF         int
	PValue     float64 // high = suspicious
	Suspicious bool
}

// RSResult holds the per-channel result of the RS analysis.
// Asymmetry = Rm - Rnm. It is near zero for a clean photograph and turns
// negative under LSB embedding; values below RSSuspiciousAsymmetry are flagged.
type RSResult struct {
	Channel    string
	Rm, Sm     float64 // regular/singular fractions under positive mask
	Rnm, Snm   float64 // regular/singular fractions under negative mask
	Asymmetry  float64 // Rm - Rnm; < RSSuspiciousAsymmetry = suspicious
	Suspicious bool
}

// Result is the combined detection output for an image.
type Result struct {
	ChiSquare []ChiSquareResult
	RS        []RSResult
	Verdict   string // "CLEAN", "SUSPICIOUS", or "LIKELY_STEGO"
}

// Analyze runs both the chi-square and RS analysis on img and returns a
// combined Result with an overall verdict.
func Analyze(img image.Image) Result {
	cs := ChiSquare(img)
	rs := RSAnalysis(img)
	return Result{
		ChiSquare: cs,
		RS:        rs,
		Verdict:   verdict(cs, rs),
	}
}

func verdict(cs []ChiSquareResult, rs []RSResult) string {
	hits, total := 0, len(cs)+len(rs)
	for _, r := range cs {
		if r.Suspicious {
			hits++
		}
	}
	for _, r := range rs {
		if r.Suspicious {
			hits++
		}
	}
	switch {
	case hits == 0:
		return "CLEAN"
	case hits < total:
		return "SUSPICIOUS"
	default:
		return "LIKELY_STEGO"
	}
}
