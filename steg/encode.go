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

func Encode(m draw.Image, pass []byte, r io.Reader, bitsPerChannel, channels int) error {
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

	cur := cursors.NewRNGCursor(m, cursorOptions(points, bitsPerChannel, channels)...)
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
	if err = container.WritePayload(adapter, bytes.NewReader(padded), mac); err != nil {
		return err
	}
	cur.Flush()
	return nil
}
