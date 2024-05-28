package rgb

import (
	"fmt"
	"image/color"
	"io"
	"math"

	"github.com/pableeee/steg/cursors"
)

const offLast = 0xfffe
const justLast = 0x0001

type RGBCursor struct {
	img      cursors.ChangeableImage
	cursor   int64
	bitMask  cursors.BitColor
	bitCount uint
	useBits  []cursors.BitColor
}

type Option func(*RGBCursor)

func UseGreenBit() Option {
	return func(c *RGBCursor) {
		c.bitMask |= cursors.G_Bit
	}
}

func UseBlueBit() Option {
	return func(c *RGBCursor) {
		c.bitMask |= cursors.B_Bit
	}
}

// NewRGBCursor by default it uses R_bit to write, but you can also add G & B bits.
func NewRGBCursor(img cursors.ChangeableImage, options ...Option) *RGBCursor {
	c := &RGBCursor{img: img, bitMask: cursors.R_Bit}
	for _, opt := range options {
		opt(c)
	}

	for _, color := range cursors.Colors {
		if c.bitMask&color == color {
			c.bitCount++
			c.useBits = append(c.useBits, color)
		}
	}

	return c
}

var _ cursors.Cursor = (*RGBCursor)(nil)

func (c *RGBCursor) validateBounds(n int64) bool {
	max := int64(c.img.Bounds().Max.X) * int64(c.img.Bounds().Max.Y) * int64(c.bitCount)
	if n >= max {
		return false
	}

	return true
}

func (c *RGBCursor) tell() (x, y int, cl cursors.BitColor) {

	planeCursor := c.cursor / int64(c.bitCount)
	colorCursor := c.cursor % int64(c.bitCount)

	x = int(math.Mod(float64(planeCursor), float64(c.img.Bounds().Max.X)))
	y = int(math.Floor((float64(planeCursor) / float64(c.img.Bounds().Max.X))))

	cl = c.useBits[colorCursor]

	return
}

func (c *RGBCursor) Seek(n int64, whence int) (int64, error) {
	if !c.validateBounds(n) {
		return c.cursor, fmt.Errorf("out of bounds")
	}

	switch whence {
	case io.SeekStart:
		c.cursor = n
	case io.SeekCurrent:
		c.cursor += n
	case io.SeekEnd:
		return 0, fmt.Errorf("not implemented")
	}

	return c.cursor, nil
}

func (c *RGBCursor) WriteBit(bit uint8) (uint, error) {
	if !c.validateBounds(c.cursor) {
		return uint(c.cursor), fmt.Errorf("out of bounds")
	}

	fn := func(r *uint32) {
		if bit == 1 {
			*r = *r | justLast
		} else {
			*r = *r & offLast
		}
	}

	x, y, colorBit := c.tell()

	r, g, b, a := c.img.At(x, y).RGBA()
	switch colorBit {
	case cursors.R_Bit:
		fn(&r)
	case cursors.G_Bit:
		fn(&g)
	case cursors.B_Bit:
		fn(&b)
	}

	c.img.Set(x, y, color.RGBA{uint8(r), uint8(g), uint8(b), uint8(a)})

	c.cursor++

	return uint(c.cursor), nil
}

func (c *RGBCursor) ReadBit() (uint8, error) {
	if !c.validateBounds(c.cursor) {
		return 0, fmt.Errorf("out of bounds")
	}
	x, y, colorBit := c.tell()
	r, g, b, _ := c.img.At(x, y).RGBA()
	c.cursor++
	val := r

	switch colorBit {
	case cursors.R_Bit:
		val = r
	case cursors.G_Bit:
		val = g
	case cursors.B_Bit:
		val = b
	}

	bit := val & justLast

	return uint8(bit), nil

}
