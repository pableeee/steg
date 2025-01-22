package steg

import (
	"crypto/md5"
	"image/draw"

	"github.com/pableeee/steg/cipher"
	"github.com/pableeee/steg/cursors"
	"github.com/pableeee/steg/steg/container"
)

func Decode(m draw.Image, pass []byte) ([]byte, error) {
	// Derive a seed from the password
	seedVal, err := deriveSeedFromPassword(pass)
	if err != nil {
		return nil, err
	}

	// Create RNG cursor
	cur := cursors.NewRNGCursor(
		m,
		cursors.UseGreenBit(),
		cursors.UseBlueBit(),
		cursors.WithSeed(seedVal),
	)

	// Create cipher middleware
	cm := cursors.CipherMiddleware(cur, cipher.NewCipher(0, pass))

	// CursorAdapter transforms a Cursor into an io.ReadWriteSeeker
	adapter := cursors.CursorAdapter(cm)

	// Use container to read payload: length + payload + checksum verification
	return container.ReadPayload(adapter, md5.New())
}
