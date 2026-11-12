package steg

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	"io"
	"math"

	"github.com/pableeee/steg/cursors"
	"golang.org/x/crypto/argon2"
)

// validateCarrier checks everything about the carrier and the settings that
// Encode, Decode, EncodeParallel and DecodeParallel need before they touch a
// pixel: the image type (see Encode) and validateSettings on its dimensions.
func validateCarrier(m image.Image, bitsPerChannel, channels int) error {
	if err := cursors.CheckImage(m); err != nil {
		return fmt.Errorf("steg: %w", err)
	}
	b := m.Bounds()
	return validateSettings(b.Dx(), b.Dy(), bitsPerChannel, channels)
}

// validateSettings checks the encoding settings and the carrier dimensions:
// bitsPerChannel in 1..8, channels in 1..3, positive dimensions, at most
// cursors.MaxPixels pixels (the order indexes pixels with uint32), and a
// capacity the format can describe (the container length field is a uint32).
// The pixel limit keeps the capacity in bits below 2^32 × 24, far inside int64.
func validateSettings(w, h, bitsPerChannel, channels int) error {
	if bitsPerChannel < 1 || bitsPerChannel > 8 {
		return fmt.Errorf("steg: bits per channel must be between 1 and 8, got %d", bitsPerChannel)
	}
	if channels < 1 || channels > 3 {
		return fmt.Errorf("steg: channels must be between 1 and 3 (1=R, 2=R+G, 3=R+G+B), got %d", channels)
	}
	if w <= 0 || h <= 0 {
		return fmt.Errorf("steg: invalid image dimensions %dx%d", w, h)
	}
	n, err := cursors.PixelCount(w, h)
	if err != nil {
		return fmt.Errorf("steg: image %dx%d is too large: at most %d pixels are supported",
			w, h, int64(cursors.MaxPixels))
	}
	totalBytes := int64(n) * int64(channels*bitsPerChannel) / 8
	// The container length field covers the 4-byte real-length prefix plus the
	// capacity: totalBytes - Overhead + 4.
	if totalBytes-Overhead+4 > math.MaxUint32 || totalBytes > math.MaxInt {
		return fmt.Errorf("steg: image %dx%d at %d channel(s) × %d bit(s) holds %d bytes, "+
			"more than the format's 4 GiB limit; use fewer channels or bits per channel",
			w, h, channels, bitsPerChannel, totalBytes)
	}
	return nil
}

// cursorOptions builds the RNGCursor option slice for the given configuration.
// channels: 1 = R only, 2 = R+G, 3 = R+G+B. order is the traversal order from
// prepareEncode/prepareDecode; it is shared, never copied or modified.
//
// write selects LSB matching (±1 embedding) for cursors that write: every
// encode path passes true, every decode path false. Matching only changes how
// a mismatching bit is written (the byte moves ±1 at random instead of to its
// pair partner); bit 0 ends up the same, so decoding is unaffected. It applies
// at 1 bit per channel only; with more bits the cursor uses plain replacement.
func cursorOptions(order []uint32, bitsPerChannel, channels int, write bool) []cursors.Option {
	opts := []cursors.Option{
		cursors.WithSharedPoints(order),
		cursors.WithBitsPerChannel(bitsPerChannel),
	}
	if write {
		opts = append(opts, cursors.WithLSBMatching())
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
func prepareEncode(m draw.Image, pass []byte, bitsPerChannel, channels int) (mainKeys, []uint32, error) {
	var salt [saltBytes]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return mainKeys{}, nil, err
	}
	return prepareOrder(m, pass, bitsPerChannel, channels, &salt, true)
}

// prepareDecode reads the salt from the public salt pixels of m, derives the
// main keys from it, and returns them with the complete pixel order.
func prepareDecode(m draw.Image, pass []byte, bitsPerChannel, channels int) (mainKeys, []uint32, error) {
	var salt [saltBytes]byte
	return prepareOrder(m, pass, bitsPerChannel, channels, &salt, false)
}

// prepareOrder computes the public prefix of the order, writes salt to it (or
// reads salt from it) through a raw, unencrypted cursor, runs Argon2id on the
// salt, and finishes the order with the derived orderKey. The caller must have
// run validateCarrier.
func prepareOrder(m draw.Image, pass []byte, bitsPerChannel, channels int,
	salt *[saltBytes]byte, write bool) (mainKeys, []uint32, error) {
	if len(pass) == 0 {
		return mainKeys{}, nil, fmt.Errorf("password must not be empty")
	}
	k := saltPixels(bitsPerChannel * channels)
	b := m.Bounds()
	order, err := cursors.NewPixelOrder(b.Dx(), b.Dy(), k)
	if err != nil {
		return mainKeys{}, nil, fmt.Errorf("steg: image too small: %w", err)
	}

	// Stream bits 0–127 all live in order[:k], which is already final. The
	// cursor is dropped before order[k:] are shuffled under it.
	cur, err := cursors.NewRNGCursor(m, cursorOptions(order, bitsPerChannel, channels, write)...)
	if err != nil {
		return mainKeys{}, nil, err
	}
	rw := cursors.CursorAdapter(cur)
	if write {
		_, err = rw.Write(salt[:])
	} else {
		_, err = io.ReadFull(rw, salt[:])
	}
	if err != nil {
		return mainKeys{}, nil, err
	}

	keys := deriveMainKeys(pass, salt[:])
	cursors.FinishPixelOrder(order, k, keys.orderKey)
	return keys, order, nil
}

// Overhead is the number of bytes every encoded image spends on framing:
// 16 (plaintext salt) + 4 (container length) + 4 (embedded real-length prefix)
// + 32 (HMAC-SHA256 tag).
const Overhead = 56

// CapacityBytes returns the maximum real payload size, in bytes, that m can
// hold with the given encoding settings. It returns 0 for images too small to
// hold the framing overhead and for settings or dimensions Encode rejects
// (bitsPerChannel outside 1..8, channels outside 1..3, too many pixels). It
// looks only at m's bounds, not its type.
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
	if validateSettings(w, h, bitsPerChannel, channels) != nil {
		return 0
	}
	// validateSettings guarantees this fits an int.
	total := int(int64(w) * int64(h) * int64(channels*bitsPerChannel) / 8)
	if total <= Overhead {
		return 0
	}
	return total - Overhead
}

// MinPaddedBlock is the smallest padded block the encoder writes when the
// carrier can hold more: every payload of up to MinPaddedBlock-4 bytes looks
// the same.
//
// It is small on purpose. Detectability is driven by the embedding rate, and
// a floor is paid by every short message: measured with cmd/stegbench on
// BOSSbase (512×512, one channel, SPAM + FLD ensemble), a 4096-byte floor
// (0.127 bpp) is detected with P_E ≈ 0.15, 256 bytes with 0.39 and 64 bytes
// with 0.45. Below 64 the 52 bytes of salt, length and tag dominate the stream
// and a smaller floor buys almost nothing (0.46 at 32). The price is that
// payload sizes are distinguishable in finer steps below 4 KiB.
const MinPaddedBlock = 64

// paddedSize returns the length of the padded block (the 4-byte real-length
// prefix, the payload and the random padding) that the encoder writes for a
// realLen-byte payload into a carrier of the given capacity (CapacityBytes).
//
// The block is rounded up to a power-of-two bucket of at least MinPaddedBlock
// bytes: max(MinPaddedBlock, nextPowerOfTwo(4 + realLen)). If that bucket does
// not fit the carrier, the block is capacity + 4 bytes, i.e. the carrier is
// filled completely, as for payloads near capacity or carriers whose
// capacity + 4 is below MinPaddedBlock. Only the stream prefix up to the end
// of the tag is written; every pixel past it keeps its original value.
//
// The bucket leaks only the order of magnitude of the payload size, through the
// (encrypted) container length and through the embedding rate a steganalyst
// could estimate. Above the floor it costs at most twice the bits an unpadded
// payload would change; payloads of up to MinPaddedBlock-4 bytes all write
// MinPaddedBlock. The caller must have checked realLen <= capacity.
func paddedSize(realLen, capacity int) int {
	full := int64(capacity) + 4
	need := int64(realLen) + 4
	bucket := int64(MinPaddedBlock)
	for bucket < need && bucket < full {
		bucket <<= 1
	}
	if bucket > full {
		return int(full)
	}
	return int(bucket)
}

// buildPaddedPayload prepends a 4-byte LE real-length prefix and appends random
// padding up to paddedSize bytes, so the payload size is hidden within its
// power-of-two bucket. The returned slice is passed directly to
// container.WritePayload.
func buildPaddedPayload(m draw.Image, payload []byte, bitsPerChannel, channels int) ([]byte, error) {
	cap := CapacityBytes(m, bitsPerChannel, channels)
	if cap <= 0 {
		return nil, fmt.Errorf("steg: image too small to hold any payload")
	}
	if len(payload) > cap {
		return nil, fmt.Errorf("steg: payload too large (%d bytes, capacity %d bytes)", len(payload), cap)
	}
	// Layout: [4B real-length][real-payload][random padding] = paddedSize bytes,
	// at most 4 + cap. Container then writes:
	// [4B container-length][data][32B HMAC], at most imageTotal - 16 bytes.
	out := make([]byte, paddedSize(len(payload), cap))
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
