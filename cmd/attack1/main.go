// Attack 1: Steganalysis — Chi-Square + RS Analysis
//
// Detects LSB steganography in an image without knowing the password.
// Natural images have unequal LSB pair frequencies; embedding homogenises them.
//
// Usage:
//
//	go run ./cmd/attack1 <image.png>
package main

import (
	"fmt"
	"image"
	_ "image/png"
	"math"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: attack1 <image.png>\n")
		os.Exit(1)
	}

	img, err := loadImage(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("=== Attack 1: Steganalysis ===\n\n")
	fmt.Printf("Image: %s  (%dx%d)\n\n", os.Args[1], img.Bounds().Dx(), img.Bounds().Dy())

	fmt.Println("--- Chi-Square Test (p > 0.05 is suspicious) ---")
	chResults := chiSquare(img)
	suspiciousChi := 0
	for _, r := range chResults {
		flag := ""
		if r.suspicious {
			flag = "  ← SUSPICIOUS"
			suspiciousChi++
		}
		fmt.Printf("  Channel %s: χ²=%.2f  p=%.4f%s\n", r.channel, r.chiSq, r.pValue, flag)
	}

	fmt.Println("\n--- RS Analysis (asymmetry > 0.01 is suspicious) ---")
	rsResults := rsAnalysis(img)
	suspiciousRS := 0
	for _, r := range rsResults {
		flag := ""
		if r.suspicious {
			flag = "  ← SUSPICIOUS"
			suspiciousRS++
		}
		fmt.Printf("  Channel %s: Rm=%.4f  Rnm=%.4f  asym=%.4f%s\n",
			r.channel, r.rm, r.rnm, r.asymmetry, flag)
	}

	total := suspiciousChi + suspiciousRS
	fmt.Printf("\n--- Verdict: %d/6 tests suspicious ---\n", total)
	switch {
	case total == 0:
		fmt.Println("  CLEAN — no steganography detected")
	case total < 6:
		fmt.Println("  SUSPICIOUS — steganography likely present")
	default:
		fmt.Println("  LIKELY_STEGO — strong steganography signal")
	}
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

// extractChannel returns one byte per pixel for channel ch (0=R,1=G,2=B).
// image.Color.RGBA() returns alpha-premultiplied 16-bit values; for 8-bit PNGs
// the high byte equals the low byte, so uint8(v) gives the component value.
func extractChannel(img image.Image, ch int) []uint8 {
	b := img.Bounds()
	out := make([]uint8, b.Dx()*b.Dy())
	i := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			switch ch {
			case 0:
				out[i] = uint8(r)
			case 1:
				out[i] = uint8(g)
			case 2:
				out[i] = uint8(bl)
			}
			i++
		}
	}
	return out
}

// ─── chi-square test ──────────────────────────────────────────────────────────

type chiResult struct {
	channel    string
	chiSq      float64
	pValue     float64
	suspicious bool
}

func chiSquare(img image.Image) []chiResult {
	names := []string{"R", "G", "B"}
	results := make([]chiResult, 3)
	for ch := 0; ch < 3; ch++ {
		results[ch] = channelChi(names[ch], extractChannel(img, ch))
	}
	return results
}

func channelChi(name string, vals []uint8) chiResult {
	var hist [256]float64
	for _, v := range vals {
		hist[v]++
	}
	var chiSq float64
	for k := 0; k < 128; k++ {
		expected := (hist[2*k] + hist[2*k+1]) / 2
		if expected == 0 {
			continue
		}
		d0 := hist[2*k] - expected
		d1 := hist[2*k+1] - expected
		chiSq += (d0*d0 + d1*d1) / expected
	}
	p := chi2PValue(chiSq, 127)
	return chiResult{channel: name, chiSq: chiSq, pValue: p, suspicious: p > 0.05}
}

// chi2PValue returns P(X ≥ chiSq) using the Wilson–Hilferty normal approximation.
func chi2PValue(chiSq float64, df int) float64 {
	if chiSq <= 0 {
		return 1
	}
	k := float64(df)
	h := 2.0 / (9 * k)
	z := (math.Pow(chiSq/k, 1.0/3.0) - (1 - h)) / math.Sqrt(h)
	return 0.5 * math.Erfc(z/math.Sqrt2)
}

// ─── RS analysis ──────────────────────────────────────────────────────────────

type rsResult struct {
	channel    string
	rm, rnm    float64
	asymmetry  float64
	suspicious bool
}

func rsAnalysis(img image.Image) []rsResult {
	b := img.Bounds()
	w := b.Dx()
	names := []string{"R", "G", "B"}
	results := make([]rsResult, 3)
	for ch := 0; ch < 3; ch++ {
		results[ch] = channelRS(names[ch], extractChannel(img, ch), w)
	}
	return results
}

func channelRS(name string, vals []uint8, width int) rsResult {
	height := len(vals) / width
	var rm, rnm, total float64

	for y := 0; y < height; y++ {
		for x := 0; x+3 < width; x += 4 {
			i := y*width + x
			p0, p1, p2, p3 := vals[i], vals[i+1], vals[i+2], vals[i+3]

			orig := roughness(p0, p1, p2, p3)
			pos := roughness(p0^1, p1, p2^1, p3)               // positive mask: flip LSB on pos 0,2
			neg := roughness(flipNeg(p0), p1, flipNeg(p2), p3) // negative mask

			total++
			if pos > orig {
				rm++
			}
			if neg > orig {
				rnm++
			}
		}
	}

	if total == 0 {
		return rsResult{channel: name}
	}
	rmF, rnmF := rm/total, rnm/total
	asym := rmF - rnmF
	return rsResult{channel: name, rm: rmF, rnm: rnmF, asymmetry: asym, suspicious: asym > 0.01}
}

func roughness(a, b, c, d uint8) float64 {
	return math.Abs(float64(a)-float64(b)) +
		math.Abs(float64(b)-float64(c)) +
		math.Abs(float64(c)-float64(d))
}

// flipNeg: even→(even-1), odd→(odd+1).
func flipNeg(x uint8) uint8 {
	if x%2 == 0 {
		return x - 1
	}
	return x + 1
}
