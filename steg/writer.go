package steg

import (
	"encoding/binary"
	"image/color"
	"io"
	"math"
)

type writer struct {
	img    ChangeableImage
	cursor int
}

func newWriter(img ChangeableImage) *writer {
	return &writer{
		img:    img,
		cursor: 0,
	}
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
		x := int(math.Mod(float64(w.cursor), float64(w.img.Bounds().Max.X)))
		y := int(math.Floor((float64(w.cursor) / float64(w.img.Bounds().Max.X))))
		w.cursor++
		c := w.img.At(x, y)
		r, g, b, a := c.RGBA()

		if bit == 1 {
			r = r | justLast
		} else {
			r = r & offLast
		}

		w.img.Set(x, y, color.RGBA{uint8(r), uint8(g), uint8(b), uint8(a)})
	}

	return nil
}

func (w *writer) Write(payload io.Reader) error {
	var payloadLength uint32
	buf := make([]byte, 1)

	// skip the first 4 bytes to later allow encoding the message length at the beggining.
	w.cursor = 4 * 8 // cursor moves by bit

	for n, err := payload.Read(buf); n == 1 && err == nil; payloadLength++ {
		if err = w.writeByte(buf[0]); err != nil {
			return err
		}
		n, err = payload.Read(buf)
	}

	bs := make([]byte, 4)
	binary.LittleEndian.PutUint32(bs, payloadLength)

	// run the cursor to the begging to write the payload lenght.
	w.cursor = 0
	for _, b := range bs {
		if err := w.writeByte(b); err != nil {
			return err
		}
	}

	return nil
}
