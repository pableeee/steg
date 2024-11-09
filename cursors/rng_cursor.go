package cursors

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"math/rand"
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

func writeBit(r *uint32, bit, n uint8) {
	if bit == 1 {
		mask := uint32(1 << n)
		*r = *r | mask
	} else {
		mask := uint32(0xffff & (1 << n))
		*r = *r & mask
	}
}

type RNGCursor struct {
	img            draw.Image
	cursor         int64
	bitMask        BitColor
	colorsPerPoint uint
	bitsPerColor   uint
	useColors      []BitColor
	points         []image.Point
	rng            *rand.Rand
}

type Option func(*RNGCursor)

func UseGreenBit() Option {
	return func(c *RNGCursor) {
		c.bitMask |= G_Bit
	}
}

func UseBlueBit() Option {
	return func(c *RNGCursor) {
		c.bitMask |= B_Bit
	}
}

func WithSeed(seed int64) Option {
	return func(c *RNGCursor) {
		c.rng = rand.New(rand.NewSource(seed))
	}
}

func WithBitsPerColor(bits uint) Option {
	return func(c *RNGCursor) {
		if bits <= 8 && bits > 0 {
			c.bitsPerColor = bits
		}
	}
}

func NewRNGCursor(img draw.Image, options ...Option) *RNGCursor {
	c := &RNGCursor{img: img, bitMask: R_Bit, rng: rand.New(rand.NewSource(0)), bitsPerColor: 1}
	for _, opt := range options {
		opt(c)
	}

	c.points = generateSequence(img.Bounds().Max.X, img.Bounds().Max.Y, c.rng)
	for _, color := range Colors {
		if c.bitMask&color == color {
			c.colorsPerPoint++
			c.useColors = append(c.useColors, color)
		}
	}

	return c
}

var _ Cursor = (*RNGCursor)(nil)

func (c *RNGCursor) maxSize() int64 {
	return int64(c.img.Bounds().Max.X) * int64(c.img.Bounds().Max.Y) * int64(c.colorsPerPoint) * int64(c.bitsPerColor)
}

func (c *RNGCursor) validateBounds(n int64) bool {
	if n > c.maxSize() {
		return false
	}

	planeCursor := c.cursor / int64(c.colorsPerPoint*c.bitsPerColor)
	return planeCursor < int64(len(c.points))
}

func (c *RNGCursor) tell() (x, y int, cl BitColor, bit uint8) {

	planeCursor := c.cursor / int64(c.colorsPerPoint*c.bitsPerColor)
	colorCursor := (c.cursor / int64(c.bitsPerColor)) % int64(c.colorsPerPoint)

	x = c.points[planeCursor].X
	y = c.points[planeCursor].Y
	cl = c.useColors[colorCursor]
	bit = uint8(c.cursor % int64(c.bitsPerColor))

	return
}

func (c *RNGCursor) Seek(n int64, whence int) (int64, error) {
	// Seek sets the offset for the next Read or Write to offset,
	// interpreted according to whence:
	// [SeekStart] means relative to the start of the file,
	// [SeekCurrent] means relative to the current offset, and
	// [SeekEnd] means relative to the end
	if !c.validateBounds(n) {
		return c.cursor, fmt.Errorf("out of bounds: %w", io.EOF)
	}

	switch whence {
	case io.SeekStart:
		if n < 0 {
			return c.cursor, fmt.Errorf("illegal argument")
		}
		c.cursor = n
	case io.SeekCurrent:
		if n < 0 && (n*-1) > c.cursor {
			return c.cursor, fmt.Errorf("illegal argument")
		}
		c.cursor += n
	case io.SeekEnd:
		max := int64(c.img.Bounds().Max.X) * int64(c.img.Bounds().Max.Y) * int64(c.colorsPerPoint) * (int64(c.bitsPerColor))
		if n > 0 || (n*-1) > max {
			return c.cursor, fmt.Errorf("illegal argument")
		}

		c.cursor = max + n
	}

	return c.cursor, nil
}

func (c *RNGCursor) WriteBit(bit uint8) (uint, error) {
	if !c.validateBounds(c.cursor) {
		return uint(c.cursor), fmt.Errorf("out of bounds: %w", io.EOF)
	}

	x, y, col, colBit := c.tell()

	r, g, b, a := c.img.At(x, y).RGBA()
	switch col {
	case R_Bit:
		writeBit(&r, bit, colBit)
	case G_Bit:
		writeBit(&g, bit, colBit)
	case B_Bit:
		writeBit(&b, bit, colBit)
	}

	c.img.Set(x, y, color.RGBA{uint8(r), uint8(g), uint8(b), uint8(a)})

	c.cursor++

	return uint(c.cursor), nil
}

func (c *RNGCursor) ReadBit() (uint8, error) {
	if !c.validateBounds(c.cursor) {
		return 0, fmt.Errorf("out of bounds: %w", io.EOF)
	}
	x, y, colorBit, _ := c.tell()
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
