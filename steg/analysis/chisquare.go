package analysis

import (
	"image"
	"math"
)

// ChiSquare runs the chi-square pairs-of-values test on all three channels of
// img. For each channel, pixel value pairs (2k, 2k+1) should be approximately
// equal in frequency after LSB embedding; natural images have unequal pairs.
//
// A high p-value (> 0.05) is suspicious — the distribution is too uniform.
func ChiSquare(img image.Image) []ChiSquareResult {
	names := []string{"R", "G", "B"}
	results := make([]ChiSquareResult, 3)
	for ch := 0; ch < 3; ch++ {
		results[ch] = channelChiSquare(names[ch], extractChannel(img, ch))
	}
	return results
}

func channelChiSquare(name string, vals []uint8) ChiSquareResult {
	var hist [256]float64
	for _, v := range vals {
		hist[v]++
	}

	var chiSq float64
	for k := 0; k < 128; k++ {
		e := (hist[2*k] + hist[2*k+1]) / 2
		if e == 0 {
			continue
		}
		d0 := hist[2*k] - e
		d1 := hist[2*k+1] - e
		chiSq += (d0*d0 + d1*d1) / e
	}

	p := chi2PValue(chiSq, 127)
	return ChiSquareResult{
		Channel:    name,
		ChiSq:      chiSq,
		PValue:     p,
		Suspicious: p > 0.05,
	}
}

// chi2PValue returns P(X ≥ chiSq) for a chi-squared distribution with df
// degrees of freedom. Uses the Wilson–Hilferty normal approximation, which is
// accurate to within ~0.001 for df ≥ 30.
func chi2PValue(chiSq float64, df int) float64 {
	if chiSq <= 0 {
		return 1
	}
	k := float64(df)
	h := 2.0 / (9 * k)
	z := (math.Pow(chiSq/k, 1.0/3.0) - (1 - h)) / math.Sqrt(h)
	return 1 - normalCDF(z)
}

func normalCDF(z float64) float64 {
	return 0.5 * math.Erfc(-z/math.Sqrt2)
}
