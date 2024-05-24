package rng

import (
	"fmt"
	"image"
	"image/color"
	"math/rand"

	"github.com/pableeee/steg/cursors"
)

const offLast = 0xfffe
const justLast = 0x0001

func generateSequence(width, height int, rng *rand.Rand) []image.Point {
	totalPixels := width * height
	positions := make([]image.Point, totalPixels)

	// Initialize the positions with sequential values
	for i := 0; i < totalPixels; i++ {
		positions[i] = image.Point{X: i % width, Y: i / width}
	}

	// Shuffle the positions to create a pseudo-random sequence
	for i := totalPixels - 1; i > 0; i-- {
		j := rng.Intn(i + 1)
		positions[i], positions[j] = positions[j], positions[i]
	}

	return positions
}

type RNGCursor struct {
	img      cursors.ChangeableImage
	cursor   uint
	bitMask  cursors.BitColor
	bitCount uint
	useBits  []cursors.BitColor
	points   []image.Point
	rng      *rand.Rand
}

type Option func(*RNGCursor)

func UseRedBit() Option {
	return func(c *RNGCursor) {
		c.bitMask |= cursors.R_Bit
	}
}

func UseGreenBit() Option {
	return func(c *RNGCursor) {
		c.bitMask |= cursors.G_Bit
	}
}

func UseBlueBit() Option {
	return func(c *RNGCursor) {
		c.bitMask |= cursors.B_Bit
	}
}

func WithSeed(seed int64) Option {
	return func(c *RNGCursor) {
		c.rng = rand.New(rand.NewSource(seed))
	}
}

func NewRNGCursor(img cursors.ChangeableImage, options ...Option) cursors.Cursor {
	c := &RNGCursor{img: img, bitMask: cursors.R_Bit, rng: rand.New(rand.NewSource(0))}
	for _, opt := range options {
		opt(c)
	}

	c.points = generateSequence(img.Bounds().Max.X, img.Bounds().Max.Y, c.rng)
	for _, color := range cursors.Colors {
		if c.bitMask&color == color {
			c.bitCount++
			c.useBits = append(c.useBits, color)
		}
	}

	return c
}

var _ cursors.Cursor = (*RNGCursor)(nil)

func (c *RNGCursor) validateBounds(n uint) bool {
	max := uint(c.img.Bounds().Max.X) * uint(c.img.Bounds().Max.Y) * c.bitCount
	if n > max {
		return false
	}

	return true
}

func (c *RNGCursor) tell() (x, y int, cl cursors.BitColor) {

	planeCursor := c.cursor / c.bitCount
	colorCursor := c.cursor % c.bitCount

	x = c.points[planeCursor].X
	y = c.points[planeCursor].Y

	cl = c.useBits[colorCursor]

	return
}

func (c *RNGCursor) Seek(n uint) error {
	if !c.validateBounds(n) {
		return fmt.Errorf("out of bounds")
	}

	c.cursor = n

	return nil
}

func (c *RNGCursor) WriteBit(bit uint8) (uint, error) {
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
	case cursors.R_Bit:
		fn(&r)
	case cursors.G_Bit:
		fn(&g)
	case cursors.B_Bit:
		fn(&b)
	}

	c.img.Set(x, y, color.RGBA{uint8(r), uint8(g), uint8(b), uint8(a)})

	c.cursor++

	return c.cursor, nil
}

func (c *RNGCursor) ReadBit() (uint8, error) {
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
