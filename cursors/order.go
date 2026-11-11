package cursors

import (
	"crypto/sha256"
	"fmt"
	"image"
	"math/rand/v2"
)

// Everything in this file defines the on-image format: changing any of it
// changes which pixel each stream bit lands on and breaks every existing image.
// TestPixelOrderGolden pins the output.

// publicOrderKey keys the ChaCha8 stream for the public prefix of the order.
// It is a fixed, published constant (not a secret): the prefix holds the salt,
// which a decoder must locate before it can derive any key.
var publicOrderKey = sha256.Sum256([]byte("github.com/pableeee/steg/v2 salt positions"))

// NewPixelOrder returns the pixel indices of a width×height image in row-major
// order with the first publicLen steps of a Fisher-Yates shuffle applied, driven
// by a ChaCha8 stream under the public key. points[:publicLen] therefore depend
// only on the dimensions and publicLen; points[publicLen:] are not yet shuffled
// and must be finished with FinishPixelOrder before use.
//
// Points cover [0,width)×[0,height); RNGCursor offsets them by Bounds().Min.
func NewPixelOrder(width, height, publicLen int) ([]image.Point, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("cursors: invalid image dimensions %dx%d", width, height)
	}
	n := width * height
	if publicLen < 0 || publicLen > n {
		return nil, fmt.Errorf("cursors: image has %d pixels, need at least %d", n, publicLen)
	}
	points := make([]image.Point, n)
	for i := range points {
		points[i] = image.Point{X: i % width, Y: i / width}
	}
	shuffleRange(points, 0, publicLen, publicOrderKey)
	return points, nil
}

// FinishPixelOrder continues the Fisher-Yates shuffle started by NewPixelOrder
// over points[publicLen:], driven by a ChaCha8 stream keyed with orderKey. The
// result is a permutation of all pixels whose public prefix is unchanged.
func FinishPixelOrder(points []image.Point, publicLen int, orderKey [32]byte) {
	shuffleRange(points, publicLen, len(points), orderKey)
}

// shuffleRange performs Fisher-Yates steps from..to-1 over the whole slice:
// step i swaps points[i] with points[i+uniform(n-i)]. The last step (i = n-1)
// is always the identity and is skipped, so it consumes no stream output.
func shuffleRange(points []image.Point, from, to int, key [32]byte) {
	n := len(points)
	if to > n-1 {
		to = n - 1
	}
	src := rand.NewChaCha8(key)
	for i := from; i < to; i++ {
		j := i + int(uniform(src, uint64(n-i)))
		points[i], points[j] = points[j], points[i]
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
