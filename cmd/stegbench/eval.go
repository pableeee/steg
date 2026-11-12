package main

import (
	"math"
	"math/rand/v2"
	"strconv"

	"github.com/pableeee/steg/steg/analysis"
)

// split is one train/test partition of image indices 0..n-1. Images, not
// planes, are split: image i's cover and every stego plane made from it go to
// the same half, so the classifier is never tested on a stego image whose
// cover it trained on (or vice versa).
type split struct {
	train, test []int
}

// makeSplit returns split number s for n images: a seeded random permutation,
// first floor(n/2) indices for training, the rest for testing. It depends only
// on n, seed and s, so every (method, rate) row is evaluated on the same splits.
func makeSplit(n int, seed int64, s int) split {
	rng := rand.New(rand.NewChaCha8(deriveSeed("split", seed, strconv.Itoa(s))))
	perm := rng.Perm(n)
	h := n / 2
	return split{train: perm[:h], test: perm[h:]}
}

// errorRates computes the detector's error on a test set from its predictions
// (true = called stego): P_FA is the fraction of covers called stego, P_MD the
// fraction of stegos called cover, P_E their mean. Empty classes give NaN.
func errorRates(coverPred, stegoPred []bool) (pfa, pmd, pe float64) {
	fa, md := 0, 0
	for _, p := range coverPred {
		if p {
			fa++
		}
	}
	for _, p := range stegoPred {
		if !p {
			md++
		}
	}
	pfa = float64(fa) / float64(len(coverPred))
	pmd = float64(md) / float64(len(stegoPred))
	if len(coverPred) == 0 {
		pfa = math.NaN()
	}
	if len(stegoPred) == 0 {
		pmd = math.NaN()
	}
	return pfa, pmd, (pfa + pmd) / 2
}

// splitResult is one (method, rate, split) evaluation.
type splitResult struct {
	split         int
	nTrain, nTest int
	pfa, pmd, pe  float64
	ensembleSeed  int64
}

// evaluate trains an ensemble on the training half of sp (cover vs stego
// features, indexed by image) and measures it on the test half.
func evaluate(cover, stego [][]float64, sp split, opts analysis.EnsembleOptions) (splitResult, error) {
	pick := func(feats [][]float64, idx []int) [][]float64 {
		out := make([][]float64, len(idx))
		for i, j := range idx {
			out[i] = feats[j]
		}
		return out
	}
	e, err := analysis.TrainEnsemble(pick(cover, sp.train), pick(stego, sp.train), opts)
	if err != nil {
		return splitResult{}, err
	}
	cp := make([]bool, len(sp.test))
	stp := make([]bool, len(sp.test))
	for i, j := range sp.test {
		cp[i] = e.Predict(cover[j])
		stp[i] = e.Predict(stego[j])
	}
	pfa, pmd, pe := errorRates(cp, stp)
	return splitResult{nTrain: len(sp.train), nTest: len(sp.test), pfa: pfa, pmd: pmd, pe: pe,
		ensembleSeed: opts.Seed}, nil
}

// meanStd returns the mean and the sample standard deviation (n − 1; 0 for a
// single value).
func meanStd(xs []float64) (mean, std float64) {
	if len(xs) == 0 {
		return math.NaN(), math.NaN()
	}
	for _, x := range xs {
		mean += x
	}
	mean /= float64(len(xs))
	if len(xs) < 2 {
		return mean, 0
	}
	var ss float64
	for _, x := range xs {
		ss += (x - mean) * (x - mean)
	}
	return mean, math.Sqrt(ss / float64(len(xs)-1))
}
