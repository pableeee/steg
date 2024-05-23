package steg

import (
	"image"
	"image/color"
	"io"

	"github.com/pableeee/steg/cursors"
)

const colorSize = 1

type ChangeableImage interface {
	image.Image
	Set(x, y int, c color.Color)
}

func Decode(img ChangeableImage, key []byte) ([]byte, error) {
	r := reader{cursor: cursors.NewOnlyRedCursor(img)}
	payload, err := r.Read()

	return payload, err
}

func Encode(m ChangeableImage, _ []byte, r io.Reader) error {
	w := writer{cursor: cursors.NewOnlyRedCursor(m)}

	return w.Write(r)
}
