package cursors_test

import (
	"bytes"
	"image"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	// Adjust import paths according to your project structure.
	"github.com/pableeee/steg/cursors"
)

// Mock data and helper functions
func writeAndReadAll(t *testing.T, rw io.ReadWriteSeeker, payload []byte) []byte {
	_, err := rw.Write(payload)
	require.NoError(t, err)

	_, err = rw.Seek(0, io.SeekStart)
	require.NoError(t, err)

	readBuf := make([]byte, len(payload))
	_, err = rw.Read(readBuf)
	require.NoError(t, err)

	return readBuf
}

func capacityInBits(width, height int, totalBitsPerPixel int) int {
	return width * height * totalBitsPerPixel
}

func TestRNGCursor(t *testing.T) {
	t.Run("1. Basic Read/Write Consistency", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))
		// Use R and G bits => 2 bits per pixel
		cur := cursors.NewRNGCursor(img,
			cursors.UseGreenBit(),
		)

		adapter := cursors.CursorAdapter(cur)

		// Capacity = 100 pixels * 2 bits = 200 bits = 25 bytes max
		payload := []byte("hello steganography")
		require.LessOrEqual(t, len(payload), 25, "Payload fits in capacity")

		readBack := writeAndReadAll(t, adapter, payload)
		assert.Equal(t, payload, readBack)
	})

	t.Run("2. Deterministic Behavior With Given Seed", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 5, 5))
		// 1 bit per pixel for simplicity
		cur1 := cursors.NewRNGCursor(img,
			cursors.WithSeed(42),
		)

		cur2 := cursors.NewRNGCursor(img,
			cursors.WithSeed(42),
		)

		payload := []byte("abc")

		// Write with cur1
		adapter1 := cursors.CursorAdapter(cur1)
		_, err := adapter1.Write(payload)
		require.NoError(t, err)

		// Read with cur2 (should produce the same pixel sequence)
		adapter2 := cursors.CursorAdapter(cur2)
		readBuf := make([]byte, len(payload))
		_, err = adapter2.Read(readBuf)
		require.NoError(t, err)

		assert.Equal(t, payload, readBuf, "Given the same seed and setup, order should be deterministic")
	})

	t.Run("3. Boundary Conditions", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 2, 2))
		// 1 bit per pixel => capacity = 4 bits = 0.5 byte
		cur := cursors.NewRNGCursor(img)

		adapter := cursors.CursorAdapter(cur)

		// Try writing 1 byte = 8 bits. Only 4 bits available, should fail halfway
		payload := []byte{0xFF}
		n, err := adapter.Write(payload)
		assert.Error(t, err, "Writing beyond capacity should return an error")
		assert.Less(t, n, len(payload))
	})

	t.Run("4. Multiple Channel Configurations", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 3, 3))
		// Test using R and B with multiple bits in B
		cur := cursors.NewRNGCursor(img,
			cursors.UseGreenBit(),
			cursors.UseBlueBit(),
		)

		adapter := cursors.CursorAdapter(cur)
		// capacity = 9 pixels * 3 bits = 9 bits = 3.375 bytes, so 3 bytes safely
		payload := []byte{0xAA, 0xBB, 0xCC}
		readBack := writeAndReadAll(t, adapter, payload)
		assert.Equal(t, payload, readBack)
	})

	t.Run("5. Stability with Different Image Sizes", func(t *testing.T) {
		// Small image
		smallImg := image.NewRGBA(image.Rect(0, 0, 1, 1))
		curSmall := cursors.NewRNGCursor(smallImg)

		adapterSmall := cursors.CursorAdapter(curSmall)
		payload := []byte{0x0F} // 8 bits, but we only have 1 bit capacity, should fail
		_, err := adapterSmall.Write(payload)
		assert.Error(t, err, "Small image should fail when writing more bits than available")

		// Larger image
		largeImg := image.NewRGBA(image.Rect(0, 0, 50, 50))
		curLarge := cursors.NewRNGCursor(largeImg,
			cursors.UseGreenBit(),
		)
		adapterLarge := cursors.CursorAdapter(curLarge)

		// capacity = 2500 pixels * 2 bits = 5000 bits ~ 625 bytes
		payloadLarge := bytes.Repeat([]byte{0xAB}, 300) // 300 bytes fits in 625 bytes capacity
		readBack := writeAndReadAll(t, adapterLarge, payloadLarge)
		assert.Equal(t, payloadLarge, readBack)
	})

	// t.Run("6. Behavior Under Different Bit Depths", func(t *testing.T) {
	// 	// Suppose we have an image with 16-bit depth (like RGBA64).
	// 	// If your code supports it, you might need a NRGBA64 image:
	// 	img := image.NewNRGBA64(image.Rect(0, 0, 10, 10))
	// 	// Let's assume that RNGCursor can handle bitDepth as an option, like WithBitDepth(16)
	// 	cur := cursors.NewRNGCursor(img,
	// 		cursors.UseGreenBit(),
	// 		cursors.WithSeed(123),
	// 	)

	// 	adapter := cursors.CursorAdapter(cur)

	// 	payload := []byte("16bit-depth-test")
	// 	readBack := writeAndReadAll(t, adapter, payload)
	// 	assert.Equal(t, payload, readBack, "Should correctly handle reading/writing with 16-bit depth images")
	// })
}
