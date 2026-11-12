package steg

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"image/draw"
	"io"

	"github.com/pableeee/steg/cipher"
	"github.com/pableeee/steg/cursors"
	"github.com/pableeee/steg/steg/container"
)

// Encode hides the contents of r in m, keyed by pass, using the lowest
// bitsPerChannel bits (1..8) of the first channels channels (1 = R, 2 = R+G,
// 3 = R+G+B) of the pixels it writes. The payload may be up to CapacityBytes long. It is
// padded with random bytes to a power-of-two bucket of at least MinPaddedBlock bytes (see
// paddedSize), or to the whole capacity when the bucket does not fit; only
// the pixels that stream prefix maps to are written, the rest of m is left
// untouched.
//
// m must be an *image.RGBA or *image.NRGBA: their 8-bit channels are modified
// in place and round-trip exactly, at any alpha. Any other type is rejected
// before a pixel is modified; convert it first, for example:
//
//	rgba := image.NewRGBA(src.Bounds())
//	draw.Draw(rgba, rgba.Bounds(), src, src.Bounds().Min, draw.Src)
//
// Invalid settings are rejected the same way. Encode, EncodeParallel, Decode
// and DecodeParallel all produce and read the same format.
func Encode(m draw.Image, pass []byte, r io.Reader, bitsPerChannel, channels int) error {
	if err := validateCarrier(m, bitsPerChannel, channels); err != nil {
		return err
	}
	realPayload, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	padded, err := buildPaddedPayload(m, realPayload, bitsPerChannel, channels)
	if err != nil {
		return err
	}

	// Write a fresh plaintext salt to stream bits 0–127, derive the keys from it,
	// and get the full pixel order.
	keys, points, err := prepareEncode(m, pass, bitsPerChannel, channels)
	if err != nil {
		return err
	}

	cur, err := cursors.NewRNGCursor(m, cursorOptions(points, bitsPerChannel, channels, true)...)
	if err != nil {
		return err
	}
	payloadCipher, err := cipher.NewCipher(keys.nonce, keys.encKey)
	if err != nil {
		return err
	}
	payloadCM := cursors.CipherMiddleware(cur, payloadCipher)
	if _, err = payloadCM.Seek(saltBytes*8, io.SeekStart); err != nil {
		return err
	}

	adapter := cursors.CursorAdapter(payloadCM)
	mac := hmac.New(sha256.New, keys.macKey)
	return container.WritePayload(adapter, bytes.NewReader(padded), mac)
}
