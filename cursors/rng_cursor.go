package cursors

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"math/rand"
	"sync"
)

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
	img             draw.Image
	cursor          int64
	bitMask         BitColor
	bitCount        uint
	bitsPerChannel  int
	useBits         []BitColor
	points          []image.Point
	rng             *rand.Rand
	maxBits         int64 // pre-computed capacity in bits

	// imgMu, when non-nil, is locked around every img.At() and img.Set() call.
	// Set via WithImageMutex to eliminate data races when multiple cursors share
	// the same draw.Image (e.g. parallel encode workers).
	imgMu *sync.Mutex

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

func WithBitsPerChannel(n int) Option {
	return func(c *RNGCursor) { c.bitsPerChannel = n }
}

// WithImageMutex sets a shared mutex that will be locked around every img.At()
// and img.Set() call. Pass the same *sync.Mutex to all cursors that share an
// image to eliminate data races in parallel encode/decode scenarios.
func WithImageMutex(mu *sync.Mutex) Option {
	return func(c *RNGCursor) { c.imgMu = mu }
}

func NewRNGCursor(img draw.Image, options ...Option) *RNGCursor {
	c := &RNGCursor{img: img, bitMask: R_Bit, bitsPerChannel: 1, rng: rand.New(rand.NewSource(0))}
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
	c.maxBits = int64(img.Bounds().Max.X) * int64(img.Bounds().Max.Y) * int64(c.bitCount) * int64(c.bitsPerChannel)
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
		px := color.RGBA{uint8(c.cacheR), uint8(c.cacheG), uint8(c.cacheB), uint8(c.cacheA)}
		if c.imgMu != nil {
			c.imgMu.Lock()
			c.img.Set(c.cacheX, c.cacheY, px)
			c.imgMu.Unlock()
		} else {
			c.img.Set(c.cacheX, c.cacheY, px)
		}
		c.dirty = false
	}
}

// loadPixel flushes the current dirty pixel (if any) then loads pixelIdx into cache.
func (c *RNGCursor) loadPixel(pixelIdx int64) {
	c.Flush()
	pt := c.points[pixelIdx]
	var r, g, b, a uint32
	if c.imgMu != nil {
		c.imgMu.Lock()
		r, g, b, a = c.img.At(pt.X, pt.Y).RGBA()
		c.imgMu.Unlock()
	} else {
		r, g, b, a = c.img.At(pt.X, pt.Y).RGBA()
	}
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
// Slot arithmetic accounts for bitsPerChannel: each pixel holds
// bitCount*bitsPerChannel bit slots, ordered by channel then by bit
// position within the channel (MSB-first within each channel's N bits).
func (c *RNGCursor) ReadByte() (uint8, error) {
	bitsPerPixel := int64(c.bitCount) * int64(c.bitsPerChannel)
	pixelIdx := c.cursor / bitsPerPixel
	slotInPixel := int(c.cursor % bitsPerPixel)
	var out uint8

	for i := 7; i >= 0; i-- {
		if c.cursor >= c.maxBits {
			return 0, fmt.Errorf("out of bounds: %w", io.EOF)
		}
		if !c.pixelCached || pixelIdx != c.cacheIdx {
			c.loadPixel(pixelIdx)
		}
		channelIdx := slotInPixel / c.bitsPerChannel
		bitInChannel := (c.bitsPerChannel - 1) - (slotInPixel % c.bitsPerChannel)
		var val uint32
		switch c.useBits[channelIdx] {
		case R_Bit:
			val = c.cacheR
		case G_Bit:
			val = c.cacheG
		case B_Bit:
			val = c.cacheB
		}
		out |= uint8((val>>bitInChannel)&1) << i
		c.cursor++
		slotInPixel++
		if slotInPixel >= int(bitsPerPixel) {
			slotInPixel = 0
			pixelIdx++
		}
	}
	return out, nil
}

// WriteByte writes 8 bits MSB-first to the cursor.
// Uses write-back caching: img.Set() is deferred until the cursor moves to the
// next pixel or Flush() is called — one Set per pixel regardless of bit count.
// Slot arithmetic accounts for bitsPerChannel: each pixel holds
// bitCount*bitsPerChannel bit slots, ordered by channel then by bit
// position within the channel (MSB-first within each channel's N bits).
func (c *RNGCursor) WriteByte(b uint8) error {
	bitsPerPixel := int64(c.bitCount) * int64(c.bitsPerChannel)
	pixelIdx := c.cursor / bitsPerPixel
	slotInPixel := int(c.cursor % bitsPerPixel)

	for i := 7; i >= 0; i-- {
		if c.cursor >= c.maxBits {
			return fmt.Errorf("out of bounds: %w", io.EOF)
		}
		if !c.pixelCached || pixelIdx != c.cacheIdx {
			c.loadPixel(pixelIdx) // flushes previous dirty pixel
		}
		channelIdx := slotInPixel / c.bitsPerChannel
		bitInChannel := (c.bitsPerChannel - 1) - (slotInPixel % c.bitsPerChannel)
		bit := (b >> i) & 1
		mask := uint32(1) << bitInChannel
		switch c.useBits[channelIdx] {
		case R_Bit:
			if bit == 1 {
				c.cacheR |= mask
			} else {
				c.cacheR &^= mask
			}
		case G_Bit:
			if bit == 1 {
				c.cacheG |= mask
			} else {
				c.cacheG &^= mask
			}
		case B_Bit:
			if bit == 1 {
				c.cacheB |= mask
			} else {
				c.cacheB &^= mask
			}
		}
		c.dirty = true
		c.cursor++
		slotInPixel++
		if slotInPixel >= int(bitsPerPixel) {
			slotInPixel = 0
			pixelIdx++
		}
	}
	// Flush the last modified pixel so that any reader that follows immediately
	// (without an intervening seek) sees the correct image data.
	c.Flush()
	return nil
}
