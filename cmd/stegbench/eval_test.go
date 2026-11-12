package main

import (
	"math"
	"slices"
	"testing"
)

func TestMakeSplit(t *testing.T) {
	for _, n := range []int{4, 5, 40, 101} {
		a := makeSplit(n, 7, 0)
		if len(a.train) != n/2 || len(a.test) != n-n/2 {
			t.Fatalf("n=%d: train %d, test %d", n, len(a.train), len(a.test))
		}
		// Every image is in exactly one half. A pair (cover i, stego i) is
		// addressed by the single index i, so it cannot be separated.
		all := append(slices.Clone(a.train), a.test...)
		slices.Sort(all)
		for i, v := range all {
			if v != i {
				t.Fatalf("n=%d: halves are not a partition of 0..n-1: %v", n, all)
			}
		}
		b := makeSplit(n, 7, 0)
		if !slices.Equal(a.train, b.train) || !slices.Equal(a.test, b.test) {
			t.Fatalf("n=%d: same seed and split gave different splits", n)
		}
	}
	if slices.Equal(makeSplit(100, 7, 0).train, makeSplit(100, 7, 1).train) {
		t.Fatal("splits 0 and 1 are identical")
	}
	if slices.Equal(makeSplit(100, 7, 0).train, makeSplit(100, 8, 0).train) {
		t.Fatal("seeds 7 and 8 gave identical splits")
	}
}

// Pairs stay together: a split holds image indices, and evaluate looks up
// both the cover and the stego features of image i by that one index, so
// cover i and stego i are always in the same half. Here: no index in both.
func TestSplitKeepsPairs(t *testing.T) {
	sp := makeSplit(10, 1, 0)
	inTrain := map[int]bool{}
	for _, i := range sp.train {
		inTrain[i] = true
	}
	for _, i := range sp.test {
		if inTrain[i] {
			t.Fatalf("image %d is in both halves", i)
		}
	}
}

func TestErrorRates(t *testing.T) {
	cases := []struct {
		cover, stego []bool
		pfa, pmd, pe float64
	}{
		{[]bool{false, false, false, false}, []bool{true, true, true, true}, 0, 0, 0},
		{[]bool{true, true}, []bool{false, false}, 1, 1, 1},
		{[]bool{true, false, false, false}, []bool{true, true, false, true}, 0.25, 0.25, 0.25},
		{[]bool{true, true, false, false}, []bool{true, true, true, true}, 0.5, 0, 0.25},
		{[]bool{false, false, false, false}, []bool{false, false, false, false}, 0, 1, 0.5},
		{[]bool{true, false, true, false, false}, []bool{false, true, true, false, true}, 0.4, 0.4, 0.4},
	}
	for i, c := range cases {
		pfa, pmd, pe := errorRates(c.cover, c.stego)
		if math.Abs(pfa-c.pfa) > 1e-12 || math.Abs(pmd-c.pmd) > 1e-12 || math.Abs(pe-c.pe) > 1e-12 {
			t.Fatalf("case %d: got %v %v %v, want %v %v %v", i, pfa, pmd, pe, c.pfa, c.pmd, c.pe)
		}
	}
	if _, _, pe := errorRates(nil, []bool{true}); !math.IsNaN(pe) {
		t.Fatal("empty cover set should give NaN")
	}
}

func TestMeanStd(t *testing.T) {
	m, s := meanStd([]float64{0.1, 0.2, 0.3})
	if math.Abs(m-0.2) > 1e-12 || math.Abs(s-0.1) > 1e-12 {
		t.Fatalf("got %v ± %v, want 0.2 ± 0.1", m, s)
	}
	if m, s := meanStd([]float64{0.4}); m != 0.4 || s != 0 {
		t.Fatalf("single value: %v ± %v", m, s)
	}
}
