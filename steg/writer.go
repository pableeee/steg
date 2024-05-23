package steg

import (
	"encoding/binary"
	"io"

	"github.com/pableeee/steg/cursors"
)

type writer struct {
	cursor cursors.Cursor
}

func byteToBits(b byte) []int {
	var bits []int
	for i := 7; i >= 0; i-- { // Extract bits from most significant to least significant
		bit := (b >> i) & 1
		bits = append(bits, int(bit))
	}

	return bits
}

func (w *writer) writeByte(p byte) error {
	bits := byteToBits(p)
	for _, bit := range bits {
		_, err := w.cursor.WriteBit(uint8(bit))
		if err != nil {
			return err
		}
	}

	return nil
}

func (w *writer) Write(payload io.Reader) error {
	var payloadLength uint32
	buf := make([]byte, 1)

	// skip the first 4 bytes to later allow encoding the message length at the beggining.
	err := w.cursor.Seek(4 * 8) // cursor moves by bit
	if err != nil {
		return err
	}

	for n, err := payload.Read(buf); n == 1 && err == nil; payloadLength++ {
		if err = w.writeByte(buf[0]); err != nil {
			return err
		}
		n, err = payload.Read(buf)
	}

	bs := make([]byte, 4)
	binary.LittleEndian.PutUint32(bs, payloadLength)

	// run the cursor to the begging to write the payload lenght.
	if err := w.cursor.Seek(0); err != nil {
		return err
	}

	for _, b := range bs {
		if err := w.writeByte(b); err != nil {
			return err
		}
	}

	return nil
}
