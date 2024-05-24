package cursors

import (
	"image"
	"image/color"
)

type ChangeableImage interface {
	image.Image
	Set(x, y int, c color.Color)
}

type Cursor interface {
	Seek(n uint) error
	WriteBit(bit uint8) (uint, error)
	ReadBit() (uint8, error)
}

type BitColor uint

const (
	NONE  BitColor = iota
	R_Bit BitColor = 0x1
	G_Bit BitColor = 0x2
	B_Bit BitColor = 0x4
)

var (
	Colors = []BitColor{R_Bit, G_Bit, B_Bit}
)
