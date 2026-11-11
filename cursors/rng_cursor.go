package cursors

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"io"
)

// ErrUnsupportedImage is returned (wrapped) for carriers other than
// *image.RGBA and *image.NRGBA.
var ErrUnsupportedImage = errors.New("unsupported image type")

// CheckImage reports whether img is a carrier the cursor can address: an
// *image.RGBA or *image.NRGBA. Both store 8 bits per channel in their Pix
// slice, which the cursor reads and writes directly, so every value
// round-trips exactly. Other types would go through color conversion
// (premultiplication, luma, 16-bit truncation) that destroys hidden bits.
func CheckImage(img image.Image) error {
	_, _, _, err := pixBuffer(img)
	return err
}

// pixBuffer returns img's pixel bytes, its stride and its bounds. pix[0] is
// the pixel at bounds.Min (true for sub-images too), and each pixel is 4
// bytes in R, G, B, A order.
func pixBuffer(img image.Image) (pix []uint8, stride int, bounds image.Rectangle, err error) {
	switch m := img.(type) {
	case *image.RGBA:
		if m != nil {
			return m.Pix, m.Stride, m.Rect, nil
		}
	case *image.NRGBA:
		if m != nil {
			return m.Pix, m.Stride, m.Rect, nil
		}
	}
	return nil, 0, image.Rectangle{}, fmt.Errorf(
		"%w %T: only *image.RGBA and *image.NRGBA carriers are supported; convert it first",
		ErrUnsupportedImage, img)
}

// RNGCursor addresses the hidden bits of an image as a bit stream: stream bit
// b lives in pixel order[b / bitsPerPixel], in the used channels from R to B
// and, within a channel, from the most significant used bit to bit 0.
//
// It reads and writes the image's Pix bytes directly. Cursors that touch
// disjoint pixels of the same image may therefore run concurrently: they
// write distinct bytes, which is not a data race.
type RNGCursor struct {
	pix    []uint8
	stride int
	width  int

	cursor         int64
	bitMask        BitColor
	bitCount       uint
	bitsPerChannel int
	bitsPerPixel   int64
	chanOff        []int    // byte offset within a pixel of each used channel
	order          []uint32 // traversal order; nil means row-major
	maxBits        int64    // capacity in bits: pixels in the order × bitsPerPixel
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

// WithSharedPoints sets the traversal order: stream bit b lives in the pixel
// with index order[b / bitsPerPixel], where index i is the pixel at
// (i % width, i / width) relative to the image's Bounds().Min. The slice is
// read, never modified, so several cursors may share it. Without this option
// the cursor walks the image in row-major order.
func WithSharedPoints(order []uint32) Option {
	return func(c *RNGCursor) { c.order = order }
}

func WithBitsPerChannel(n int) Option {
	return func(c *RNGCursor) { c.bitsPerChannel = n }
}

// NewRNGCursor returns a cursor over img, which must be an *image.RGBA or
// *image.NRGBA (see CheckImage). The cursor uses the R channel plus any
// channels added by options, and 1 bit per channel unless WithBitsPerChannel
// says otherwise (1..8).
func NewRNGCursor(img draw.Image, options ...Option) (*RNGCursor, error) {
	pix, stride, b, err := pixBuffer(img)
	if err != nil {
		return nil, err
	}
	c := &RNGCursor{pix: pix, stride: stride, width: b.Dx(), bitMask: R_Bit, bitsPerChannel: 1}
	for _, opt := range options {
		opt(c)
	}
	if c.bitsPerChannel < 1 || c.bitsPerChannel > 8 {
		return nil, fmt.Errorf("cursors: bits per channel must be 1..8, got %d", c.bitsPerChannel)
	}

	numPixels := int64(0)
	if !b.Empty() {
		n, err := PixelCount(b.Dx(), b.Dy())
		if err != nil {
			return nil, err
		}
		numPixels = int64(n)
	}
	if c.order != nil {
		// Capacity follows the order, not the image bounds, so a slice shorter
		// than the image can never be indexed past its end. An order longer
		// than the image cannot be a permutation of its pixels.
		if int64(len(c.order)) > numPixels {
			return nil, fmt.Errorf("cursors: order has %d entries but the image has %d pixels",
				len(c.order), numPixels)
		}
		numPixels = int64(len(c.order))
	}

	for i, color := range Colors {
		if c.bitMask&color == color {
			c.bitCount++
			c.chanOff = append(c.chanOff, i) // R, G, B are bytes 0, 1, 2 of a pixel
		}
	}
	c.bitsPerPixel = int64(c.bitCount) * int64(c.bitsPerChannel)
	c.maxBits = numPixels * c.bitsPerPixel
	return c, nil
}

func (c *RNGCursor) BitCount() uint { return c.bitCount }

var _ Cursor = (*RNGCursor)(nil)

func (c *RNGCursor) validateBounds(n int64) bool {
	return n < c.maxBits
}

// pixelOffset returns the offset in pix of the first byte of the pixelIdx-th
// pixel of the traversal.
func (c *RNGCursor) pixelOffset(pixelIdx int64) int {
	idx := int(pixelIdx)
	if c.order != nil {
		idx = int(c.order[pixelIdx])
	}
	return (idx/c.width)*c.stride + (idx%c.width)*4
}

func (c *RNGCursor) Seek(n int64, whence int) (int64, error) {
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
		if n > 0 || (n*-1) > c.maxBits {
			return c.cursor, fmt.Errorf("illegal argument")
		}
		c.cursor = c.maxBits + n
	}

	return c.cursor, nil
}

// ReadByte reads 8 bits MSB-first from the cursor.
// Each pixel holds bitCount*bitsPerChannel bit slots, ordered by channel then
// by bit position within the channel (MSB-first within each channel's N bits).
func (c *RNGCursor) ReadByte() (byte, error) {
	pixelIdx := c.cursor / c.bitsPerPixel
	slotInPixel := int(c.cursor % c.bitsPerPixel)
	off := -1
	var out uint8

	for i := 7; i >= 0; i-- {
		if c.cursor >= c.maxBits {
			return 0, fmt.Errorf("out of bounds: %w", io.EOF)
		}
		if off < 0 {
			off = c.pixelOffset(pixelIdx)
		}
		channelIdx := slotInPixel / c.bitsPerChannel
		bitInChannel := (c.bitsPerChannel - 1) - (slotInPixel % c.bitsPerChannel)
		val := c.pix[off+c.chanOff[channelIdx]]
		out |= ((val >> bitInChannel) & 1) << i
		c.cursor++
		slotInPixel++
		if int64(slotInPixel) == c.bitsPerPixel {
			slotInPixel = 0
			pixelIdx++
			off = -1
		}
	}
	return out, nil
}

// WriteByte writes 8 bits MSB-first to the cursor, with the same slot layout
// as ReadByte. Bits are stored in the image immediately; there is nothing to
// flush.
func (c *RNGCursor) WriteByte(b byte) error {
	pixelIdx := c.cursor / c.bitsPerPixel
	slotInPixel := int(c.cursor % c.bitsPerPixel)
	off := -1

	for i := 7; i >= 0; i-- {
		if c.cursor >= c.maxBits {
			return fmt.Errorf("out of bounds: %w", io.EOF)
		}
		if off < 0 {
			off = c.pixelOffset(pixelIdx)
		}
		channelIdx := slotInPixel / c.bitsPerChannel
		bitInChannel := (c.bitsPerChannel - 1) - (slotInPixel % c.bitsPerChannel)
		p := &c.pix[off+c.chanOff[channelIdx]]
		mask := uint8(1) << bitInChannel
		if (b>>i)&1 == 1 {
			*p |= mask
		} else {
			*p &^= mask
		}
		c.cursor++
		slotInPixel++
		if int64(slotInPixel) == c.bitsPerPixel {
			slotInPixel = 0
			pixelIdx++
			off = -1
		}
	}
	return nil
}
