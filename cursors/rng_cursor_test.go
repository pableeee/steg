package cursors

import (
	"image"
	"image/color"
	"io"
	"testing"

	"github.com/golang/mock/gomock"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeek(t *testing.T) {
	t.Run("should fail on a seek larger that the bits available on the cursor config", func(t *testing.T) {
		_ = gomock.NewController(t)
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
	type readResult struct {
		color BitColor
		bit   uint8
	}
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
			// {
			// 	opts: []Option{UseGreenBit()},
			// },
			// {
			// 	opts: []Option{UseGreenBit(), UseBlueBit()},
			// },
		}

		for _, test := range testCases {
			_ = gomock.NewController(t)
			img := image.NewRGBA(image.Rect(0, 0, 2, 2))

			cur := NewRNGCursor(img, test.opts...)
			maxAvailable := (img.Bounds().Max.X * img.Bounds().Max.Y * int(cur.bitCount))

			for i := 0; i < maxAvailable/int(cur.bitCount); i++ {
				c := color.RGBA{R: uint8(i), G: uint8(i), B: uint8(i), A: uint8(i)}
				x := i % img.Bounds().Max.X
				y := int(i / img.Bounds().Max.X)
				for e := 0; e < int(cur.bitCount); e++ {
					b := cur.useBits[e%len(cur.useBits)]
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
					currBit := cur.useBits[(i+e)%len(cur.useBits)]
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

					assert.Equal(t, uint8(expected), read)
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
