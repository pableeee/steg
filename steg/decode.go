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
	seed, err := deriveSeed(pass)
	if err != nil {
		return nil, err
	}

	cur := cursors.NewRNGCursor(m, cursorOptions(seed, bitsPerChannel, channels)...)

	// Read the 16-byte random salt from image bytes 0–15 (stored in plaintext).
	saltAdapter := cursors.CursorAdapter(cur)
	var randomSalt [16]byte
	if _, err = io.ReadFull(saltAdapter, randomSalt[:]); err != nil {
		return nil, err
	}

	// Derive main keys from the recovered salt; seek cursor and cipher to bit 128.
	encKey, macKey, payloadNonce, err := deriveMainKeys(pass, randomSalt[:])
	if err != nil {
		return nil, err
	}
	payloadCipher, err := cipher.NewCipher(payloadNonce, encKey)
	if err != nil {
		return nil, err
	}
	payloadCM := cursors.CipherMiddleware(cur, payloadCipher)
	if _, err = payloadCM.Seek(128, io.SeekStart); err != nil {
		return nil, err
	}

	adapter := cursors.CursorAdapter(payloadCM)
	mac := hmac.New(sha256.New, macKey)
	padded, err := container.ReadPayload(adapter, mac)
	if err != nil {
		return nil, err
	}
	return extractRealPayload(padded)
}
