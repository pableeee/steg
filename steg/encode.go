package steg

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"image/draw"
	"io"

	"github.com/pableeee/steg/cipher"
	"github.com/pableeee/steg/cursors"
	"github.com/pableeee/steg/steg/container"
)

func Encode(m draw.Image, pass []byte, r io.Reader) error {
	seed, encKey, macKey, err := deriveKeys(pass)
	if err != nil {
		return err
	}

	cur := cursors.NewRNGCursor(
		m,
		cursors.UseGreenBit(),
		cursors.UseBlueBit(),
		cursors.WithSeed(seed),
	)

	// Generate a cryptographically random nonce and write it as the first 4
	// bytes (32 bits) of the pixel sequence in plaintext. Decode reads these
	// same bits to reconstruct the nonce, so each encode with the same carrier
	// and password produces a unique keystream.
	nonceBuf := make([]byte, 4)
	if _, err = rand.Read(nonceBuf); err != nil {
		return err
	}
	nonce := binary.BigEndian.Uint32(nonceBuf)

	rawAdapter := cursors.CursorAdapter(cur)
	if _, err = rawAdapter.Write(nonceBuf); err != nil {
		return err
	}

	c, err := cipher.NewCipher(nonce, encKey)
	if err != nil {
		return err
	}

	// Wrap the same cursor in cipher middleware and sync cipher counter to bit 32.
	cm := cursors.CipherMiddleware(cur, c)
	if _, err = cm.Seek(32, io.SeekStart); err != nil {
		return err
	}

	adapter := cursors.CursorAdapter(cm)
	mac := hmac.New(sha256.New, macKey)
	return container.WritePayload(adapter, r, mac)
}
