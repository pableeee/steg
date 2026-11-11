package cursors

import (
	"crypto/sha256"
	"fmt"
	"math"
	"math/rand/v2"
)

// Everything in this file defines the on-image format: changing any of it
// changes which pixel each stream bit lands on and breaks every existing image.
// TestPixelOrderGolden pins the output.

// publicOrderKey keys the ChaCha8 stream for the public prefix of the order.
// It is a fixed, published constant (not a secret): the prefix holds the salt,
// which a decoder must locate before it can derive any key.
var publicOrderKey = sha256.Sum256([]byte("github.com/pableeee/steg/v2 salt positions"))

// MaxPixels is the largest number of pixels a carrier may have: the order
// stores each pixel as a uint32 index (4 bytes per pixel rather than the 16 of
// an image.Point), so indices must stay below 2^32 (and fit an int on 32-bit
// platforms).
const MaxPixels = min(math.MaxUint32, math.MaxInt)

// PixelCount returns width×height, or an error if either dimension is not
// positive or the product exceeds MaxPixels. It never overflows.
func PixelCount(width, height int) (int, error) {
	if width <= 0 || height <= 0 {
		return 0, fmt.Errorf("cursors: invalid image dimensions %dx%d", width, height)
	}
	if int64(width) > MaxPixels/int64(height) {
		return 0, fmt.Errorf("cursors: image %dx%d has more than %d pixels", width, height, int64(MaxPixels))
	}
	return width * height, nil
}

// NewPixelOrder returns the pixel indices of a width×height image in row-major
// order with the first publicLen steps of a Fisher-Yates shuffle applied, driven
// by a ChaCha8 stream under the public key. order[:publicLen] therefore depend
// only on the dimensions and publicLen; order[publicLen:] are not yet shuffled
// and must be finished with FinishPixelOrder before use.
//
// Index i is the pixel at (i % width, i / width) relative to Bounds().Min.
func NewPixelOrder(width, height, publicLen int) ([]uint32, error) {
	n, err := PixelCount(width, height)
	if err != nil {
		return nil, err
	}
	if publicLen < 0 || publicLen > n {
		return nil, fmt.Errorf("cursors: image has %d pixels, need at least %d", n, publicLen)
	}
	order := make([]uint32, n)
	for i := range order {
		order[i] = uint32(i)
	}
	shuffleRange(order, 0, publicLen, publicOrderKey)
	return order, nil
}

// FinishPixelOrder continues the Fisher-Yates shuffle started by NewPixelOrder
// over order[publicLen:], driven by a ChaCha8 stream keyed with orderKey. The
// result is a permutation of all pixels whose public prefix is unchanged.
func FinishPixelOrder(order []uint32, publicLen int, orderKey [32]byte) {
	shuffleRange(order, publicLen, len(order), orderKey)
}

// shuffleRange performs Fisher-Yates steps from..to-1 over the whole slice:
// step i swaps order[i] with order[i+uniform(n-i)]. The last step (i = n-1)
// is always the identity and is skipped, so it consumes no stream output.
func shuffleRange(order []uint32, from, to int, key [32]byte) {
	n := len(order)
	if to > n-1 {
		to = n - 1
	}
	src := rand.NewChaCha8(key)
	for i := from; i < to; i++ {
		j := i + int(uniform(src, uint64(n-i)))
		order[i], order[j] = order[j], order[i]
	}
}

// uniform returns an unbiased value in [0, n) for n > 0 from src's raw 64-bit
// output, by rejection sampling: draws below (2^64 - n) mod n are discarded so
// the accepted range is an exact multiple of n. Format-defining: this is used
// instead of rand.Rand.IntN/Shuffle, whose algorithms carry no stability
// guarantee, while ChaCha8.Uint64 is fixed by the chacha8rand spec.
func uniform(src *rand.ChaCha8, n uint64) uint64 {
	threshold := -n % n
	for {
		if x := src.Uint64(); x >= threshold {
			return x % n
		}
	}
}
