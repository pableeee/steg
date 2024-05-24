package steg

import (
	"crypto/md5"
	"image"
	"image/color"
	"io"

	"github.com/pableeee/steg/cipher"
	"github.com/pableeee/steg/cursors"
	"github.com/pableeee/steg/cursors/rng"
)

type ChangeableImage interface {
	image.Image
	Set(x, y int, c color.Color)
}

func Decode(img ChangeableImage, pass []byte) ([]byte, error) {
	h := md5.New()
	seed, err := h.Write(pass)
	if err != nil {
		return nil, err
	}

	cursor := rng.NewRNGCursor(img,
		rng.UseGreenBit(),
		rng.UseRedBit(),
		rng.UseBlueBit(),
		rng.WithSeed(int64(seed)),
	)

	cursor = cursors.CipherMiddleware(cursor, cipher.NewCipher(0, pass))
	r := reader{
		hashFunc: md5.New(),
		cursor:   cursor,
	}
	payload, err := r.Read()

	return payload, err
}

func Encode(m ChangeableImage, pass []byte, r io.Reader) error {
	h := md5.New()
	seed, err := h.Write(pass)
	if err != nil {
		return err
	}

	cursor := rng.NewRNGCursor(m,
		rng.UseGreenBit(),
		rng.UseRedBit(),
		rng.UseBlueBit(),
		rng.WithSeed(int64(seed)),
	)

	cursor = cursors.CipherMiddleware(cursor, cipher.NewCipher(0, pass))

	w := writer{
		hashFunc: md5.New(),
		cursor:   cursor,
	}

	return w.Write(r)
}
