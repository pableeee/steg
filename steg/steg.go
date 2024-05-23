package steg

import (
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
)

const offLast = 0xfffe
const justLast = 0x0001
const colorSize = 1

type ChangeableImage interface {
	image.Image
	Set(x, y int, c color.Color)
}

func Decode(img ChangeableImage, key []byte) ([]byte, error) {
	r := reader{img: img, cursor: &onlyRedCursor{
		img: img,
	}}
	payload, err := r.Read()

	return payload, err
}

func Encode(m ChangeableImage, _ []byte, r io.Reader) error {
	w := writer{img: m, cursor: &onlyRedCursor{
		img: m,
	}}

	return w.Write(r)
}

type Cursor interface {
	Tell() (x, y int)
	Seek(n uint) error
	WriteBit(bit uint8) (uint, error)
	ReadBit() (uint8, error)
}

type onlyRedCursor struct {
	img    ChangeableImage
	cursor uint
}

var _ Cursor = (*onlyRedCursor)(nil)

func (c *onlyRedCursor) validateBounds(n uint) bool {
	if n > uint(c.img.Bounds().Max.X)*uint(c.img.Bounds().Max.Y) {
		return false
	}

	return true
}

func (c *onlyRedCursor) Tell() (x, y int) {
	x = int(math.Mod(float64(c.cursor), float64(c.img.Bounds().Max.X)))
	y = int(math.Floor((float64(c.cursor) / float64(c.img.Bounds().Max.X))))

	return
}

func (c *onlyRedCursor) Seek(n uint) error {
	if !c.validateBounds(n) {
		return fmt.Errorf("out of bounds")
	}

	c.cursor = n

	return nil
}

func (c *onlyRedCursor) WriteBit(bit uint8) (uint, error) {
	if !c.validateBounds(c.cursor) {
		return c.cursor, fmt.Errorf("out of bounds")
	}
	x, y := c.Tell()

	r, g, b, a := c.img.At(x, y).RGBA()
	if bit == 1 {
		r = r | justLast
	} else {
		r = r & offLast
	}

	c.img.Set(x, y, color.RGBA{uint8(r), uint8(g), uint8(b), uint8(a)})

	c.cursor++

	return c.cursor, nil
}

func (c *onlyRedCursor) ReadBit() (uint8, error) {
	if !c.validateBounds(c.cursor) {
		return 0, fmt.Errorf("out of bounds")
	}
	x, y := c.Tell()
	r, _, _, _ := c.img.At(x, y).RGBA()
	c.cursor++

	bit := r & justLast

	return uint8(bit), nil

}
