package steg

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"image/draw"
	"io"

	"github.com/pableeee/steg/cipher"
	"github.com/pableeee/steg/cursors"
	"github.com/pableeee/steg/steg/container"
)

func Encode(m draw.Image, pass []byte, r io.Reader) error {
	// Read the entire payload into memory to determine its size for capacity validation
	payloadData, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("failed to read payload: %w", err)
	}

	// Derive a seed from the password
	seedVal, err := DeriveSeedFromPassword(pass)
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

	// Validate capacity before encoding
	hashFn := md5.New()
	requiredBytes := container.CalculateRequiredCapacity(int64(len(payloadData)), hashFn.Size())
	availableBits := cur.Capacity()
	availableBytes := availableBits / 8

	if requiredBytes > availableBytes {
		return &ErrInsufficientCapacity{
			RequiredBytes:  requiredBytes,
			AvailableBytes: availableBytes,
		}
	}

	// Generate random nonce for encryption (improves security vs fixed nonce=0)
	// Note: For now, we use nonce=0 to maintain format compatibility
	// TODO: Implement proper nonce storage in container format for full security
	nonce := uint32(0) // Maintain old format for backward compatibility

	// Create cipher middleware with nonce (currently 0 for compatibility)
	cm := cursors.CipherMiddleware(cur, cipher.NewCipher(nonce, pass))

	// CursorAdapter transforms a Cursor into an io.ReadWriteSeeker
	adapter := cursors.CursorAdapter(cm)

	// Use container to write payload: length + payload + checksum (old format)
	return container.WritePayload(adapter, bytes.NewReader(payloadData), hashFn)
}
