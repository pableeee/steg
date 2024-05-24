package cursors

import (
	"image"
	"image/color"
)

const offLast = 0xfffe
const justLast = 0x0001

type ChangeableImage interface {
	image.Image
	Set(x, y int, c color.Color)
}

type Cursor interface {
	Seek(n uint) error
	WriteBit(bit uint8) (uint, error)
	ReadBit() (uint8, error)
}
