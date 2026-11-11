// Attack 1: Steganalysis — Chi-Square + RS Analysis
//
// Detects LSB steganography in an image without knowing the password. Natural
// images have unequal LSB pair frequencies and a near-zero RS asymmetry;
// embedding random bits homogenises the pairs and drives the asymmetry
// negative.
//
// The statistics and the per-channel thresholds come from steg/analysis, the
// same implementation `steg detect` uses; this tool only formats them. Pass
// several images (for example a clean carrier and its stego versions under
// different embedding strategies) to get a comparison table at the end.
//
// Usage:
//
//	go run ./cmd/attack1 <image.png> [image.png ...]
package main

import (
	"fmt"
	"image"
	_ "image/png"
	"os"

	"github.com/pableeee/steg/steg/analysis"
)

// report is the analysis of one image.
type report struct {
	path   string
	bounds image.Rectangle
	result analysis.Result
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: attack1 <image.png> [image.png ...]\n")
		os.Exit(1)
	}

	fmt.Printf("=== Attack 1: Steganalysis ===\n")

	var reports []report
	for _, path := range os.Args[1:] {
		r, err := analyse(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			os.Exit(1)
		}
		printReport(r)
		reports = append(reports, r)
	}

	if len(reports) > 1 {
		printComparison(reports)
	}
}

func analyse(path string) (report, error) {
	img, err := loadImage(path)
	if err != nil {
		return report{}, err
	}
	return report{path: path, bounds: img.Bounds(), result: analysis.Analyze(img)}, nil
}

func printReport(r report) {
	fmt.Printf("\nImage: %s  (%dx%d)\n", r.path, r.bounds.Dx(), r.bounds.Dy())

	fmt.Println("\n--- Chi-Square Test (high p = LSB pairs too uniform) ---")
	for _, c := range r.result.ChiSquare {
		fmt.Printf("  Channel %s: χ²=%10.2f  p=%.4f%s\n", c.Channel, c.ChiSq, c.PValue, flag(c.Suspicious))
	}

	fmt.Println("\n--- RS Analysis (negative asym = embedding signature) ---")
	for _, s := range r.result.RS {
		fmt.Printf("  Channel %s: Rm=%.4f  Rnm=%.4f  Sm=%.4f  Snm=%.4f  asym=%+.4f%s\n",
			s.Channel, s.Rm, s.Rnm, s.Sm, s.Snm, s.Asymmetry, flag(s.Suspicious))
	}

	hits, total := suspiciousCount(r.result)
	fmt.Printf("\n--- Verdict: %s (%d/%d tests suspicious) ---\n", r.result.Verdict, hits, total)
}

// printComparison prints one row per image: chi-square p-values and RS
// asymmetries per channel, and the verdict.
func printComparison(reports []report) {
	fmt.Printf("\n=== Comparison ===\n\n")
	const row = "%-32s  %-24s  %-25s  %s\n"
	fmt.Printf(row, "image", "chi-square p (R/G/B)", "RS asym (R/G/B)", "verdict")
	for _, r := range reports {
		var p, a [3]float64
		for i, c := range r.result.ChiSquare {
			p[i] = c.PValue
		}
		for i, s := range r.result.RS {
			a[i] = s.Asymmetry
		}
		hits, total := suspiciousCount(r.result)
		fmt.Printf(row, r.path,
			fmt.Sprintf("%.4f / %.4f / %.4f", p[0], p[1], p[2]),
			fmt.Sprintf("%+.4f / %+.4f / %+.4f", a[0], a[1], a[2]),
			fmt.Sprintf("%s (%d/%d)", r.result.Verdict, hits, total))
	}
}

func suspiciousCount(res analysis.Result) (hits, total int) {
	for _, c := range res.ChiSquare {
		if c.Suspicious {
			hits++
		}
	}
	for _, s := range res.RS {
		if s.Suspicious {
			hits++
		}
	}
	return hits, len(res.ChiSquare) + len(res.RS)
}

func flag(suspicious bool) string {
	if suspicious {
		return "  <- SUSPICIOUS"
	}
	return ""
}

func loadImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}
