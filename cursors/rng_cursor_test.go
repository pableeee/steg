package cursors_test

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
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

func mustCursor(t *testing.T, img *image.RGBA, opts ...cursors.Option) *cursors.RNGCursor {
	t.Helper()
	c, err := cursors.NewRNGCursor(img, opts...)
	require.NoError(t, err)
	return c
}

func capacityInBits(width, height int, totalBitsPerPixel int) int {
	return width * height * totalBitsPerPixel
}

func TestRNGCursor(t *testing.T) {
	t.Run("1. Basic Read/Write Consistency", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))
		// Use R and G bits => 2 bits per pixel
		cur := mustCursor(t, img,
			cursors.UseGreenBit(),
		)

		adapter := cursors.CursorAdapter(cur)

		// Capacity = 100 pixels * 2 bits = 200 bits = 25 bytes max
		payload := []byte("hello steganography")
		require.LessOrEqual(t, len(payload), 25, "Payload fits in capacity")

		readBack := writeAndReadAll(t, adapter, payload)
		assert.Equal(t, payload, readBack)
	})

	t.Run("2. Deterministic Behavior With Given Order", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 5, 5))
		// 1 bit per pixel for simplicity. Each cursor builds its own copy of
		// the order from the same key, so agreement means the order is
		// deterministic, not merely shared.
		order := func() []uint32 {
			points, err := cursors.NewPixelOrder(5, 5, 3)
			require.NoError(t, err)
			cursors.FinishPixelOrder(points, 3, [32]byte{42})
			return points
		}
		cur1 := mustCursor(t, img,
			cursors.WithSharedPoints(order()),
		)

		cur2 := mustCursor(t, img,
			cursors.WithSharedPoints(order()),
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
		cur := mustCursor(t, img)

		adapter := cursors.CursorAdapter(cur)

		// Try writing 1 byte = 8 bits. Only 4 bits available, should fail halfway
		payload := []byte{0xFF}
		n, err := adapter.Write(payload)
		assert.ErrorIs(t, err, io.EOF)
		assert.Less(t, n, len(payload))
	})

	t.Run("4. Multiple Channel Configurations", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 3, 3))
		// Test using R and B with multiple bits in B
		cur := mustCursor(t, img,
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
		curSmall := mustCursor(t, smallImg)

		adapterSmall := cursors.CursorAdapter(curSmall)
		payload := []byte{0x0F} // 8 bits, but we only have 1 bit capacity, should fail
		_, err := adapterSmall.Write(payload)
		assert.Error(t, err, "Small image should fail when writing more bits than available")

		// Larger image
		largeImg := image.NewRGBA(image.Rect(0, 0, 50, 50))
		curLarge := mustCursor(t, largeImg,
			cursors.UseGreenBit(),
		)
		adapterLarge := cursors.CursorAdapter(curLarge)

		// capacity = 2500 pixels * 2 bits = 5000 bits ~ 625 bytes
		payloadLarge := bytes.Repeat([]byte{0xAB}, 300) // 300 bytes fits in 625 bytes capacity
		readBack := writeAndReadAll(t, adapterLarge, payloadLarge)
		assert.Equal(t, payloadLarge, readBack)
	})
}

// TestRNGCursorRejectsUnsupported checks the constructor refuses carriers it
// cannot address exactly and settings outside 1..8 bits per channel.
func TestRNGCursorRejectsUnsupported(t *testing.T) {
	r := image.Rect(0, 0, 4, 4)
	for _, img := range []draw.Image{
		image.NewGray(r), image.NewRGBA64(r), image.NewNRGBA64(r),
		image.NewPaletted(r, color.Palette{color.Black, color.White}),
		(*image.RGBA)(nil),
	} {
		_, err := cursors.NewRNGCursor(img)
		require.ErrorIs(t, err, cursors.ErrUnsupportedImage, "%T", img)
		require.ErrorIs(t, cursors.CheckImage(img), cursors.ErrUnsupportedImage, "%T", img)
	}
	for _, bpc := range []int{0, 9, -1} {
		_, err := cursors.NewRNGCursor(image.NewRGBA(r), cursors.WithBitsPerChannel(bpc))
		require.Error(t, err, "bpc=%d", bpc)
	}
	_, err := cursors.NewRNGCursor(image.NewRGBA(r), cursors.WithSharedPoints(make([]uint32, 17)))
	require.Error(t, err, "order longer than the image")
}

// TestRNGCursorWritesExactBits checks the cursor stores bits in the Pix bytes
// of both supported types, including NRGBA pixels with alpha < 255 (which a
// premultiplying At/Set round trip would destroy), and never touches alpha.
func TestRNGCursorWritesExactBits(t *testing.T) {
	r := image.Rect(0, 0, 8, 8)
	nrgba := image.NewNRGBA(r)
	for i := range nrgba.Pix {
		nrgba.Pix[i] = byte(i * 37)
	}
	for i := 3; i < len(nrgba.Pix); i += 4 {
		nrgba.Pix[i] = byte(i % 3 * 60) // alpha 0, 60, 120
	}
	rgba := image.NewRGBA(r)
	copy(rgba.Pix, nrgba.Pix)

	for _, img := range []draw.Image{nrgba, rgba} {
		var pix []uint8
		switch m := img.(type) {
		case *image.NRGBA:
			pix = m.Pix
		case *image.RGBA:
			pix = m.Pix
		}
		alpha := make([]byte, 0, 64)
		for i := 3; i < len(pix); i += 4 {
			alpha = append(alpha, pix[i])
		}
		cur, err := cursors.NewRNGCursor(img, cursors.UseGreenBit(), cursors.UseBlueBit(),
			cursors.WithBitsPerChannel(8))
		require.NoError(t, err)
		payload := make([]byte, 64*3)
		for i := range payload {
			payload[i] = byte(255 - i)
		}
		readBack := writeAndReadAll(t, cursors.CursorAdapter(cur), payload)
		require.Equal(t, payload, readBack, "%T", img)
		// Row-major, 8 bits per channel: the payload is the RGB bytes verbatim.
		for p := 0; p < 64; p++ {
			require.Equal(t, payload[p*3:p*3+3], pix[p*4:p*4+3], "%T pixel %d", img, p)
			require.Equal(t, alpha[p], pix[p*4+3], "%T alpha of pixel %d", img, p)
		}
	}
}
