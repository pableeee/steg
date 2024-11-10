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

// RNGCursor allows reading and writing bits to an image in a pseudo-random sequence.
type RNGCursor struct {
	img          draw.Image
	cursor       int64
	bitMask      BitColor
	bitCount     uint
	useBits      []BitColor
	points       []image.Point
	rng          *rand.Rand
	maxCursorPos int64
	bitDepth     uint8
}

// Option is a function that configures the RNGCursor.
type Option func(*RNGCursor)

// UseGreenBit includes the green channel in data manipulation.
func UseGreenBit() Option {
	return func(c *RNGCursor) {
		c.bitMask |= G_Bit
	}
}

// UseBlueBit includes the blue channel in data manipulation.
func UseBlueBit() Option {
	return func(c *RNGCursor) {
		c.bitMask |= B_Bit
	}
}

// WithSeed sets the seed for the random number generator.
func WithSeed(seed int64) Option {
	return func(c *RNGCursor) {
		c.rng = rand.New(rand.NewSource(seed))
	}
}

// WithBitDepth sets the bit depth per channel for the image.
func WithBitDepth(bitDepth uint8) Option {
	return func(c *RNGCursor) {
		c.bitDepth = bitDepth
	}
}

// NewRNGCursor creates a new RNGCursor with the specified options.
func NewRNGCursor(img draw.Image, options ...Option) *RNGCursor {
	c := &RNGCursor{
		img:      img,
		bitMask:  R_Bit, // Default to using the red bit
		rng:      rand.New(rand.NewSource(0)),
		bitDepth: 8, // Default bit depth
	}
	for _, opt := range options {
		opt(c)
	}

	c.points = generateSequence(img.Bounds().Dx(), img.Bounds().Dy(), c.rng)
	for _, color := range Colors {
		if c.bitMask&color == color {
			c.bitCount++
			c.useBits = append(c.useBits, color)
		}
	}

	c.maxCursorPos = int64(len(c.points)) * int64(c.bitCount)

	return c
}

// Ensure RNGCursor implements the Cursor interface.
var _ Cursor = (*RNGCursor)(nil)

// validateBounds checks if the cursor position is within valid bounds.
func (c *RNGCursor) validateBounds(n int64) bool {
	return n >= 0 && n < c.maxCursorPos
}

// tell calculates the current position and color bit to manipulate.
func (c *RNGCursor) tell() (x, y int, cl BitColor) {
	planeCursor := c.cursor / int64(c.bitCount)
	colorCursor := c.cursor % int64(c.bitCount)

	x = c.points[planeCursor].X
	y = c.points[planeCursor].Y

	cl = c.useBits[colorCursor]

	return
}

// Seek sets the cursor position based on offset and whence.
func (c *RNGCursor) Seek(offset int64, whence int) (int64, error) {
	var newCursor int64

	switch whence {
	case io.SeekStart:
		newCursor = offset
	case io.SeekCurrent:
		newCursor = c.cursor + offset
	case io.SeekEnd:
		newCursor = c.maxCursorPos + offset
	default:
		return c.cursor, fmt.Errorf("invalid whence")
	}

	if !c.validateBounds(newCursor) {
		return c.cursor, fmt.Errorf("seek position out of bounds")
	}

	c.cursor = newCursor
	return c.cursor, nil
}

// WriteBit writes a single bit to the image at the current cursor position.
func (c *RNGCursor) WriteBit(bit uint8) (uint, error) {
	if !c.validateBounds(c.cursor) {
		return uint(c.cursor), fmt.Errorf("write position out of bounds")
	}

	x, y, colorBit := c.tell()

	// Get the color at the pixel
	clr := c.img.At(x, y)

	// Extract color components based on the image's color model
	r, g, b, a := c.extractColorComponents(clr)

	// Modify the LSB of the selected color component
	switch colorBit {
	case R_Bit:
		r = c.setLSB(r, bit)
	case G_Bit:
		g = c.setLSB(g, bit)
	case B_Bit:
		b = c.setLSB(b, bit)
	}

	// Set the new color at the pixel
	newColor := c.combineColorComponents(r, g, b, a)
	c.img.Set(x, y, newColor)

	c.cursor++

	return uint(c.cursor), nil
}

// ReadBit reads a single bit from the image at the current cursor position.
func (c *RNGCursor) ReadBit() (uint8, error) {
	if !c.validateBounds(c.cursor) {
		return 0, fmt.Errorf("read position out of bounds")
	}

	x, y, colorBit := c.tell()

	// Get the color at the pixel
	clr := c.img.At(x, y)

	// Extract color components based on the image's color model
	r, g, b, _ := c.extractColorComponents(clr)

	var value uint16
	switch colorBit {
	case R_Bit:
		value = r
	case G_Bit:
		value = g
	case B_Bit:
		value = b
	}

	// Extract the LSB
	bit := uint8(value & 0x0001)

	c.cursor++

	return bit, nil
}

// extractColorComponents extracts the color components from the color based on bit depth.
func (c *RNGCursor) extractColorComponents(clr color.Color) (r, g, b, a uint16) {
	switch c.bitDepth {
	case 8:
		if nrgba, ok := clr.(color.NRGBA); ok {
			r = uint16(nrgba.R)
			g = uint16(nrgba.G)
			b = uint16(nrgba.B)
			a = uint16(nrgba.A)
		} else {
			r8, g8, b8, a8 := clr.RGBA()
			r = uint16(r8 >> 8)
			g = uint16(g8 >> 8)
			b = uint16(b8 >> 8)
			a = uint16(a8 >> 8)
		}
	case 16:
		if nrgba64, ok := clr.(color.NRGBA64); ok {
			r = nrgba64.R
			g = nrgba64.G
			b = nrgba64.B
			a = nrgba64.A
		} else {
			r32, g32, b32, a32 := clr.RGBA()
			r = uint16(r32)
			g = uint16(g32)
			b = uint16(b32)
			a = uint16(a32)
		}
	default:
		// For other bit depths, scale accordingly
		r32, g32, b32, a32 := clr.RGBA()
		scale := uint32(1<<c.bitDepth - 1)
		r = uint16((r32 * scale) / 0xFFFF)
		g = uint16((g32 * scale) / 0xFFFF)
		b = uint16((b32 * scale) / 0xFFFF)
		a = uint16((a32 * scale) / 0xFFFF)
	}
	return
}

// combineColorComponents creates a color from the components based on bit depth.
func (c *RNGCursor) combineColorComponents(r, g, b, a uint16) color.Color {
	switch c.bitDepth {
	case 8:
		return color.NRGBA{
			R: uint8(r),
			G: uint8(g),
			B: uint8(b),
			A: uint8(a),
		}
	case 16:
		return color.NRGBA64{
			R: r,
			G: g,
			B: b,
			A: a,
		}
	default:
		// For other bit depths, scale values accordingly
		scale := uint32(1<<c.bitDepth - 1)
		r32 := uint32(r) * 0xFFFF / scale
		g32 := uint32(g) * 0xFFFF / scale
		b32 := uint32(b) * 0xFFFF / scale
		a32 := uint32(a) * 0xFFFF / scale
		return color.NRGBA64{
			R: uint16(r32),
			G: uint16(g32),
			B: uint16(b32),
			A: uint16(a32),
		}
	}
}

// setLSB sets or clears the least significant bit of a value.
func (c *RNGCursor) setLSB(value uint16, bit uint8) uint16 {
	if bit == 1 {
		return value | 0x0001 // Set LSB
	}
	return value &^ 0x0001 // Clear LSB
}
