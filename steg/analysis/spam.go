package analysis

import "fmt"

// SPAM (Subtractive Pixel Adjacency Matrix) features, second order, T = 3:
// Pevný, Bas, Fridrich, "Steganalysis by Subtractive Pixel Adjacency Matrix",
// IEEE TIFS 5(2), 2010.
//
// For a direction d (one of →, ←, ↓, ↑, ↘, ↖, ↙, ↗) and a pixel p, the
// difference is D(p) = X(p) − X(p+d), truncated to [−T, T]. Walking along d,
// three consecutive differences w = D(p), v = D(p+d), u = D(p+2d) (which need
// the four pixels p … p+3d to lie inside the image) form one sample of a
// second-order Markov chain. The transition matrix
//
//	M_d(u, v, w) = Pr(u | v, w) = count(w, v, u) / count(w, v)
//
// is estimated over all valid p, with 0/0 defined as 0. For ← and ↑ this is
// the → and ↓ chain of the image rotated by 180°: the difference is always
// "current pixel minus next pixel in the walking direction".
//
// The feature vector is F1 followed by F2, where F1 is the mean of M→, M←,
// M↓, M↑ and F2 is the mean of M↘, M↖, M↙, M↗.
//
// Index order inside each 343-value half: the conditioning pair (w, v) is the
// block and u varies fastest:
//
//	index(u, v, w) = ((w+T)·7 + (v+T))·7 + (u+T)
//
// so feature[base + 7·((w+T)·7+(v+T)) : … + 7] is the conditional distribution
// of u given (w, v); base is 0 for F1 and 343 for F2.

const (
	spamT     = 3
	spamBins  = 2*spamT + 1                    // 7 truncated difference values
	spamBlock = spamBins * spamBins * spamBins // 343 entries per matrix
)

// SPAMDim is the length of the vector SPAM returns (2 × (2T+1)^3 with T = 3).
const SPAMDim = 2 * spamBlock

// spamDirs lists the 8 walking directions as (dx, dy) steps, in the order
// →, ←, ↓, ↑ (averaged into F1) then ↘, ↖, ↙, ↗ (averaged into F2).
var spamDirs = [8][2]int{
	{1, 0}, {-1, 0}, {0, 1}, {0, -1},
	{1, 1}, {-1, -1}, {-1, 1}, {1, -1},
}

// spamClip maps a raw difference a−b (offset by +255) to its truncated bin
// index clamp(a−b, −T, T)+T.
var spamClip = func() (t [511]uint8) {
	for i := range t {
		d := min(max(i-255, -spamT), spamT)
		t[i] = uint8(d + spamT)
	}
	return t
}()

// spamIndex returns the position of M(u, v, w) inside a 343-value block.
func spamIndex(u, v, w int) int {
	return ((w+spamT)*spamBins+(v+spamT))*spamBins + (u + spamT)
}

// SPAM returns the second-order SPAM feature vector (T = 3) of one 8-bit plane
// of a width×height image stored row-major in plane (len(plane) == width*height).
// It returns an error for mismatched sizes or planes too small to have any
// second-order transitions in some direction.
//
// Every direction needs four collinear pixels, so both width and height must
// be at least 4. See the comment at the top of spam.go for the exact feature
// definition and index order.
func SPAM(plane []uint8, width, height int) ([]float64, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("spam: invalid dimensions %d×%d", width, height)
	}
	if len(plane) != width*height {
		return nil, fmt.Errorf("spam: plane has %d values, want %d×%d = %d",
			len(plane), width, height, width*height)
	}
	if width < 4 || height < 4 {
		return nil, fmt.Errorf("spam: plane %d×%d too small, need at least 4×4", width, height)
	}
	out := make([]float64, SPAMDim)
	var m [spamBlock]float64
	for i, d := range spamDirs {
		spamDirection(plane, width, height, d[0], d[1], &m)
		half := out[(i/4)*spamBlock : (i/4+1)*spamBlock]
		for k, p := range m {
			half[k] += p / 4
		}
	}
	return out, nil
}

// spamDirection fills m with the second-order transition matrix M_d of plane
// for the walking direction d = (dx, dy), with dx, dy ∈ {−1, 0, 1}. The plane
// must be at least 4×4.
func spamDirection(plane []uint8, width, height, dx, dy int, m *[spamBlock]float64) {
	var counts [spamBlock]uint32

	// Start positions p such that p, p+d, p+2d, p+3d are all inside.
	x0, x1 := spanFor(dx, width)
	y0, y1 := spanFor(dy, height)
	step := dy*width + dx

	for y := y0; y < y1; y++ {
		row := y * width
		for x := x0; x < x1; x++ {
			p := row + x
			a := int(plane[p])
			b := int(plane[p+step])
			c := int(plane[p+2*step])
			e := int(plane[p+3*step])
			wi := int(spamClip[a-b+255])
			vi := int(spamClip[b-c+255])
			ui := int(spamClip[c-e+255])
			counts[(wi*spamBins+vi)*spamBins+ui]++
		}
	}

	for blk := 0; blk < spamBlock; blk += spamBins {
		var total uint32
		for k := blk; k < blk+spamBins; k++ {
			total += counts[k]
		}
		for k := blk; k < blk+spamBins; k++ {
			if total == 0 {
				m[k] = 0
			} else {
				m[k] = float64(counts[k]) / float64(total)
			}
		}
	}
}

// spanFor returns the half-open range of start coordinates along one axis for
// which start, start+s, start+2s, start+3s all lie in [0, n).
func spanFor(s, n int) (lo, hi int) {
	switch s {
	case 1:
		return 0, n - 3
	case -1:
		return 3, n
	default:
		return 0, n
	}
}
