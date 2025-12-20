package cursors

import (
	"fmt"
	"io"
)

func CursorAdapter(c Cursor) io.ReadWriteSeeker {
	return &ReadWriteSeekerAdapter{cur: c}
}

// ReadWriteSeekerAdapter is a concrete type that adapts a Cursor to io.ReadWriteSeeker
type ReadWriteSeekerAdapter struct {
	cur Cursor
}

func byteToBits(b byte) []int {
	var bits []int
	for i := 7; i >= 0; i-- { // Extract bits from most significant to least significant
		bit := (b >> i) & 1
		bits = append(bits, int(bit))
	}

	return bits
}

func (r *ReadWriteSeekerAdapter) Seek(offset int64, whence int) (int64, error) {
	// Seek sets the offset for the next Read or Write to offset,
	// interpreted according to whence:
	// [SeekStart] means relative to the start of the file,
	// [SeekCurrent] means relative to the current offset, and
	// [SeekEnd] means relative to the end
	return r.cur.Seek(offset*8, whence)
}

func (r *ReadWriteSeekerAdapter) Read(payload []byte) (n int, err error) {
	p := 0
	for ; p < len(payload); p++ {
		var nBits = 8
		var res uint8
		for i := 0; i < nBits; i++ {
			bit, err := r.cur.ReadBit()
			if err != nil {
				return p, err
			}

			res |= uint8(bit << (nBits - i - 1))
		}
		payload[p] = res
	}

	return p, nil
}
func (r *ReadWriteSeekerAdapter) Write(payload []byte) (n int, err error) {
	return r.WriteWithConfig(payload, ParallelWriteConfig{Enabled: false})
}

// WriteWithConfig writes payload with optional parallel processing
// Note: Parallel writing requires unwrapping cipherMiddleware to access the underlying RNGCursor.
// If cipherMiddleware is present, bits are pre-encrypted sequentially, then written in parallel.
func (r *ReadWriteSeekerAdapter) WriteWithConfig(payload []byte, config ParallelWriteConfig) (n int, err error) {
	// Convert bytes to bits
	allBits := make([]uint8, 0, len(payload)*8)
	for _, bite := range payload {
		bits := byteToBits(bite)
		for _, b := range bits {
			allBits = append(allBits, uint8(b))
		}
	}

	// Check if we can use parallel writing
	if config.Enabled {
		underlyingCursor, cipherBlock := GetUnderlyingCursor(r.cur)

		// If we have cipher middleware, pre-encrypt all bits sequentially
		var bitsToWrite []uint8
		if cipherBlock != nil {
			// Pre-encrypt bits (must be done sequentially due to cipher state)
			bitsToWrite = make([]uint8, len(allBits))
			// Reset cipher to start position (0 = io.SeekStart)
			_, err := cipherBlock.Seek(0, 0)
			if err != nil {
				return 0, fmt.Errorf("failed to reset cipher: %w", err)
			}
			for i, bit := range allBits {
				encrypted, err := cipherBlock.EncryptBit(bit)
				if err != nil {
					return 0, fmt.Errorf("failed to encrypt bit at position %d: %w", i, err)
				}
				bitsToWrite[i] = encrypted
			}
		} else {
			bitsToWrite = allBits
		}

		// Now write (encrypted) bits in parallel using underlying cursor
		if rngCur, ok := underlyingCursor.(*RNGCursor); ok {
			if err := rngCur.WritePixelsParallel(bitsToWrite, config); err != nil {
				return 0, err
			}
			return len(payload), nil
		}
		// Fall through to sequential if parallel not possible
	}

	// Fallback to sequential writing
	for i, bite := range payload {
		bits := byteToBits(bite)
		for _, b := range bits {
			_, err = r.cur.WriteBit(uint8(b))
			if err != nil {
				return i, err
			}
		}
	}

	return len(payload), nil
}

// WriteParallel writes payload using parallel pixel processing
// Note: This only works with RNGCursor directly, not through CipherMiddleware.
// For encrypted writes, encryption must be handled separately.
func (r *ReadWriteSeekerAdapter) WriteParallel(payload []byte, config ParallelWriteConfig) (n int, err error) {
	config.Enabled = true
	return r.WriteWithConfig(payload, config)
}
