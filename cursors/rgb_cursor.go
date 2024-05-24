package cursors

import (
	"fmt"
	"image/color"
	"math"
)

type BitColor uint

const (
	NONE  BitColor = iota
	R_Bit BitColor = 0x1
	G_Bit BitColor = 0x2
	B_Bit BitColor = 0x4
)

var (
	Colors = []BitColor{R_Bit, G_Bit, B_Bit}
)

type RGBCursor struct {
	img      ChangeableImage
	cursor   uint
	bitMask  BitColor
	bitCount uint
	useBits  []BitColor
}

type Option func(*RGBCursor)

func UseRedBit() Option {
	return func(c *RGBCursor) {
		c.bitMask |= R_Bit
	}
}

func UseGreenBit() Option {
	return func(c *RGBCursor) {
		c.bitMask |= G_Bit
	}
}

func UseBlueBit() Option {
	return func(c *RGBCursor) {
		c.bitMask |= B_Bit
	}
}

func NewOnlyRedCursor(img ChangeableImage, options ...Option) Cursor {
	c := &RGBCursor{img: img, bitMask: R_Bit}
	for _, opt := range options {
		opt(c)
	}

	for _, color := range Colors {
		if c.bitMask&color == color {
			c.bitCount++
			c.useBits = append(c.useBits, color)
		}
	}

	return c
}

var _ Cursor = (*RGBCursor)(nil)

func (c *RGBCursor) validateBounds(n uint) bool {
	max := uint(c.img.Bounds().Max.X) * uint(c.img.Bounds().Max.Y) * c.bitCount
	if n > max {
		return false
	}

	return true
}

func (c *RGBCursor) tell() (x, y int, cl BitColor) {

	planeCursor := c.cursor / c.bitCount
	colorCursor := c.cursor % c.bitCount

	x = int(math.Mod(float64(planeCursor), float64(c.img.Bounds().Max.X)))
	y = int(math.Floor((float64(planeCursor) / float64(c.img.Bounds().Max.X))))

	cl = c.useBits[colorCursor]

	return
}

func (c *RGBCursor) Seek(n uint) error {
	if !c.validateBounds(n) {
		return fmt.Errorf("out of bounds")
	}

	c.cursor = n

	return nil
}

func (c *RGBCursor) WriteBit(bit uint8) (uint, error) {
	if !c.validateBounds(c.cursor) {
		return c.cursor, fmt.Errorf("out of bounds")
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
	case R_Bit:
		fn(&r)
	case G_Bit:
		fn(&g)
	case B_Bit:
		fn(&b)
	}

	c.img.Set(x, y, color.RGBA{uint8(r), uint8(g), uint8(b), uint8(a)})

	c.cursor++

	return c.cursor, nil
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
	case R_Bit:
		val = r
	case G_Bit:
		val = g
	case B_Bit:
		val = b
	}

	bit := val & justLast

	return uint8(bit), nil

}
