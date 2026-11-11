package analysis

import (
	"errors"
	"math"
)

// errNotPositiveDefinite is returned by choleskyFactor when a pivot is not
// safely positive, i.e. the matrix is singular, indefinite, or too badly
// conditioned to factor in float64. The solver never regularises on its own;
// callers that expect near-singular input (the FLD scatter matrix) add a ridge
// and retry.
var errNotPositiveDefinite = errors.New("analysis: matrix is not positive definite")

// choleskyFactor overwrites the lower triangle of the n×n row-major symmetric
// matrix a with its Cholesky factor L (a = L·Lᵀ). Only the lower triangle of a
// is read; the strict upper triangle is left untouched.
//
// A pivot is rejected when it is not greater than n·ε times the largest
// diagonal entry of the input, which catches exactly singular matrices
// (including the zero matrix) as well as ones whose factorisation would be
// dominated by rounding error.
func choleskyFactor(a []float64, n int) error {
	if len(a) != n*n {
		panic("analysis: choleskyFactor: size mismatch")
	}
	maxDiag := 0.0
	for i := range n {
		maxDiag = max(maxDiag, math.Abs(a[i*n+i]))
	}
	tol := float64(n) * 0x1p-52 * maxDiag
	for j := range n {
		rowJ := a[j*n : j*n+j]
		d := a[j*n+j]
		for _, v := range rowJ {
			d -= v * v
		}
		if !(d > tol) { // also rejects NaN
			return errNotPositiveDefinite
		}
		ljj := math.Sqrt(d)
		a[j*n+j] = ljj
		for i := j + 1; i < n; i++ {
			rowI := a[i*n : i*n+j]
			s := a[i*n+j]
			for k, v := range rowI {
				s -= v * rowJ[k]
			}
			a[i*n+j] = s / ljj
		}
	}
	return nil
}

// choleskySolve solves L·Lᵀ·x = b in place (b becomes x), where the lower
// triangle of l holds the factor produced by choleskyFactor.
func choleskySolve(l []float64, n int, b []float64) {
	// Forward substitution: L·y = b.
	for i := range n {
		s := b[i]
		row := l[i*n : i*n+i]
		for k, v := range row {
			s -= v * b[k]
		}
		b[i] = s / l[i*n+i]
	}
	// Back substitution: Lᵀ·x = y.
	for i := n - 1; i >= 0; i-- {
		s := b[i]
		for k := i + 1; k < n; k++ {
			s -= l[k*n+i] * b[k]
		}
		b[i] = s / l[i*n+i]
	}
}

// solveSPD solves a·x = b for a symmetric positive-definite n×n row-major
// matrix a. Neither a nor b is modified. It returns errNotPositiveDefinite
// for singular or indefinite input rather than regularising silently.
func solveSPD(a []float64, n int, b []float64) ([]float64, error) {
	if len(b) != n {
		panic("analysis: solveSPD: size mismatch")
	}
	l := make([]float64, len(a))
	copy(l, a)
	if err := choleskyFactor(l, n); err != nil {
		return nil, err
	}
	x := make([]float64, n)
	copy(x, b)
	choleskySolve(l, n, x)
	return x, nil
}
