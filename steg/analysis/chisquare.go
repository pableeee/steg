package analysis

import (
	"image"
	"math"
)

// ChiSquareSuspiciousP is the chi-square p-value above which a channel is
// flagged: a p-value this high means the (2k, 2k+1) pair frequencies are too
// evenly matched for a natural image, as LSB embedding makes them.
const ChiSquareSuspiciousP = 0.05

// ChiSquare runs the chi-square pairs-of-values test on all three channels of
// img. For each channel, pixel value pairs (2k, 2k+1) should be approximately
// equal in frequency after LSB embedding; natural images have unequal pairs.
//
// A p-value above ChiSquareSuspiciousP is suspicious — the distribution is too
// uniform.
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
	pairs := 0
	for k := 0; k < 128; k++ {
		e := (hist[2*k] + hist[2*k+1]) / 2
		if e == 0 {
			continue
		}
		pairs++
		d0 := hist[2*k] - e
		d1 := hist[2*k+1] - e
		chiSq += (d0*d0 + d1*d1) / e
	}

	// Only pairs that occur in the image contribute to the statistic, so the
	// degrees of freedom follow the number of pairs actually used (Westfeld &
	// Pfitzmann's k − 1), not the 127 of a channel that uses every level. With
	// a fixed 127, an image with few distinct levels is compared against far
	// too wide a distribution and its p-value is pushed towards 1.
	df := pairs - 1
	res := ChiSquareResult{Channel: name, ChiSq: chiSq, DF: df}
	if df < 1 {
		// Zero or one occupied pair: there is nothing to compare, so the test
		// gives no evidence either way. Report it as not suspicious.
		return res
	}
	res.PValue = chi2PValue(chiSq, df)
	res.Suspicious = res.PValue > ChiSquareSuspiciousP
	return res
}

// chi2PValue returns P(X ≥ chiSq) for a chi-squared distribution with df ≥ 1
// degrees of freedom, computed exactly as the regularised upper incomplete
// gamma function Q(df/2, chiSq/2).
func chi2PValue(chiSq float64, df int) float64 {
	if chiSq <= 0 {
		return 1
	}
	return gammaQ(float64(df)/2, chiSq/2)
}

// gammaQ returns the regularised upper incomplete gamma function
// Q(a, x) = Γ(a, x) / Γ(a) for a > 0, x ≥ 0. It uses the power series for
// P = 1 − Q when x < a + 1 and Lentz's continued fraction for Q otherwise
// (Numerical Recipes §6.2); each converges quickly in its region, so both
// stay accurate to around 1e-14.
func gammaQ(a, x float64) float64 {
	if x <= 0 {
		return 1
	}
	lg, _ := math.Lgamma(a)
	prefix := math.Exp(-x + a*math.Log(x) - lg)
	if x < a+1 {
		return 1 - prefix*gammaPSeries(a, x)
	}
	return prefix * gammaQContinuedFraction(a, x)
}

const (
	gammaEps     = 1e-15
	gammaMaxIter = 1000
	gammaTiny    = 1e-300
)

// gammaPSeries returns Σ xⁿ / (a (a+1) … (a+n)), so that
// P(a, x) = e^{-x} x^a / Γ(a) · gammaPSeries(a, x).
func gammaPSeries(a, x float64) float64 {
	ap := a
	del := 1 / a
	sum := del
	for i := 0; i < gammaMaxIter; i++ {
		ap++
		del *= x / ap
		sum += del
		if math.Abs(del) < math.Abs(sum)*gammaEps {
			break
		}
	}
	return sum
}

// gammaQContinuedFraction evaluates the continued fraction for Γ(a, x) with
// the modified Lentz method, so that
// Q(a, x) = e^{-x} x^a / Γ(a) · gammaQContinuedFraction(a, x).
func gammaQContinuedFraction(a, x float64) float64 {
	b := x + 1 - a
	c := 1 / gammaTiny
	d := 1 / b
	h := d
	for i := 1; i <= gammaMaxIter; i++ {
		an := -float64(i) * (float64(i) - a)
		b += 2
		d = an*d + b
		if math.Abs(d) < gammaTiny {
			d = gammaTiny
		}
		c = b + an/c
		if math.Abs(c) < gammaTiny {
			c = gammaTiny
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < gammaEps {
			break
		}
	}
	return h
}
