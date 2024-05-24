package steg

import (
	"encoding/binary"
	"hash"
	"io"

	"github.com/pableeee/steg/cursors"
)

type writer struct {
	cursor   cursors.Cursor
	hashFunc hash.Hash
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
	for _, b := range bits {
		_, err := w.cursor.WriteBit(uint8(b))
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
		w.hashFunc.Write(buf)
		if err = w.writeByte(buf[0]); err != nil {
			return err
		}
		n, err = payload.Read(buf)
	}

	fileHash := w.hashFunc.Sum(nil)
	// writes a checksum, to enable validation when decoding.
	for _, b := range fileHash {
		if err := w.writeByte(b); err != nil {
			return err
		}
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
