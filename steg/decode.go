package steg

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"image/draw"
	"io"

	"github.com/pableeee/steg/cipher"
	"github.com/pableeee/steg/cursors"
	"github.com/pableeee/steg/steg/container"
)

func Decode(m draw.Image, pass []byte) ([]byte, error) {
	seed, encKey, macKey, err := deriveKeys(pass)
	if err != nil {
		return nil, err
	}

	cur := cursors.NewRNGCursor(
		m,
		cursors.UseGreenBit(),
		cursors.UseBlueBit(),
		cursors.WithSeed(seed),
	)

	// Read the same 4 nonce bytes from the same pixel positions used during encode.
	rawAdapter := cursors.CursorAdapter(cur)
	nonceBuf := make([]byte, 4)
	if _, err = io.ReadFull(rawAdapter, nonceBuf); err != nil {
		return nil, err
	}
	nonce := binary.BigEndian.Uint32(nonceBuf)

	c, err := cipher.NewCipher(nonce, encKey)
	if err != nil {
		return nil, err
	}

	cm := cursors.CipherMiddleware(cur, c)
	if _, err = cm.Seek(32, io.SeekStart); err != nil {
		return nil, err
	}

	adapter := cursors.CursorAdapter(cm)
	mac := hmac.New(sha256.New, macKey)
	return container.ReadPayload(adapter, mac)
}
