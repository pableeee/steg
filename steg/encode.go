package steg

import (
	"crypto/md5"
	"image/draw"
	"io"

	"github.com/pableeee/steg/cipher"
	"github.com/pableeee/steg/cursors"
	"github.com/pableeee/steg/steg/container"
)

// Encode embeds the data from the provided reader into the given image using the provided passphrase.
// It:
// 1. Derives a seed from the hash of the password.
// 2. Creates a pseudo-random cursor over the image (RNGCursor).
// 3. Wraps the cursor with a cipher middleware if a pass is provided.
// 4. Uses container.WritePayload to write length, payload, and checksum.
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
