package steg

import (
	"image"
	"image/color"
	"io"
)

const offLast = 0xfffe
const justLast = 0x0001
const colorSize = 1

type ChangeableImage interface {
	image.Image
	Set(x, y int, c color.Color)
}

func Decode(img image.Image, key []byte) ([]byte, error) {
	r := reader{img: img}
	payload, err := r.Read()

	return payload, err
}

func Encode(m ChangeableImage, _ []byte, r io.Reader) error {
	w := writer{img: m}

	return w.Write(r)
}
