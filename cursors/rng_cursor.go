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

// getBitMask returns a mask for the LSBs based on bitsPerChannel
func getBitMask(bitsPerChannel int) uint32 {
	if bitsPerChannel <= 0 {
		return justLast
	}
	if bitsPerChannel > 3 {
		return 0x0007 // 3 bits max
	}
	return (1 << bitsPerChannel) - 1
}

// getClearMask returns a mask to clear the bits based on bitsPerChannel
func getClearMask(bitsPerChannel int) uint32 {
	if bitsPerChannel <= 0 {
		return offLast
	}
	if bitsPerChannel > 3 {
		return 0xfff8 // clear 3 bits max
	}
	return ^((1 << bitsPerChannel) - 1) | 0xffff0000 // Keep upper 16 bits from RGBA format
}

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
	img            draw.Image
	cursor         int64
	bitMask        BitColor
	bitCount       uint
	useBits        []BitColor
	points         []image.Point
	rng            *rand.Rand
	bitsPerChannel int // Number of bits to use per channel (1-3)
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

// WithBitsPerChannel sets the number of bits to use per color channel (1-3).
// Default is 1 bit per channel. More bits increase capacity but may be more noticeable.
func WithBitsPerChannel(n int) Option {
	return func(c *RNGCursor) {
		if n < 1 {
			n = 1
		} else if n > 3 {
			n = 3
		}
		c.bitsPerChannel = n
	}
}

func NewRNGCursor(img draw.Image, options ...Option) *RNGCursor {
	c := &RNGCursor{
		img:            img,
		bitMask:        R_Bit,
		rng:            rand.New(rand.NewSource(0)),
		bitsPerChannel: 1, // Default to 1 bit per channel
	}
	for _, opt := range options {
		opt(c)
	}

	c.points = generateSequence(img.Bounds().Max.X, img.Bounds().Max.Y, c.rng)
	for _, color := range Colors {
		if c.bitMask&color == color {
			c.bitCount++
			c.useBits = append(c.useBits, color)
		}
	}

	return c
}

var _ Cursor = (*RNGCursor)(nil)

func (c *RNGCursor) validateBounds(n int64) bool {
	max := c.Capacity()
	return n < max
}

// Capacity returns the total capacity in bits available for encoding.
func (c *RNGCursor) Capacity() int64 {
	width := int64(c.img.Bounds().Max.X)
	height := int64(c.img.Bounds().Max.Y)
	return width * height * int64(c.bitCount) * int64(c.bitsPerChannel)
}

func (c *RNGCursor) tell() (x, y int, cl BitColor, bitPos int) {
	bitsPerPixel := int64(c.bitCount) * int64(c.bitsPerChannel)
	planeCursor := c.cursor / bitsPerPixel
	remaining := c.cursor % bitsPerPixel
	colorCursor := remaining / int64(c.bitsPerChannel)
	bitPos = int(remaining % int64(c.bitsPerChannel))

	x = c.points[planeCursor].X
	y = c.points[planeCursor].Y

	cl = c.useBits[colorCursor]

	return
}

func (c *RNGCursor) Seek(n int64, whence int) (int64, error) {
	// Seek sets the offset for the next Read or Write to offset,
	// interpreted according to whence:
	// [SeekStart] means relative to the start of the file,
	// [SeekCurrent] means relative to the current offset, and
	// [SeekEnd] means relative to the end
	var newPos int64
	max := c.Capacity()

	switch whence {
	case io.SeekStart:
		if n < 0 {
			return c.cursor, fmt.Errorf("illegal argument")
		}
		newPos = n
	case io.SeekCurrent:
		if n < 0 && (n*-1) > c.cursor {
			return c.cursor, fmt.Errorf("illegal argument")
		}
		newPos = c.cursor + n
	case io.SeekEnd:
		if n > 0 || (n*-1) > max {
			return c.cursor, fmt.Errorf("illegal argument")
		}
		newPos = max + n
	default:
		return c.cursor, fmt.Errorf("invalid whence")
	}

	if newPos < 0 || newPos > max {
		return c.cursor, fmt.Errorf("out of bounds: %w", io.EOF)
	}

	c.cursor = newPos
	return c.cursor, nil
}

func (c *RNGCursor) WriteBit(bit uint8) (uint, error) {
	if !c.validateBounds(c.cursor) {
		return uint(c.cursor), fmt.Errorf("out of bounds: %w", io.EOF)
	}

	x, y, colorBit, bitPos := c.tell()

	r, g, b, a := c.img.At(x, y).RGBA()
	
	// Convert RGBA (16-bit values) to 8-bit values
	r8 := uint8(r >> 8)
	g8 := uint8(g >> 8)
	b8 := uint8(b >> 8)
	a8 := uint8(a >> 8)

	// Modify the appropriate channel
	switch colorBit {
	case R_Bit:
		// Clear the bit at the specific position
		clearMask := ^(uint8(1) << bitPos)
		r8 = r8 & clearMask
		// Set the bit if needed
		if bit == 1 {
			setMask := uint8(1) << bitPos
			r8 = r8 | setMask
		}
	case G_Bit:
		clearMask := ^(uint8(1) << bitPos)
		g8 = g8 & clearMask
		if bit == 1 {
			setMask := uint8(1) << bitPos
			g8 = g8 | setMask
		}
	case B_Bit:
		clearMask := ^(uint8(1) << bitPos)
		b8 = b8 & clearMask
		if bit == 1 {
			setMask := uint8(1) << bitPos
			b8 = b8 | setMask
		}
	default:
		return uint(c.cursor), fmt.Errorf("invalid color bit")
	}

	c.img.Set(x, y, color.RGBA{r8, g8, b8, a8})

	c.cursor++

	return uint(c.cursor), nil
}

func (c *RNGCursor) ReadBit() (uint8, error) {
	if !c.validateBounds(c.cursor) {
		return 0, fmt.Errorf("out of bounds")
	}
	x, y, colorBit, bitPos := c.tell()
	r, g, b, _ := c.img.At(x, y).RGBA()
	
	var val uint32
	switch colorBit {
	case R_Bit:
		val = r
	case G_Bit:
		val = g
	case B_Bit:
		val = b
	default:
		return 0, fmt.Errorf("invalid color bit")
	}

	// Extract the 8-bit value (RGBA returns 16-bit values)
	val8Bit := uint8(val >> 8)
	
	// Extract the bit at the specific position
	bit := (val8Bit >> bitPos) & 1

	c.cursor++

	return bit, nil
}
