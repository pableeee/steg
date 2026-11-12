package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"image"
	"math"
	"math/rand/v2"
	"strconv"

	"github.com/pableeee/steg/steg"
)

// Embedding methods.
const (
	methodReplacement = "replacement" // simulated LSB replacement
	methodMatching    = "matching"    // simulated LSB matching (±1)
	methodTool        = "tool"        // the real steg.Encode, 1 channel × 1 bit
)

var knownMethods = []string{methodReplacement, methodMatching, methodTool}

// writeRule writes message bit (0 or 1) into the cover value v and returns the
// stego value. rng supplies any randomness the rule needs.
type writeRule func(v, bit uint8, rng *rand.Rand) uint8

// writeReplacement is LSB replacement: bit 0 is set to the message bit and
// nothing else changes. It matches a cursors.RNGCursor without
// WithLSBMatching at 1 bit per channel.
func writeReplacement(v, bit uint8, _ *rand.Rand) uint8 {
	return v&^1 | bit&1
}

// writeMatching is LSB matching: v is left alone when its bit 0 already equals
// the message bit; otherwise it moves by +1 or −1 with equal probability, only
// up at 0 and only down at 255. It matches a cursors.RNGCursor with
// WithLSBMatching at 1 bit per channel, which draws its directions from its
// own generator; here they come from rng. A random bit is consumed only when
// a change is needed.
func writeMatching(v, bit uint8, rng *rand.Rand) uint8 {
	if v&1 == bit&1 {
		return v
	}
	switch v {
	case 0:
		return 1
	case 255:
		return 254
	}
	if rng.Uint64()&1 == 1 {
		return v + 1
	}
	return v - 1
}

func ruleFor(method string) writeRule {
	switch method {
	case methodReplacement:
		return writeReplacement
	case methodMatching:
		return writeMatching
	}
	return nil
}

// deriveSeed hashes a domain label, the run seed and any further parts into a
// ChaCha8 seed. Every random stream in the benchmark comes from here, keyed by
// what it is for, so streams are independent of each other and of the order
// in which workers happen to run.
func deriveSeed(label string, seed int64, parts ...string) [32]byte {
	h := sha256.New()
	h.Write([]byte("stegbench/" + label))
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(seed))
	h.Write(b[:])
	for _, p := range parts {
		binary.LittleEndian.PutUint64(b[:], uint64(len(p)))
		h.Write(b[:])
		h.Write([]byte(p))
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func rateKey(rate float64) string { return strconv.FormatFloat(rate, 'g', -1, 64) }

// embedSim simulates embedding at rate bits per pixel into cover, writing the
// stego plane to dst (len(dst) == len(cover)). It picks round(rate × N)
// distinct positions uniformly at random (a partial Fisher-Yates shuffle over
// perm, a caller-provided scratch slice of length N whose contents are
// overwritten), draws a uniform message bit for each, and applies rule. It
// returns the number of positions written and the number of values changed.
func embedSim(dst, cover []uint8, rate float64, rule writeRule, rng *rand.Rand, perm []int32) (written, changed int) {
	n := len(cover)
	copy(dst, cover)
	k := int(math.Round(rate * float64(n)))
	if k > n {
		k = n
	}
	for i := range perm[:n] {
		perm[i] = int32(i)
	}
	var bits uint64
	left := 0
	for i := 0; i < k; i++ {
		j := i + rng.IntN(n-i)
		perm[i], perm[j] = perm[j], perm[i]
		pos := perm[i]
		if left == 0 {
			bits, left = rng.Uint64(), 64
		}
		bit := uint8(bits & 1)
		bits >>= 1
		left--
		v := cover[pos]
		nv := rule(v, bit, rng)
		dst[pos] = nv
		if nv != v {
			changed++
		}
	}
	return k, changed
}

// toolPassword is the fixed password the tool method encodes with.
var toolPassword = []byte("stegbench-fixed-password")

// Framing written by the encoder around the padded block: 16-byte salt,
// 4-byte container length, 32-byte HMAC tag. The padded block itself already
// contains the 4-byte real-length prefix. Together with that prefix these
// make up steg.Overhead.
const toolFraming = 16 + 4 + 32

// toolPlan is how the tool method embeds into a w × h plane for a requested
// rate: the payload length to pass to steg.Encode and the bits per pixel the
// encoder then writes.
type toolPlan struct {
	payloadLen int
	block      int     // padded block: real-length prefix + payload + padding
	effRate    float64 // (toolFraming + block) × 8 / N
}

// planTool picks the payload size whose padded block gives the embedding rate
// closest to rate. Bounded padding makes the block a power-of-two bucket of
// at least steg.MinPaddedBlock bytes, or the whole capacity (CapacityBytes + 4) when the
// bucket would not fit; so only those block sizes are reachable, and the
// payload chosen for a bucket B is B − 4 bytes, for the full capacity
// CapacityBytes. Ties go to the smaller block.
func planTool(w, h int, rate float64) (toolPlan, error) {
	capacity := steg.CapacityForDims(w, h, 1, 1)
	if capacity <= 0 {
		return toolPlan{}, fmt.Errorf("image %dx%d too small for the tool method", w, h)
	}
	n := float64(w) * float64(h)
	eff := func(block int) float64 { return float64(toolFraming+block) * 8 / n }

	// Candidates in increasing block size; a strictly closer one replaces the
	// current best, so ties keep the smaller block.
	full := capacity + 4
	var cands []toolPlan
	for b := steg.MinPaddedBlock; b < full; b <<= 1 {
		cands = append(cands, toolPlan{payloadLen: b - 4, block: b, effRate: eff(b)})
	}
	cands = append(cands, toolPlan{payloadLen: capacity, block: full, effRate: eff(full)})
	best := cands[0]
	for _, c := range cands[1:] {
		if math.Abs(c.effRate-rate) < math.Abs(best.effRate-rate) {
			best = c
		}
	}
	return best, nil
}

// embedTool runs the real encoder: the plane goes into R, G and B of an RGBA
// image (alpha 255), steg.Encode hides a random payload of plan.payloadLen
// bytes with 1 channel (R) × 1 bit, and the R plane is copied to dst. Only R
// is written by the encoder, so G and B are irrelevant to the result. The
// payload comes from rng; the salt and the ±1 directions come from
// crypto/rand inside the encoder, so the output is not reproducible.
func embedTool(dst, cover []uint8, w, h int, plan toolPlan, rng *rand.Rand) (changed int, err error) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i, v := range cover {
		img.Pix[4*i] = v
		img.Pix[4*i+1] = v
		img.Pix[4*i+2] = v
		img.Pix[4*i+3] = 255
	}
	payload := make([]byte, plan.payloadLen)
	for i := range payload {
		payload[i] = byte(rng.Uint32())
	}
	if err := steg.Encode(img, toolPassword, bytes.NewReader(payload), 1, 1); err != nil {
		return 0, err
	}
	for i := range dst {
		dst[i] = img.Pix[4*i]
		if dst[i] != cover[i] {
			changed++
		}
	}
	return changed, nil
}
