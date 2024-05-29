package cursors

import (
	"fmt"
	"image"
	"image/color"
	"io"
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

				_, _, c, _ := cur.tell()
				assert.Equal(t, test.bits[i%len(test.bits)], c)
			}
		}
	})
}

func TestReadBit(t *testing.T) {
	t.Run("should fail on a read after the bits available on the cursor config", func(t *testing.T) {
		for bits := uint(1); bits < 8; bits++ {
			for _, test := range testCases {
				img := image.NewRGBA(image.Rect(0, 0, 10, 10))
				test.opts = append(test.opts, WithBitsPerColor(bits))
				cur := NewRNGCursor(img, test.opts...)

				maxAvailable := cur.maxSize()
				for i := int64(0); i < maxAvailable; i++ {
					x, y, _, _ := cur.tell()
					assert.True(t, x < img.Bounds().Max.X)
					assert.True(t, y < img.Bounds().Max.Y)

					_, err := cur.ReadBit()
					require.NoError(t, err)
				}

				_, err := cur.ReadBit()
				assert.Error(t, err)
			}
		}
	})

	t.Run("should read sequentially the bits in the image", func(t *testing.T) {
		for _, test := range testCases {
			for bits := uint(1); bits < 8; bits++ {
				img := image.NewRGBA(image.Rect(0, 0, 2, 2))
				cur := NewRNGCursor(img, test.opts...)
				maxAvailable := cur.maxSize()

				// fol all pixes in the image.
				for i := 0; i < img.Bounds().Max.X*img.Bounds().Max.Y; i++ {
					// create color using seq no.
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

				for i := 0; i < int(maxAvailable)/int(cur.bitCount); i++ {
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
		}
	})
}

func TestWriteBit(t *testing.T) {
	type writeResult struct {
		color BitColor
		bit   uint8
	}
	t.Run("should write until the cursors EOF, check within the bounds", func(t *testing.T) {
		// test will run using a max of 7 bits out of a RGB value to encode data.
		for bits := uint(1); bits < 8; bits++ {
			// test using all 3 RBG values.
			for _, test := range testCases {
				img := image.NewRGBA(image.Rect(0, 0, 10, 10))
				test.opts = append(test.opts, WithBitsPerColor(bits))

				cur := NewRNGCursor(img, test.opts...)
				maxAvailable := cur.maxSize()
				// loop all available bits, and check they are within the image an RBB bits bounds.
				for i := int64(0); i < maxAvailable; i++ {
					x, y, col, bitColor := cur.tell()
					assert.True(t, x < img.Bounds().Max.X)
					assert.True(t, y < img.Bounds().Max.Y)
					assert.Equal(t, bitColor, uint8(uint(i)%cur.bitPerColor))

					assert.Equal(t, col, cur.useBits[(i/int64(cur.bitPerColor))%int64(cur.bitCount)])

					_, err := cur.WriteBit(1)
					require.NoError(t, err)
				}

				// after writing the max, next write should fail with EOF.
				_, err := cur.WriteBit(1)
				assert.ErrorIs(t, err, io.EOF)
			}
		}
	})

	t.Run("should write until the cursors EOF, for each color, up to 7 bits", func(t *testing.T) {
		// test will run using a max of 7 bits out of a RGB value to encode data.
		for bits := uint(1); bits < 8; bits++ {
			// test using all 3 RBG values.
			for _, test := range testCases {
				m := image.NewRGBA(image.Rect(0, 0, 10, 10))
				test.opts = append(test.opts, WithBitsPerColor(bits))
				cur := NewRNGCursor(m, test.opts...)
				maxAvailable := cur.maxSize()

				// set a black image
				for i := 0; i < m.Bounds().Max.X*m.Bounds().Max.X; i++ {
					x := i % m.Bounds().Max.X
					y := i / m.Bounds().Max.Y
					m.Set(x, y, color.Black)
				}

				// set all the bites to 1, for the current config.
				for i := int64(0); i < maxAvailable; i++ {
					_, err := cur.WriteBit(uint8(1))
					require.NoError(t, err)
				}

				var mask uint32
				for i := 0; i < int(cur.bitPerColor); i++ {
					mask |= 1 << i
				}

				r, g, b, a := color.Black.RGBA()
				for _, col := range cur.useBits {
					switch col {
					case R_Bit:
						r = mask
					case G_Bit:
						g = mask
					case B_Bit:
						b = mask
					}
				}

				c := m.ColorModel().Convert(color.RGBA{uint8(r), uint8(g), uint8(b), uint8(a)})
				r, g, b, _ = c.RGBA()
				for _, p := range cur.points {
					cr, cg, cb, _ := m.At(p.X, p.Y).RGBA()
					assert.Equal(t, r, cr, "checking red")
					assert.Equal(t, g, cg, "checking green")
					assert.Equal(t, b, cb, "checking blue")
				}
			}
		}
	})
}
