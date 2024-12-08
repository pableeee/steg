package steg

import (
	"crypto/md5"
	"image/draw"

	"github.com/pableeee/steg/cipher"
	"github.com/pableeee/steg/cursors"
	"github.com/pableeee/steg/steg/container"
)

// Decode extracts the embedded data from the given image using the provided passphrase.
// It:
// 1. Derives a seed from the hash of the password.
// 2. Creates a pseudo-random cursor over the image (RNGCursor).
// 3. Wraps the cursor with a cipher middleware if a pass is provided.
// 4. Uses container.ReadPayload to retrieve the payload and validate checksum.
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
