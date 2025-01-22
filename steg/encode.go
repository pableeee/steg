package steg

import (
	"crypto/md5"
	"image/draw"
	"io"

	"github.com/pableeee/steg/cipher"
	"github.com/pableeee/steg/cursors"
	"github.com/pableeee/steg/steg/container"
)

func Encode(m draw.Image, pass []byte, r io.Reader) error {
	// Derive a seed from the password
	seedVal, err := deriveSeedFromPassword(pass)
	if err != nil {
		return err
	}

	// Create RNG cursor with options
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

	// Use container to write payload: length + payload + checksum
	return container.WritePayload(adapter, r, md5.New())
}
