package cursors

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"math"
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

type bitsPayload struct {
	x, y, n      int
	payload      []int
	responseChan chan response
}

type response struct {
	err error
	seq int
}

type RNGCursor struct {
	img         draw.Image
	cursor      int64
	bitMask     BitColor
	bitDepth    uint8
	useChannels []BitColor
	points      []image.Point
	rng         *rand.Rand

	// concurrent read/writes
	concurrency int
	cancel      context.CancelFunc
	workerChan  chan bitsPayload
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

func WithBitDepth(depth uint8) Option {
	return func(c *RNGCursor) {
		c.bitDepth = depth
	}
}

func WithConcurrency(concurrency int) Option {
	return func(c *RNGCursor) {
		c.concurrency = concurrency
		c.workerChan = make(chan bitsPayload, concurrency)
	}
}

func WithSeed(seed int64) Option {
	return func(c *RNGCursor) {
		c.rng = rand.New(rand.NewSource(seed))
	}
}

func NewRNGCursor(img draw.Image, options ...Option) *RNGCursor {
	c := &RNGCursor{
		img:         img,
		bitMask:     R_Bit,
		rng:         rand.New(rand.NewSource(0)),
		bitDepth:    1,
		concurrency: 1,
		workerChan:  make(chan bitsPayload),
	}

	for _, opt := range options {
		opt(c)
	}

	c.points = generateSequence(img.Bounds().Max.X, img.Bounds().Max.Y, c.rng)
	for _, color := range Colors {
		if c.bitMask&color == color {
			c.useChannels = append(c.useChannels, color)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel

	for i := 0; i < c.concurrency; i++ {
		go c.writeBitDepthPayload(ctx)
	}

	return c
}

var _ Cursor = (*RNGCursor)(nil)
var _ io.ReadWriteSeeker = (*RNGCursor)(nil)
var _ io.Closer = (*RNGCursor)(nil)

func (c *RNGCursor) validateBounds(n int64) bool {
	max := int64(c.img.Bounds().Max.X) * int64(c.img.Bounds().Max.Y) * int64(len(c.useChannels))

	return n < max
}

func (c *RNGCursor) tell() (x, y int, cl BitColor) {

	planeCursor := c.cursor / int64(len(c.useChannels))
	colorCursor := c.cursor % int64(len(c.useChannels))

	x = c.points[planeCursor].X
	y = c.points[planeCursor].Y

	cl = c.useChannels[colorCursor]

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
		max := int64(c.img.Bounds().Max.X) * int64(c.img.Bounds().Max.Y) * int64(len(c.useChannels))
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

	return uint(c.cursor), nil
}

func (c *RNGCursor) Read(payload []byte) (n int, err error) {
	buffer := make([]int, 0)
	pixels := math.Ceil(float64(8 / (int(c.bitDepth) * len(c.useChannels))))

	return 0, nil
}

func (c *RNGCursor) Write(payload []byte) (n int, err error) {
	buffer := make([]int, 0)
	responseChan := make(chan response)
	for i, bite := range payload {
		bits := byteToBits(bite)
		buffer = append(buffer, bits...)

		// at this point i would like to write a whole pixel worth of data.
		// that way i can parellize the writes.
		if len(buffer) < int(c.bitDepth)*len(c.useChannels) {
			// no enough bits to write a pixel yet
			continue
		}

		// keep only the bits that are part of the pixel
		currPayload := buffer[:int(c.bitDepth)*len(c.useChannels)]
		// remove the bits that were used
		buffer = buffer[int(c.bitDepth)*len(c.useChannels):]

		x, y, _ := c.tell()

		// send the payload to the worker to write on a pixel
		c.workerChan <- bitsPayload{
			x:            x,
			y:            y,
			n:            i,
			payload:      currPayload,
			responseChan: responseChan,
		}

		c.cursor += int64(c.bitDepth) * int64(len(c.useChannels))
	}

	for res, ok := <-responseChan; ok; res, ok = <-responseChan {
		if res.err != nil {
			return res.seq, err
		}
		n += int(c.bitDepth) * len(c.useChannels)
	}

	return n % 8, nil
}

func (c *RNGCursor) writeBitDepthPayload(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case pp := <-c.workerChan:
			channels := splitSlice(pp.payload, int(c.bitDepth))

			r, g, b, a := c.img.At(pp.x, pp.y).RGBA()
			for i, channel := range channels {
				p := uint32(0)
				for e, b := range channel {
					p = p | uint32(b)<<e
				}

				switch c.useChannels[i] {
				case R_Bit:
					r = p
				case G_Bit:
					g = p
				case B_Bit:
					b = p
				}
			}

			col := color.RGBA{uint8(r), uint8(g), uint8(b), uint8(a)}
			c.img.Set(pp.x, pp.y, col)
		}
	}
}

func (c *RNGCursor) Close() error {
	c.cancel()

	return nil
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
