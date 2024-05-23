package steg

import (
	"encoding/binary"
	"fmt"
	"image"
	"math"
)

type reader struct {
	img    image.Image
	cursor int
}

func (t *reader) readByte() (byte, error) {
	var nBits = 8
	var res uint8
	for i := 0; i < nBits; i++ {
		x := int(math.Mod(float64(t.cursor), float64(t.img.Bounds().Max.X)))
		y := int(math.Floor((float64(t.cursor) / float64(t.img.Bounds().Max.X))))
		t.cursor++
		r, _, _, _ := t.img.At(x, y).RGBA()

		bit := r & justLast
		res |= uint8(bit << (nBits - i - 1))

	}

	return res, nil
}

func (t *reader) Read() ([]byte, error) {
	payloadSize := make([]byte, 4)
	for i := 0; i < 4; i++ {
		b, err := t.readByte()
		if err != nil {
			return nil, fmt.Errorf("unable to read payload size %w", err)
		}
		payloadSize[i] = b
	}

	// binary.LittleEndian.PutUint32(bs, uint32(len(payload)))
	payload := make([]byte, binary.LittleEndian.Uint32(payloadSize))
	for i := 0; i < len(payload); i++ {
		b, err := t.readByte()
		if err != nil {
			return nil, fmt.Errorf("unable to read payload %w", err)
		}
		payload[i] = b
	}

	return payload, nil
}
