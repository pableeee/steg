package steg

import (
	"encoding/binary"
	"hash"
	"io"

	cph "github.com/pableeee/steg/cipher"
	"github.com/pableeee/steg/cursors"
)

type writer struct {
	cursor   cursors.Cursor
	cipher   cph.StreamCipherBlock
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
		bit, err := w.cipher.EncryptBit(uint8(b))
		if err != nil {
			return err
		}
		_, err = w.cursor.WriteBit(bit)
		if err != nil {
			return err
		}
	}

	return nil
}

func (w *writer) seek(n uint) error {
	err := w.cursor.Seek(n)
	if err != nil {
		return err
	}
	err = w.cipher.Seek(n)
	if err != nil {
		return err
	}

	return nil
}

func (w *writer) Write(payload io.Reader) error {
	var payloadLength uint32
	buf := make([]byte, 1)

	// skip the first 4 bytes to later allow encoding the message length at the beggining.
	err := w.seek(4 * 8) // cursor moves by bit
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
	for _, b := range fileHash {
		if err := w.writeByte(b); err != nil {
			return err
		}
	}

	bs := make([]byte, 4)
	binary.LittleEndian.PutUint32(bs, payloadLength)

	// run the cursor to the begging to write the payload lenght.
	if err := w.seek(0); err != nil {
		return err
	}

	for _, b := range bs {
		if err := w.writeByte(b); err != nil {
			return err
		}
	}

	return nil
}
