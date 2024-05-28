package cursors

type Cursor interface {
	Seek(offset int64, whence int) (int64, error)
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
