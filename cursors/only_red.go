package cursors

import (
	"fmt"
	"image/color"
	"math"
)

type onlyRedCursor struct {
	img    ChangeableImage
	cursor uint
}

func NewOnlyRedCursor(img ChangeableImage) Cursor {
	return &onlyRedCursor{img: img}
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
