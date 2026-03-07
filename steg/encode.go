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
	seed, encKey, macKey, nonce, err := deriveKeys(pass)
	if err != nil {
		return err
	}

	// Buffer the full payload to build the padded data block. Padding fills the
	// image to capacity on every encode, removing the payload-size signal from
	// LSB statistics.
	realPayload, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	padded, err := buildPaddedPayload(m, realPayload, bitsPerChannel, channels)
	if err != nil {
		return err
	}

	cur := cursors.NewRNGCursor(m, cursorOptions(seed, bitsPerChannel, channels)...)

	// Nonce is derived from the KDF output — no plaintext bytes are written to
	// the image before the cipher starts.
	c, err := cipher.NewCipher(nonce, encKey)
	if err != nil {
		return err
	}

	cm := cursors.CipherMiddleware(cur, c)
	adapter := cursors.CursorAdapter(cm)
	mac := hmac.New(sha256.New, macKey)
	if err = container.WritePayload(adapter, bytes.NewReader(padded), mac); err != nil {
		return err
	}
	cur.Flush()
	return nil
}
