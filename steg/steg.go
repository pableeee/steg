package steg

import (
	"image"
	"image/color"
	"io"

	"github.com/pableeee/steg/cipher"
	"github.com/pableeee/steg/cursors"
)

type ChangeableImage interface {
	image.Image
	Set(x, y int, c color.Color)
}

func Decode(img ChangeableImage, pass []byte) ([]byte, error) {
	r := reader{cursor: cursors.NewOnlyRedCursor(img), cipher: cipher.NewCipher(0, pass)}
	payload, err := r.Read()

	return payload, err
}

func Encode(m ChangeableImage, pass []byte, r io.Reader) error {
	w := writer{cursor: cursors.NewOnlyRedCursor(m),
		cipher: cipher.NewCipher(0, pass),
	}

	return w.Write(r)
}
