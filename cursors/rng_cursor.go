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

func GenerateSequence(width, height int, seed int64) []image.Point {
	rng := rand.New(rand.NewSource(seed))
	return generateSequence(width, height, rng)
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
	img      draw.Image
	cursor   int64
	bitMask  BitColor
	bitCount uint
	useBits  []BitColor
	points   []image.Point
	rng      *rand.Rand
	maxBits  int64 // pre-computed capacity in bits

	// pixel cache — amortises img.At() and img.Set() across the bits of one pixel.
	// Write-back: img.Set() is deferred until the cursor leaves the current pixel
	// (via loadPixel or Flush), so each pixel costs one At + one Set regardless
	// of how many of its bits are modified.
	pixelCached bool
	dirty       bool
	cacheIdx    int64
	cacheX      int
	cacheY      int
	cacheR      uint32
	cacheG      uint32
	cacheB      uint32
	cacheA      uint32
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

func WithSharedPoints(points []image.Point) Option {
	return func(c *RNGCursor) { c.points = points }
}

func NewRNGCursor(img draw.Image, options ...Option) *RNGCursor {
	c := &RNGCursor{img: img, bitMask: R_Bit, rng: rand.New(rand.NewSource(0))}
	for _, opt := range options {
		opt(c)
	}

	if c.points == nil {
		c.points = generateSequence(img.Bounds().Max.X, img.Bounds().Max.Y, c.rng)
	}
	for _, color := range Colors {
		if c.bitMask&color == color {
			c.bitCount++
			c.useBits = append(c.useBits, color)
		}
	}
	c.maxBits = int64(img.Bounds().Max.X) * int64(img.Bounds().Max.Y) * int64(c.bitCount)
	return c
}

func (c *RNGCursor) BitCount() uint { return c.bitCount }

var _ Cursor = (*RNGCursor)(nil)

func (c *RNGCursor) validateBounds(n int64) bool {
	return n < c.maxBits
}

// Flush writes the cached pixel to the image if it has been modified.
// Must be called after the final WriteByte before the cursor is abandoned.
func (c *RNGCursor) Flush() {
	if c.dirty {
		c.img.Set(c.cacheX, c.cacheY, color.RGBA{uint8(c.cacheR), uint8(c.cacheG), uint8(c.cacheB), uint8(c.cacheA)})
		c.dirty = false
	}
}

// loadPixel flushes the current dirty pixel (if any) then loads pixelIdx into cache.
func (c *RNGCursor) loadPixel(pixelIdx int64) {
	c.Flush()
	pt := c.points[pixelIdx]
	r, g, b, a := c.img.At(pt.X, pt.Y).RGBA()
	c.cacheIdx = pixelIdx
	c.cacheX, c.cacheY = pt.X, pt.Y
	c.cacheR, c.cacheG, c.cacheB, c.cacheA = r, g, b, a
	c.pixelCached = true
}

func (c *RNGCursor) Seek(n int64, whence int) (int64, error) {
	if !c.validateBounds(n) {
		return c.cursor, fmt.Errorf("out of bounds: %w", io.EOF)
	}

	c.Flush()
	c.pixelCached = false

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
		if n > 0 || (n*-1) > c.maxBits {
			return c.cursor, fmt.Errorf("illegal argument")
		}
		c.cursor = c.maxBits + n
	}

	return c.cursor, nil
}

// ReadByte reads 8 bits MSB-first from the cursor.
// pixelIdx and channelIdx are maintained by increment rather than division,
// and img.At() is called at most once per pixel (cache miss only).
func (c *RNGCursor) ReadByte() (uint8, error) {
	pixelIdx := c.cursor / int64(c.bitCount)
	channelIdx := int(c.cursor % int64(c.bitCount))
	var out uint8

	for i := 7; i >= 0; i-- {
		if c.cursor >= c.maxBits {
			return 0, fmt.Errorf("out of bounds: %w", io.EOF)
		}
		if !c.pixelCached || pixelIdx != c.cacheIdx {
			c.loadPixel(pixelIdx)
		}
		var val uint32
		switch c.useBits[channelIdx] {
		case R_Bit:
			val = c.cacheR
		case G_Bit:
			val = c.cacheG
		case B_Bit:
			val = c.cacheB
		}
		out |= uint8(val&1) << i
		c.cursor++
		channelIdx++
		if channelIdx >= int(c.bitCount) {
			channelIdx = 0
			pixelIdx++
		}
	}
	return out, nil
}

// WriteByte writes 8 bits MSB-first to the cursor.
// Uses write-back caching: img.Set() is deferred until the cursor moves to the
// next pixel or Flush() is called — one Set per pixel regardless of bit count.
func (c *RNGCursor) WriteByte(b uint8) error {
	pixelIdx := c.cursor / int64(c.bitCount)
	channelIdx := int(c.cursor % int64(c.bitCount))

	for i := 7; i >= 0; i-- {
		if c.cursor >= c.maxBits {
			return fmt.Errorf("out of bounds: %w", io.EOF)
		}
		if !c.pixelCached || pixelIdx != c.cacheIdx {
			c.loadPixel(pixelIdx) // flushes previous dirty pixel
		}
		bit := (b >> i) & 1
		switch c.useBits[channelIdx] {
		case R_Bit:
			if bit == 1 {
				c.cacheR |= justLast
			} else {
				c.cacheR &= offLast
			}
		case G_Bit:
			if bit == 1 {
				c.cacheG |= justLast
			} else {
				c.cacheG &= offLast
			}
		case B_Bit:
			if bit == 1 {
				c.cacheB |= justLast
			} else {
				c.cacheB &= offLast
			}
		}
		c.dirty = true
		c.cursor++
		channelIdx++
		if channelIdx >= int(c.bitCount) {
			channelIdx = 0
			pixelIdx++
		}
	}
	// Flush the last modified pixel so that any reader that follows immediately
	// (without an intervening seek) sees the correct image data.
	c.Flush()
	return nil
}
