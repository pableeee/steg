package main

import (
	"image"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/pableeee/steg/cursors"
	"github.com/pableeee/steg/steg"
)

// cursorWrite writes, through a real cursors.RNGCursor in row-major order over
// the R channel at 1 bit per channel, bit bits[i] into a pixel whose R value
// is vals[i], and returns the resulting R values. len(vals) must be a
// multiple of 8.
func cursorWrite(t *testing.T, vals, bits []uint8, opts ...cursors.Option) []uint8 {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, len(vals), 1))
	for i, v := range vals {
		img.Pix[4*i] = v
		img.Pix[4*i+3] = 255
	}
	cur, err := cursors.NewRNGCursor(img, opts...)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(bits); i += 8 {
		var b byte
		for k := 0; k < 8; k++ {
			b = b<<1 | bits[i+k]
		}
		if err := cur.WriteByte(b); err != nil {
			t.Fatal(err)
		}
	}
	out := make([]uint8, len(vals))
	for i := range out {
		out[i] = img.Pix[4*i]
	}
	return out
}

// allPairs returns every (value, bit) combination, 512 entries.
func allPairs() (vals, bits []uint8) {
	for v := 0; v < 256; v++ {
		for b := uint8(0); b < 2; b++ {
			vals = append(vals, uint8(v))
			bits = append(bits, b)
		}
	}
	return vals, bits
}

func TestWriteReplacementMatchesCursor(t *testing.T) {
	vals, bits := allPairs()
	got := cursorWrite(t, vals, bits)
	for i := range vals {
		want := writeReplacement(vals[i], bits[i], nil)
		if got[i] != want {
			t.Fatalf("v=%d bit=%d: cursor wrote %d, writeReplacement %d", vals[i], bits[i], got[i], want)
		}
		if want&1 != bits[i] || want&^1 != vals[i]&^1 {
			t.Fatalf("v=%d bit=%d: writeReplacement gave %d, changing more than bit 0", vals[i], bits[i], want)
		}
	}
}

func TestWriteMatchingMatchesCursor(t *testing.T) {
	vals, bits := allPairs()
	rng := rand.New(rand.NewPCG(1, 2))
	ups, downs := 0, 0
	// The cursor's directions are crypto-random, so compare the cursor and
	// the rule against the same invariants, and exactly where they are
	// deterministic (no change needed, or saturation).
	for trial := 0; trial < 50; trial++ {
		cur := cursorWrite(t, vals, bits, cursors.WithLSBMatching())
		for i := range vals {
			v, b := vals[i], bits[i]
			sim := writeMatching(v, b, rng)
			for _, x := range []struct {
				who string
				out uint8
			}{{"cursor", cur[i]}, {"writeMatching", sim}} {
				if x.out&1 != b {
					t.Fatalf("%s: v=%d bit=%d gave %d: bit 0 is not the message bit", x.who, v, b, x.out)
				}
				d := int(x.out) - int(v)
				switch {
				case v&1 == b && d != 0:
					t.Fatalf("%s: v=%d bit=%d changed to %d although bit 0 matched", x.who, v, b, x.out)
				case v&1 != b && d != 1 && d != -1:
					t.Fatalf("%s: v=%d bit=%d gave %d: change is not ±1", x.who, v, b, x.out)
				}
			}
			if v&1 == b || v == 0 || v == 255 {
				if cur[i] != sim {
					t.Fatalf("v=%d bit=%d: cursor %d, writeMatching %d", v, b, cur[i], sim)
				}
			}
			if v&1 != b && v != 0 && v != 255 {
				if sim > v {
					ups++
				} else {
					downs++
				}
			}
		}
	}
	if writeMatching(0, 1, rng) != 1 || writeMatching(255, 0, rng) != 254 {
		t.Fatal("saturation: 0 must go up to 1 and 255 down to 254")
	}
	if frac := float64(ups) / float64(ups+downs); math.Abs(frac-0.5) > 0.03 {
		t.Fatalf("writeMatching went up %.3f of the time, want about 0.5", frac)
	}
}

func TestEmbedSimCounts(t *testing.T) {
	const n = 64 * 64
	cover := make([]uint8, n)
	crng := rand.New(rand.NewPCG(3, 4))
	for i := range cover {
		cover[i] = uint8(crng.IntN(256))
	}
	cover[0], cover[1] = 0, 255 // saturated values somewhere in the plane
	orig := append([]uint8(nil), cover...)
	dst := make([]uint8, n)
	perm := make([]int32, n)
	for _, method := range []string{methodReplacement, methodMatching} {
		for _, rate := range []float64{0.0001, 0.05, 0.37, 1.0} {
			rng := rand.New(rand.NewChaCha8(deriveSeed("t", 1, method, rateKey(rate))))
			written, changed := embedSim(dst, cover, rate, ruleFor(method), rng, perm)
			if want := int(math.Round(rate * n)); written != want {
				t.Fatalf("%s @ %v: wrote %d positions, want %d", method, rate, written, want)
			}
			// The positions are distinct: perm[:written] has no duplicates.
			seen := make(map[int32]bool, written)
			for _, p := range perm[:written] {
				if seen[p] {
					t.Fatalf("%s @ %v: position %d chosen twice", method, rate, p)
				}
				seen[p] = true
			}
			diff := 0
			for i := range dst {
				if dst[i] == cover[i] {
					continue
				}
				diff++
				if !seen[int32(i)] {
					t.Fatalf("%s @ %v: unchosen position %d changed", method, rate, i)
				}
				d := int(dst[i]) - int(cover[i])
				if method == methodReplacement && dst[i]^cover[i] != 1 {
					t.Fatalf("replacement changed %d to %d", cover[i], dst[i])
				}
				if method == methodMatching && d != 1 && d != -1 {
					t.Fatalf("matching changed %d to %d", cover[i], dst[i])
				}
			}
			if diff != changed {
				t.Fatalf("%s @ %v: reported %d changes, counted %d", method, rate, changed, diff)
			}
			// About half the written positions change: binomial(written, 1/2).
			if written >= 200 {
				sd := math.Sqrt(float64(written)) / 2
				if math.Abs(float64(changed)-float64(written)/2) > 5*sd {
					t.Fatalf("%s @ %v: %d of %d changed, want about half", method, rate, changed, written)
				}
			}
			for i := range cover {
				if cover[i] != orig[i] {
					t.Fatal("embedSim modified the cover")
				}
			}
		}
	}
}

func TestEmbedSimDeterministic(t *testing.T) {
	cover := make([]uint8, 1000)
	for i := range cover {
		cover[i] = uint8(i * 7)
	}
	run := func() []uint8 {
		dst := make([]uint8, len(cover))
		rng := rand.New(rand.NewChaCha8(deriveSeed("embed", 9, "img.pgm", methodMatching, "0.4")))
		embedSim(dst, cover, 0.4, writeMatching, rng, make([]int32, len(cover)))
		return dst
	}
	a, b := run(), run()
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("same seed gave different stego planes")
		}
	}
}

func TestPlanTool512(t *testing.T) {
	const w, h = 512, 512
	capacity := steg.CapacityForDims(w, h, 1, 1) // 32712
	want := map[float64]int{                     // requested rate -> block
		0.0035: 64, 0.0094: 256, 0.05: 2048, 0.1: 4096, 0.2: 8192, 0.4: 16384, 1.0: capacity + 4,
	}
	for rate, block := range want {
		p, err := planTool(w, h, rate)
		if err != nil {
			t.Fatal(err)
		}
		if p.block != block {
			t.Fatalf("rate %v: block %d, want %d", rate, p.block, block)
		}
		if got := float64(toolFraming+p.block) * 8 / (w * h); p.effRate != got {
			t.Fatalf("rate %v: effRate %v, want %v", rate, p.effRate, got)
		}
		if p.payloadLen > capacity {
			t.Fatalf("rate %v: payload %d exceeds capacity %d", rate, p.payloadLen, capacity)
		}
	}
}

// The tool method's measured changes agree with its planned rate: the encoder
// writes effRate × N bits, about half of which change a pixel, and every
// change is ±1 (LSB matching).
func TestEmbedToolMatchesPlan(t *testing.T) {
	const w, h = 256, 256
	cover := make([]uint8, w*h)
	crng := rand.New(rand.NewPCG(5, 6))
	for i := range cover {
		cover[i] = uint8(crng.IntN(256))
	}
	for _, rate := range []float64{0.1, 1.0} {
		plan, err := planTool(w, h, rate)
		if err != nil {
			t.Fatal(err)
		}
		dst := make([]uint8, len(cover))
		changed, err := embedTool(dst, cover, w, h, plan, rand.New(rand.NewPCG(7, 8)))
		if err != nil {
			t.Fatal(err)
		}
		for i := range dst {
			if d := int(dst[i]) - int(cover[i]); d < -1 || d > 1 {
				t.Fatalf("pixel %d changed by %d", i, d)
			}
		}
		bitsWritten := plan.effRate * w * h
		sd := math.Sqrt(bitsWritten) / 2
		if math.Abs(float64(changed)-bitsWritten/2) > 5*sd {
			t.Fatalf("rate %v (eff %v): %d changes, want about %.0f", rate, plan.effRate, changed, bitsWritten/2)
		}
	}
}
