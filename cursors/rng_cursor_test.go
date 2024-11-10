package cursors

import (
	"fmt"
	"image"
	"image/color"
	"io"
	"reflect"
	"testing"

	"github.com/golang/mock/gomock"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testCases = []struct {
	opts []Option
}{
	{
		opts: []Option{},
	},
	{
		opts: []Option{UseGreenBit()},
	},
	{
		opts: []Option{UseGreenBit(), UseBlueBit()},
	},
}

func TestSeek(t *testing.T) {
	t.Run("should fail seek using SeekStart using negative offset", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))
		cur := NewRNGCursor(img)
		n, err := cur.Seek(-1, io.SeekStart)
		assert.Error(t, err)
		assert.Equal(t, int64(0), n)
	})
	t.Run("should fail seek using SeekStart on eof ", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))
		cur := NewRNGCursor(img)
		n, err := cur.Seek(101, io.SeekStart)
		assert.Error(t, err)
		assert.Equal(t, int64(0), n)
	})
	t.Run("should succeed seek using SeekStart", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))
		cur := NewRNGCursor(img)
		n, err := cur.Seek(7, io.SeekStart)
		assert.NoError(t, err)
		assert.Equal(t, int64(7), n)
	})

	t.Run("should fail seek using SeekCurrent using a negative offset", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))
		cur := NewRNGCursor(img)
		n, err := cur.Seek(7, io.SeekCurrent)
		assert.NoError(t, err)
		assert.Equal(t, int64(7), n)

		n, err = cur.Seek(-8, io.SeekCurrent)
		assert.Error(t, err)
		assert.Equal(t, int64(7), n)
	})

	t.Run("should fail seek using SeekCurrent on eof ", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))
		cur := NewRNGCursor(img)
		n, err := cur.Seek(7, io.SeekCurrent)
		assert.NoError(t, err)
		assert.Equal(t, int64(7), n)

		n, err = cur.Seek(101, io.SeekCurrent)
		assert.Error(t, err)
		assert.Equal(t, int64(7), n)
	})

	t.Run("should succeed seek using SeekCurrent", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))
		cur := NewRNGCursor(img)
		n, err := cur.Seek(7, io.SeekCurrent)
		assert.NoError(t, err)
		assert.Equal(t, int64(7), n)

		n, err = cur.Seek(1, io.SeekCurrent)
		assert.NoError(t, err)
		assert.Equal(t, int64(8), n)
	})

	t.Run("should fail seek using SeekEnd using a positive offset", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))
		cur := NewRNGCursor(img)
		n, err := cur.Seek(7, io.SeekEnd)
		assert.Error(t, err)
		assert.Equal(t, int64(0), n)
	})

	t.Run("should fail seek using SeekEnd on eof", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))
		cur := NewRNGCursor(img)
		n, err := cur.Seek(-101, io.SeekEnd)
		assert.Error(t, err)
		assert.Equal(t, int64(0), n)
	})

	t.Run("should succeed seek using SeekEnd", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))
		cur := NewRNGCursor(img)
		n, err := cur.Seek(-99, io.SeekEnd)
		assert.NoError(t, err)
		assert.Equal(t, int64(1), n)
	})

	t.Run("should fail on a seek larger that the bits available on the cursor config", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))
		cur := NewRNGCursor(img)

		maxAvailable := img.Bounds().Max.X * img.Bounds().Max.X * int(cur.bitCount)
		for i := 0; i < maxAvailable; i++ {
			_, err := cur.Seek(int64(i), io.SeekStart)
			require.NoError(t, err)
		}

		_, err := cur.Seek(int64(maxAvailable+1), io.SeekStart)
		assert.Error(t, err)
	})

	t.Run("cursor should only move on the configured bits", func(t *testing.T) {
		_ = gomock.NewController(t)
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))

		testCases := []struct {
			opts []Option
			bits []BitColor
		}{
			{
				opts: []Option{},
				bits: []BitColor{R_Bit},
			},
			{
				opts: []Option{UseGreenBit()},
				bits: []BitColor{R_Bit, G_Bit},
			},
			{
				opts: []Option{UseGreenBit(), UseBlueBit()},
				bits: []BitColor{R_Bit, G_Bit, B_Bit},
			},
		}

		for _, test := range testCases {
			cur := NewRNGCursor(img, test.opts...)
			maxAvailable := img.Bounds().Max.X * img.Bounds().Max.X * int(cur.bitCount)
			for i := 0; i < maxAvailable; i++ {
				_, err := cur.Seek(int64(i), io.SeekStart)
				require.NoError(t, err)

				_, _, c := cur.tell()
				assert.Equal(t, test.bits[i%len(test.bits)], c)
			}
		}
	})
}

func TestReadBit(t *testing.T) {
	t.Run("should fail on a read after the bits available on the cursor config", func(t *testing.T) {
		_ = gomock.NewController(t)
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))
		cur := NewRNGCursor(img)

		maxAvailable := img.Bounds().Max.X * img.Bounds().Max.X * int(cur.bitCount)
		for i := 0; i < maxAvailable; i++ {
			x, y, _ := cur.tell()
			assert.True(t, x < img.Bounds().Max.X)
			assert.True(t, y < img.Bounds().Max.Y)

			_, err := cur.ReadBit()
			require.NoError(t, err)
		}

		_, err := cur.ReadBit()
		assert.Error(t, err)
	})

	t.Run("should read sequentially the bits in the image", func(t *testing.T) {
		testCases := []struct {
			opts []Option
		}{
			{
				opts: []Option{},
			},
			{
				opts: []Option{UseGreenBit()},
			},
			{
				opts: []Option{UseGreenBit(), UseBlueBit()},
			},
		}

		for _, test := range testCases {
			_ = gomock.NewController(t)
			img := image.NewRGBA(image.Rect(0, 0, 2, 2))

			cur := NewRNGCursor(img, test.opts...)
			maxAvailable := (img.Bounds().Max.X * img.Bounds().Max.Y * int(cur.bitCount))

			// fol all pixes in the image.
			for i := 0; i < maxAvailable/int(cur.bitCount); i++ {
				c := color.RGBA{R: uint8(i), G: uint8(i), B: uint8(i), A: uint8(i)}
				x := i % img.Bounds().Max.X
				y := int(i / img.Bounds().Max.X)
				// for all selected bit colors selected for payload encoding.
				for e := 0; e < int(cur.bitCount); e++ {
					index := (i * int(cur.bitCount)) + e
					b := cur.useBits[index%len(cur.useBits)]
					switch b {
					case R_Bit:
						c.R = uint8(i + e)
					case G_Bit:
						c.G = uint8(i + e)
					case B_Bit:
						c.B = uint8(i + e)
					}
				}
				img.Set(x, y, &c)
			}

			for i := 0; i < maxAvailable/int(cur.bitCount); i++ {
				for e := 0; e < int(cur.bitCount); e++ {
					index := (i * int(cur.bitCount)) + e
					currBit := cur.useBits[index%len(cur.useBits)]
					read, err := cur.ReadBit()
					require.NoError(t, err)

					r, g, b, _ := img.
						At(cur.points[i].X, cur.points[i].Y).RGBA()

					var val uint32
					switch currBit {
					case R_Bit:
						val = r
					case G_Bit:
						val = g
					case B_Bit:
						val = b
					}

					expected := uint8(val & 0x0001)

					assert.Equal(t, uint8(expected), read, fmt.Sprintf("testing with %+v", cur.useBits))
				}
			}
		}
	})
}

func TestWriteBit(t *testing.T) {
	type writeResult struct {
		color BitColor
		bit   uint8
	}
	t.Run("should fail on a write after the bits available on the cursor config", func(t *testing.T) {
		_ = gomock.NewController(t)
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))
		cur := NewRNGCursor(img)

		maxAvailable := img.Bounds().Max.X * img.Bounds().Max.X * int(cur.bitCount)
		for i := 0; i < maxAvailable; i++ {
			x, y, _ := cur.tell()
			assert.True(t, x < img.Bounds().Max.X)
			assert.True(t, y < img.Bounds().Max.Y)

			_, err := cur.WriteBit(1)
			require.NoError(t, err)
		}

		_, err := cur.WriteBit(1)
		assert.Error(t, err)
	})

	t.Run("should write sequentially the bits in the image", func(t *testing.T) {
		testCases := []struct {
			opts []Option
		}{
			{
				opts: []Option{},
			},
			{
				opts: []Option{UseGreenBit()},
			},
			{
				opts: []Option{UseGreenBit(), UseBlueBit()},
			},
		}

		for _, test := range testCases {
			m := image.NewRGBA(image.Rect(0, 0, 10, 10))

			cur := NewRNGCursor(m, test.opts...)
			maxAvailable := (m.Bounds().Max.X * m.Bounds().Max.Y * int(cur.bitCount))
			results := make([]writeResult, maxAvailable)

			for i := 0; i < maxAvailable/int(cur.bitCount); i++ {
				for e := 0; e < int(cur.bitCount); e++ {
					b := uint8(i & 0x0001)
					_, err := cur.WriteBit(b)
					require.NoError(t, err)
					results[(i*int(cur.bitCount))+e] = writeResult{color: cur.useBits[i%len(cur.useBits)], bit: b}
				}
			}

			for i, p := range cur.points {
				x := p.X
				y := p.Y
				c := m.At(x, y)
				for e := 0; e < int(cur.bitCount); e++ {
					res := results[(i*int(cur.bitCount))+e]
					var val uint8
					r, g, b, _ := c.RGBA()
					switch res.color {
					case R_Bit:
						val = uint8(r)
					case G_Bit:
						val = uint8(g)
					case B_Bit:
						val = uint8(b)
					}
					actual := uint8(val & 0x0001)
					assert.Equal(t, res.bit, actual)
				}
			}

		}
	})
}

func TestChatGPT(t *testing.T) {
	t.Run("TestRandomnessAndSeeds", func(t *testing.T) {
		for _, test := range testCases {
			img := image.NewNRGBA(image.Rect(0, 0, 10, 10))
			bitsToWrite := []uint8{1, 0, 1, 1, 0}

			// Same seed
			seed := int64(42)
			opts := append(test.opts, WithSeed(seed))
			cursor1 := NewRNGCursor(img, opts...)
			cursor2 := NewRNGCursor(img, opts...)

			// Write bits with cursor1
			for _, bit := range bitsToWrite {
				cursor1.WriteBit(bit)
			}

			// Seek cursor2 to start and read bits
			cursor2.Seek(0, io.SeekStart)
			bitsRead := make([]uint8, len(bitsToWrite))
			for i := range bitsRead {
				bit, err := cursor2.ReadBit()
				if err != nil {
					t.Fatalf("ReadBit failed: %v", err)
				}
				bitsRead[i] = bit
			}

			// Assert bits match
			if !reflect.DeepEqual(bitsToWrite, bitsRead) {
				t.Errorf("Bits read do not match bits written with same seed.\nExpected: %v\nGot:      %v", bitsToWrite, bitsRead)
			}

			// Different seed
			cursor3 := NewRNGCursor(img, WithSeed(seed+1))
			cursor3.Seek(0, io.SeekStart)
			bitsReadDifferentSeed := make([]uint8, len(bitsToWrite))
			for i := range bitsReadDifferentSeed {
				bit, err := cursor3.ReadBit()
				if err != nil {
					t.Fatalf("ReadBit failed: %v", err)
				}
				bitsReadDifferentSeed[i] = bit
			}

			// Assert bits do not match
			if reflect.DeepEqual(bitsToWrite, bitsReadDifferentSeed) {
				t.Errorf("Bits read match bits written with different seed, expected them to differ.")
			}
		}
	})

	t.Run("TestSeekFunctionality", func(t *testing.T) {
		img := image.NewNRGBA(image.Rect(0, 0, 10, 10))
		cursor := NewRNGCursor(img)

		// Write bits at different positions
		positions := []int64{0, 5, 10}
		bits := []uint8{1, 0, 1}

		for i, pos := range positions {
			_, err := cursor.Seek(pos, io.SeekStart)
			if err != nil {
				t.Fatalf("Seek failed: %v", err)
			}
			cursor.WriteBit(bits[i])
		}

		// Read bits back
		for i, pos := range positions {
			_, err := cursor.Seek(pos, io.SeekStart)
			if err != nil {
				t.Fatalf("Seek failed: %v", err)
			}
			bit, err := cursor.ReadBit()
			if err != nil {
				t.Fatalf("ReadBit failed: %v", err)
			}
			if bit != bits[i] {
				t.Errorf("Bit mismatch at position %d: expected %d, got %d", pos, bits[i], bit)
			}
		}

		// Invalid whence
		_, err := cursor.Seek(0, 999)
		if err == nil {
			t.Error("Expected error for invalid whence, got none")
		}
	})

	t.Run("TestInvalidBitValue", func(t *testing.T) {
		img := image.NewNRGBA(image.Rect(0, 0, 10, 10))
		cursor := NewRNGCursor(img)

		// Attempt to write an invalid bit value
		_, err := cursor.WriteBit(2)
		if err == nil {
			t.Error("Expected error when writing invalid bit value, got none")
		}
	})
}
