// Package analysis implements statistical steganalysis detectors for LSB
// steganography. It provides two complementary tests:
//
//   - Chi-square: detects global LSB histogram uniformity caused by embedding.
//   - RS analysis: detects local pixel smoothness asymmetry caused by embedding.
package analysis

import "image"

// ChiSquareResult holds the per-channel result of the chi-square test.
// A high PValue (> 0.05) means the LSB distribution is suspiciously uniform.
type ChiSquareResult struct {
	Channel    string
	ChiSq      float64
	PValue     float64 // high = suspicious
	Suspicious bool
}

// RSResult holds the per-channel result of the RS analysis.
// Asymmetry = Rm - Rnm; positive values indicate embedding.
type RSResult struct {
	Channel    string
	Rm, Sm     float64 // regular/singular fractions under positive mask
	Rnm, Snm   float64 // regular/singular fractions under negative mask
	Asymmetry  float64 // Rm - Rnm; > 0 = suspicious
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
