package analysis

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// photoPlane returns a deterministic w×h 8-bit plane that behaves like one
// channel of a photograph: smooth low-frequency structure (a gradient plus two
// sinusoids, with amplitudes, frequencies and phases drawn from seed) plus
// Gaussian sensor-style noise with σ = 2, rounded and clamped to [0, 255].
// Different seeds give different scenes, not only different noise.
func photoPlane(w, h int, seed uint64) []uint8 {
	return photoPlaneNoise(w, h, seed, seed)
}

// photoPlaneNoise is photoPlane with the scene drawn from sceneSeed and the
// noise from noiseSeed, so the same scene can be re-shot with fresh noise.
func photoPlaneNoise(w, h int, sceneSeed, noiseSeed uint64) []uint8 {
	rng := rand.New(rand.NewPCG(sceneSeed, 0x5eed))
	// Ranges keep base ± gradients ± sinusoids inside [0, 250], so only the
	// noise tail ever clips: saturated flat regions would not be photo-like.
	base := 100 + 50*rng.Float64()
	gx, gy := 50*rng.Float64()-25, 50*rng.Float64()-25
	a1, f1, p1 := 10+15*rng.Float64(), 3+6*rng.Float64(), 2*math.Pi*rng.Float64()
	a2, f2, p2 := 10+15*rng.Float64(), 3+6*rng.Float64(), 2*math.Pi*rng.Float64()
	noise := rand.New(rand.NewPCG(noiseSeed, 0x2015e))
	plane := make([]uint8, w*h)
	for y := range h {
		for x := range w {
			fx, fy := float64(x)/float64(w), float64(y)/float64(h)
			v := base + gx*fx + gy*fy +
				a1*math.Sin(f1*fy+0.5*f1*fx+p1) +
				a2*math.Cos(f2*fx-0.3*f2*fy+p2) +
				2*noise.NormFloat64()
			plane[y*w+x] = uint8(math.Max(0, math.Min(255, math.Round(v))))
		}
	}
	return plane
}

// lsbMatch returns a copy of plane after LSB matching (±1 embedding) at the
// given rate: each pixel carries a random message bit with probability rate;
// when its LSB differs from the bit, the pixel is moved by +1 or −1 at random
// (always inwards at 0 and 255). At rate 1 about half the pixels change.
func lsbMatch(plane []uint8, rate float64, seed uint64) []uint8 {
	rng := rand.New(rand.NewPCG(seed, 0xe3bed))
	out := make([]uint8, len(plane))
	copy(out, plane)
	for i, p := range out {
		if rng.Float64() >= rate {
			continue
		}
		bit := uint8(rng.IntN(2))
		if p&1 == bit {
			continue
		}
		switch {
		case p == 0:
			out[i] = 1
		case p == 255:
			out[i] = 254
		case rng.IntN(2) == 0:
			out[i] = p - 1
		default:
			out[i] = p + 1
		}
	}
	return out
}

func l1(a, b []float64) float64 {
	s := 0.0
	for i := range a {
		s += math.Abs(a[i] - b[i])
	}
	return s
}

func TestSPAMLength(t *testing.T) {
	f, err := SPAM(photoPlane(64, 48, 1), 64, 48)
	require.NoError(t, err)
	assert.Len(t, f, SPAMDim)
	assert.Equal(t, 686, SPAMDim)
}

// TestSPAMConstantPlane: every difference of a constant plane is 0, so in all
// 8 directions the only observed (w, v) pair is (0, 0) and it is always
// followed by u = 0: M(0,0,0) = 1 and every other entry is 0 (empty blocks are
// 0/0 := 0). Both averages are therefore the indicator of index
// spamIndex(0,0,0) = (3·7+3)·7+3 = 171, i.e. features 171 and 343+171 = 514.
func TestSPAMConstantPlane(t *testing.T) {
	require.Equal(t, 171, spamIndex(0, 0, 0))
	plane := make([]uint8, 10*7)
	for i := range plane {
		plane[i] = 137
	}
	f, err := SPAM(plane, 10, 7)
	require.NoError(t, err)
	for i, v := range f {
		if i == 171 || i == 514 {
			assert.Equal(t, 1.0, v, "feature %d", i)
		} else {
			assert.Equal(t, 0.0, v, "feature %d", i)
		}
	}
}

// TestSPAMDirectionHandComputed pins individual direction matrices on a 4×4
// plane small enough to enumerate by hand.
//
//	row 0:  0 1 3 7
//	row 1:  0 1 3 4
//	row 2:  0 1 3 7
//	row 3:  5 5 5 5
func TestSPAMDirectionHandComputed(t *testing.T) {
	plane := []uint8{
		0, 1, 3, 7,
		0, 1, 3, 4,
		0, 1, 3, 7,
		5, 5, 5, 5,
	}
	type entry struct {
		u, v, w int
		p       float64
	}
	cases := []struct {
		name   string
		dx, dy int
		want   []entry
	}{
		// → one chain per row, D = X(x) − X(x+1):
		//   rows 0, 2: (−1, −2, −4→−3); row 1: (−1, −2, −1); row 3: (0, 0, 0).
		{"right", 1, 0, []entry{
			{u: -3, v: -2, w: -1, p: 2.0 / 3},
			{u: -1, v: -2, w: -1, p: 1.0 / 3},
			{u: 0, v: 0, w: 0, p: 1},
		}},
		// ← walks from x=3 leftwards, D = X(x) − X(x−1):
		//   rows 0, 2: (4→3, 2, 1); row 1: (1, 2, 1); row 3: (0, 0, 0).
		{"left", -1, 0, []entry{
			{u: 1, v: 2, w: 3, p: 1},
			{u: 1, v: 2, w: 1, p: 1},
			{u: 0, v: 0, w: 0, p: 1},
		}},
		// ↓ one chain per column, D = X(y) − X(y+1):
		//   cols 0, 1: (0, 0, −5→−3 / −4→−3); col 2: (0, 0, −2); col 3: (3, −3, 2).
		{"down", 0, 1, []entry{
			{u: -3, v: 0, w: 0, p: 2.0 / 3},
			{u: -2, v: 0, w: 0, p: 1.0 / 3},
			{u: 2, v: -3, w: 3, p: 1},
		}},
		// ↘ only from (0,0): 0, 1, 3, 5 → (−1, −2, −2).
		{"down-right", 1, 1, []entry{
			{u: -2, v: -2, w: -1, p: 1},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m [spamBlock]float64
			spamDirection(plane, 4, 4, tc.dx, tc.dy, &m)
			want := make(map[int]float64)
			for _, e := range tc.want {
				want[spamIndex(e.u, e.v, e.w)] = e.p
			}
			for i, v := range m {
				assert.InDelta(t, want[i], v, 1e-15, "index %d", i)
			}
		})
	}
}

// TestSPAMBlocksAreConditionalDistributions: within one direction every block
// of 7 is Pr(· | v, w), so it sums to 1, or to 0 when (w, v) never occurs.
// F1 and F2 average four such matrices, so their blocks sum to k/4 for the
// number k of directions in which the pair occurs; on a noisy plane every
// pair occurs in every direction often enough that the sums are all 1.
func TestSPAMBlocksAreConditionalDistributions(t *testing.T) {
	const w, h = 128, 96
	for _, plane := range [][]uint8{photoPlane(w, h, 7), lsbMatch(photoPlane(w, h, 7), 1, 1)} {
		var m [spamBlock]float64
		for _, d := range spamDirs {
			spamDirection(plane, w, h, d[0], d[1], &m)
			for blk := 0; blk < spamBlock; blk += spamBins {
				s := 0.0
				for _, v := range m[blk : blk+spamBins] {
					s += v
				}
				if s != 0 {
					assert.InDelta(t, 1, s, 1e-12, "direction %v block %d", d, blk/spamBins)
				}
			}
		}

		f, err := SPAM(plane, w, h)
		require.NoError(t, err)
		empty := 0
		for blk := 0; blk < SPAMDim; blk += spamBins {
			s := 0.0
			for _, v := range f[blk : blk+spamBins] {
				s += v
			}
			q := s * 4
			assert.InDelta(t, math.Round(q), q, 1e-12, "block %d sum %v not a multiple of 1/4", blk/spamBins, s)
			assert.True(t, s >= 0 && s <= 1+1e-12, "block %d sum %v", blk/spamBins, s)
			if s == 0 {
				empty++
			}
		}
		// Strong (w, v) pairs like (3, −3) are rare on a σ = 2 plane, but the
		// vast majority of the 98 blocks must be populated.
		assert.Less(t, empty, 10)
	}

	// A vertical ramp X(x, y) = y: horizontal differences are all 0, ↓ sees
	// only (−1, −1, −1) and ↑ only (1, 1, 1). So the F1 block for
	// (w, v) = (−1, −1) is fed by ↓ alone and sums to exactly 1/4.
	ramp := make([]uint8, 16*16)
	for y := range 16 {
		for x := range 16 {
			ramp[y*16+x] = uint8(y)
		}
	}
	f, err := SPAM(ramp, 16, 16)
	require.NoError(t, err)
	blk := spamIndex(-3, -1, -1) // start of the (w, v) = (−1, −1) block
	assert.InDelta(t, 0.25, f[spamIndex(-1, -1, -1)], 1e-15, "↓ contributes 1/4")
	assert.InDelta(t, 0.25, f[spamIndex(1, 1, 1)], 1e-15, "↑ contributes 1/4")
	s := 0.0
	for _, v := range f[blk : blk+spamBins] {
		s += v
	}
	assert.InDelta(t, 0.25, s, 1e-15)
}

func TestSPAMErrors(t *testing.T) {
	cases := []struct {
		name string
		n    int
		w, h int
	}{
		{"size mismatch", 15, 4, 4},
		{"zero width", 0, 0, 4},
		{"negative height", 4, 4, -1},
		{"too narrow", 3 * 10, 3, 10},
		{"too short", 10 * 3, 10, 3},
		{"single row", 100, 100, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := SPAM(make([]uint8, tc.n), tc.w, tc.h)
			assert.Error(t, err)
		})
	}
	_, err := SPAM(make([]uint8, 16), 4, 4)
	assert.NoError(t, err, "4×4 is the smallest valid plane")
}

func TestSPAMDeterministic(t *testing.T) {
	plane := photoPlane(97, 61, 3)
	a, err := SPAM(plane, 97, 61)
	require.NoError(t, err)
	b, err := SPAM(plane, 97, 61)
	require.NoError(t, err)
	assert.Equal(t, a, b)
}

// TestSPAMSeparatesLSBMatching: ±1 embedding at 100% rate adds independent
// ±1 noise to about half the pixels, which flattens the distribution of
// differences. On a σ = 2 plane that is only 0.5 of extra variance against 4,
// so on any single image the shift is about as large as the sampling noise of
// the 686 conditional probabilities. It is, however, systematic, which is what
// the classifier exploits, so the test measures it the same way:
//
//   - Averaged over 16 scenes (256×256), the L1 distance from the clean mean
//     vector to the stego mean vector is compared with the distance to the
//     mean of the same scenes re-shot with fresh noise. Calibration: stego
//     4.69, noise re-shoot 0.95. The assertion asks for 2.5×.
//   - The probability that a flat run stays flat, F1 at (u, v, w) = (0, 0, 0),
//     drops under embedding: calibration 16 of 16 scenes; asserted ≥ 13.
func TestSPAMSeparatesLSBMatching(t *testing.T) {
	const w, h, scenes = 256, 256, 16
	i0 := spamIndex(0, 0, 0)
	meanClean := make([]float64, SPAMDim)
	meanStego := make([]float64, SPAMDim)
	meanRedraw := make([]float64, SPAMDim)
	drops := 0
	for seed := uint64(1); seed <= scenes; seed++ {
		clean := photoPlane(w, h, seed)
		fClean, err := SPAM(clean, w, h)
		require.NoError(t, err)
		fStego, err := SPAM(lsbMatch(clean, 1, seed), w, h)
		require.NoError(t, err)
		fRedraw, err := SPAM(photoPlaneNoise(w, h, seed, seed+1000), w, h)
		require.NoError(t, err)
		for i := range SPAMDim {
			meanClean[i] += fClean[i] / scenes
			meanStego[i] += fStego[i] / scenes
			meanRedraw[i] += fRedraw[i] / scenes
		}
		if fStego[i0] < fClean[i0] {
			drops++
		}
	}
	dStego, dNoise := l1(meanClean, meanStego), l1(meanClean, meanRedraw)
	t.Logf("mean over %d scenes: L1(clean, stego) = %.3f, L1(clean, noise re-shoot) = %.3f; P(0|0,0) dropped in %d/%d",
		scenes, dStego, dNoise, drops, scenes)
	assert.Greater(t, dStego, 2.5*dNoise)
	assert.GreaterOrEqual(t, drops, 13)
}

func BenchmarkSPAM512(b *testing.B) {
	plane := photoPlane(512, 512, 1)
	b.ResetTimer()
	for b.Loop() {
		if _, err := SPAM(plane, 512, 512); err != nil {
			b.Fatal(err)
		}
	}
}
