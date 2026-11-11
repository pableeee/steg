package steg

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	"io"

	"github.com/pableeee/steg/cursors"
	"golang.org/x/crypto/argon2"
)

// cursorOptions builds the RNGCursor option slice for the given configuration.
// channels: 1 = R only, 2 = R+G, 3 = R+G+B. points is the traversal order from
// prepareEncode/prepareDecode; it is shared, never copied or modified.
func cursorOptions(points []image.Point, bitsPerChannel, channels int) []cursors.Option {
	opts := []cursors.Option{
		cursors.WithSharedPoints(points),
		cursors.WithBitsPerChannel(bitsPerChannel),
	}
	if channels >= 2 {
		opts = append(opts, cursors.UseGreenBit())
	}
	if channels >= 3 {
		opts = append(opts, cursors.UseBlueBit())
	}
	return opts
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func lcm(a, b int) int         { return a / gcd(a, b) * b }
func lcmBytes(bpb, bc int) int { return lcm(bpb, bc) / bpb }

// saltBytes is the size of the plaintext random salt at the start of the stream.
const saltBytes = 16

// saltPixels returns k = ceil(128 / bitsPerPixel), the number of pixels that
// hold salt bits. They are the public prefix of the traversal order. The last
// of them may also hold the first bits of the encrypted container.
func saltPixels(bitsPerPixel int) int {
	return (saltBytes*8 + bitsPerPixel - 1) / bitsPerPixel
}

// mainKeys is everything derived from the password and the per-image salt.
type mainKeys struct {
	encKey   []byte   // AES-128 key for the payload stream cipher
	macKey   []byte   // HMAC-SHA256 key for the container tag
	nonce    uint32   // CTR nonce for the payload stream cipher
	orderKey [32]byte // ChaCha8 key for the secret part of the pixel order
}

// deriveMainKeys stretches pass using Argon2id with the per-image random salt.
// Output layout (84 bytes): [0:16] encKey, [16:48] macKey, [48:52] nonce
// (big-endian uint32), [52:84] orderKey.
func deriveMainKeys(pass, salt []byte) mainKeys {
	derived := argon2.IDKey(pass, salt, 2, 64*1024, 4, 84)
	k := mainKeys{
		encKey: derived[0:16],
		macKey: derived[16:48],
		nonce:  binary.BigEndian.Uint32(derived[48:52]),
	}
	copy(k.orderKey[:], derived[52:84])
	return k
}

// prepareEncode writes a fresh random salt to the public salt pixels of m,
// derives the main keys from it, and returns them with the complete pixel
// order. prepareDecode is its mirror. Both the sequential and the parallel
// paths obtain their order here and nowhere else, so they cannot drift apart.
func prepareEncode(m draw.Image, pass []byte, bitsPerChannel, channels int) (mainKeys, []image.Point, error) {
	var salt [saltBytes]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return mainKeys{}, nil, err
	}
	return prepareOrder(m, pass, bitsPerChannel, channels, &salt, true)
}

// prepareDecode reads the salt from the public salt pixels of m, derives the
// main keys from it, and returns them with the complete pixel order.
func prepareDecode(m draw.Image, pass []byte, bitsPerChannel, channels int) (mainKeys, []image.Point, error) {
	var salt [saltBytes]byte
	return prepareOrder(m, pass, bitsPerChannel, channels, &salt, false)
}

// prepareOrder computes the public prefix of the order, writes salt to it (or
// reads salt from it) through a raw, unencrypted cursor, runs Argon2id on the
// salt, and finishes the order with the derived orderKey.
func prepareOrder(m draw.Image, pass []byte, bitsPerChannel, channels int,
	salt *[saltBytes]byte, write bool) (mainKeys, []image.Point, error) {
	if len(pass) == 0 {
		return mainKeys{}, nil, fmt.Errorf("password must not be empty")
	}
	bitsPerPixel := bitsPerChannel * channels
	if bitsPerPixel <= 0 {
		return mainKeys{}, nil, fmt.Errorf("steg: invalid encoding settings (%d bits/channel, %d channels)",
			bitsPerChannel, channels)
	}
	k := saltPixels(bitsPerPixel)
	b := m.Bounds()
	points, err := cursors.NewPixelOrder(b.Dx(), b.Dy(), k)
	if err != nil {
		return mainKeys{}, nil, fmt.Errorf("steg: image too small: %w", err)
	}

	// Stream bits 0–127 all live in points[:k], which is already final. The
	// cursor is flushed and dropped before points[k:] are shuffled under it.
	cur := cursors.NewRNGCursor(m, cursorOptions(points, bitsPerChannel, channels)...)
	rw := cursors.CursorAdapter(cur)
	if write {
		_, err = rw.Write(salt[:])
	} else {
		_, err = io.ReadFull(rw, salt[:])
	}
	cur.Flush()
	if err != nil {
		return mainKeys{}, nil, err
	}

	keys := deriveMainKeys(pass, salt[:])
	cursors.FinishPixelOrder(points, k, keys.orderKey)
	return keys, points, nil
}

// Overhead is the number of bytes every encoded image spends on framing:
// 16 (plaintext salt) + 4 (container length) + 4 (embedded real-length prefix)
// + 32 (HMAC-SHA256 tag).
const Overhead = 56

// CapacityBytes returns the maximum real payload size, in bytes, that m can
// hold with the given encoding settings. It returns 0 for images too small to
// hold the framing overhead.
//
// This is the single source of truth for capacity; callers must not reimplement
// the arithmetic, or they will drift from the encoder and produce payloads it
// rejects.
func CapacityBytes(m image.Image, bitsPerChannel, channels int) int {
	b := m.Bounds()
	return CapacityForDims(b.Dx(), b.Dy(), bitsPerChannel, channels)
}

// CapacityForDims is CapacityBytes for an image whose dimensions are known but
// which has not been decoded yet.
func CapacityForDims(w, h, bitsPerChannel, channels int) int {
	total := w * h * channels * bitsPerChannel / 8
	if total <= Overhead {
		return 0
	}
	return total - Overhead
}

// buildPaddedPayload prepends a 4-byte LE real-length prefix and appends random
// padding so the full image capacity is always written. This removes the
// payload-size signal from LSB statistics regardless of actual payload size.
// The returned slice is passed directly to container.WritePayload.
func buildPaddedPayload(m draw.Image, payload []byte, bitsPerChannel, channels int) ([]byte, error) {
	cap := CapacityBytes(m, bitsPerChannel, channels)
	if cap <= 0 {
		return nil, fmt.Errorf("steg: image too small to hold any payload")
	}
	if len(payload) > cap {
		return nil, fmt.Errorf("steg: payload too large (%d bytes, capacity %d bytes)", len(payload), cap)
	}
	// Layout: [4B real-length][real-payload][random padding] = 4 + cap bytes total.
	// Container then writes: [4B container-length][data][32B HMAC] = imageTotal bytes.
	out := make([]byte, 4+cap)
	binary.LittleEndian.PutUint32(out[:4], uint32(len(payload)))
	copy(out[4:], payload)
	if _, err := rand.Read(out[4+len(payload):]); err != nil {
		return nil, err
	}
	return out, nil
}

// extractRealPayload recovers the original payload from the padded buffer returned
// by container.ReadPayload. The first 4 bytes are the LE-encoded real length.
func extractRealPayload(padded []byte) ([]byte, error) {
	if len(padded) < 4 {
		return nil, fmt.Errorf("steg: padded payload too short")
	}
	realLen := binary.LittleEndian.Uint32(padded[:4])
	if int(realLen) > len(padded)-4 {
		return nil, fmt.Errorf("steg: corrupt payload: real length %d exceeds data", realLen)
	}
	return padded[4 : 4+realLen], nil
}
