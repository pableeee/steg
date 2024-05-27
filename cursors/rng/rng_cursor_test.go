package rng

import (
	"image"
	"image/color"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pableeee/steg/cursors"
)

func TestSeek(t *testing.T) {
	t.Run("should fail on a seek larger that the bits available on the cursor config", func(t *testing.T) {
		_ = gomock.NewController(t)
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))
		cur := NewRNGCursor(img)

		maxAvailable := img.Bounds().Max.X * img.Bounds().Max.X * int(cur.bitCount)
		for i := 0; i < maxAvailable; i++ {
			err := cur.Seek(uint(i))
			require.NoError(t, err)
		}

		err := cur.Seek(uint(maxAvailable + 1))
		assert.Error(t, err)
	})

	t.Run("cursor should only move on the configured bits", func(t *testing.T) {
		_ = gomock.NewController(t)
		img := image.NewRGBA(image.Rect(0, 0, 10, 10))

		testCases := []struct {
			opts []Option
			bits []cursors.BitColor
		}{
			{
				opts: []Option{},
				bits: []cursors.BitColor{cursors.R_Bit},
			},
			{
				opts: []Option{UseGreenBit()},
				bits: []cursors.BitColor{cursors.R_Bit, cursors.G_Bit},
			},
			{
				opts: []Option{UseGreenBit(), UseBlueBit()},
				bits: []cursors.BitColor{cursors.R_Bit, cursors.G_Bit, cursors.B_Bit},
			},
		}

		for _, test := range testCases {
			cur := NewRNGCursor(img, test.opts...)
			maxAvailable := img.Bounds().Max.X * img.Bounds().Max.X * int(cur.bitCount)
			for i := 0; i < maxAvailable; i++ {
				err := cur.Seek(uint(i))
				require.NoError(t, err)

				_, _, c := cur.tell()
				assert.Equal(t, test.bits[i%len(test.bits)], c)
			}
		}

	})
}

func TestReadBit(t *testing.T) {
	type readResult struct {
		color cursors.BitColor
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
					case cursors.R_Bit:
						c.R = uint8(i + e)
					case cursors.G_Bit:
						c.G = uint8(i + e)
					case cursors.B_Bit:
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
					case cursors.R_Bit:
						val = r
					case cursors.G_Bit:
						val = g
					case cursors.B_Bit:
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
		color cursors.BitColor
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
					case cursors.R_Bit:
						val = uint8(r)
					case cursors.G_Bit:
						val = uint8(g)
					case cursors.B_Bit:
						val = uint8(b)
					}
					actual := uint8(val & 0x0001)
					assert.Equal(t, res.bit, actual)
				}
			}

		}
	})
}
