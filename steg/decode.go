package steg

import (
	"crypto/hmac"
	"crypto/sha256"
	"image/draw"

	"github.com/pableeee/steg/cipher"
	"github.com/pableeee/steg/cursors"
	"github.com/pableeee/steg/steg/container"
)

func Decode(m draw.Image, pass []byte, bitsPerChannel, channels int) ([]byte, error) {
	seed, encKey, macKey, nonce, err := deriveKeys(pass)
	if err != nil {
		return nil, err
	}

	cur := cursors.NewRNGCursor(m, cursorOptions(seed, bitsPerChannel, channels)...)

	c, err := cipher.NewCipher(nonce, encKey)
	if err != nil {
		return nil, err
	}

	cm := cursors.CipherMiddleware(cur, c)
	adapter := cursors.CursorAdapter(cm)
	mac := hmac.New(sha256.New, macKey)
	padded, err := container.ReadPayload(adapter, mac)
	if err != nil {
		return nil, err
	}
	return extractRealPayload(padded)
}
