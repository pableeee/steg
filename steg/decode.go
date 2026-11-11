package steg

import (
	"crypto/hmac"
	"crypto/sha256"
	"image/draw"
	"io"

	"github.com/pableeee/steg/cipher"
	"github.com/pableeee/steg/cursors"
	"github.com/pableeee/steg/steg/container"
)

func Decode(m draw.Image, pass []byte, bitsPerChannel, channels int) ([]byte, error) {
	// Read the plaintext salt from stream bits 0–127, derive the keys from it,
	// and get the full pixel order.
	keys, points, err := prepareDecode(m, pass, bitsPerChannel, channels)
	if err != nil {
		return nil, err
	}

	cur := cursors.NewRNGCursor(m, cursorOptions(points, bitsPerChannel, channels)...)
	payloadCipher, err := cipher.NewCipher(keys.nonce, keys.encKey)
	if err != nil {
		return nil, err
	}
	payloadCM := cursors.CipherMiddleware(cur, payloadCipher)
	if _, err = payloadCM.Seek(saltBytes*8, io.SeekStart); err != nil {
		return nil, err
	}

	// The padded block is exactly 4 bytes (real-length prefix) plus the image's
	// usable capacity; anything longer cannot have been written by Encode.
	maxPadded := CapacityBytes(m, bitsPerChannel, channels) + 4

	adapter := cursors.CursorAdapter(payloadCM)
	mac := hmac.New(sha256.New, keys.macKey)
	padded, err := container.ReadPayload(adapter, mac, maxPadded)
	if err != nil {
		return nil, err
	}
	return extractRealPayload(padded)
}
