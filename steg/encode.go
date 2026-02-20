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

func Encode(m draw.Image, pass []byte, r io.Reader) error {
	seed, aesKey, err := deriveKeys(pass)
	if err != nil {
		return err
	}

	cur := cursors.NewRNGCursor(
		m,
		cursors.UseGreenBit(),
		cursors.UseBlueBit(),
		cursors.WithSeed(seed),
	)

	// Read 4 bytes from the carrier image pixel LSBs in plaintext.
	// These become the per-image nonce. The cursor advances to bit 32.
	rawAdapter := cursors.CursorAdapter(cur)
	nonceBuf := make([]byte, 4)
	if _, err = io.ReadFull(rawAdapter, nonceBuf); err != nil {
		return err
	}
	nonce := binary.BigEndian.Uint32(nonceBuf)

	c, err := cipher.NewCipher(nonce, aesKey)
	if err != nil {
		return err
	}

	// Wrap the same cursor in cipher middleware and sync cipher counter to bit 32.
	cm := cursors.CipherMiddleware(cur, c)
	if _, err = cm.Seek(32, io.SeekStart); err != nil {
		return err
	}

	adapter := cursors.CursorAdapter(cm)
	mac := hmac.New(sha256.New, aesKey)
	return container.WritePayload(adapter, r, mac)
}
