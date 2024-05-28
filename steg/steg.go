package steg

import (
	"crypto/md5"
	"image"
	"image/color"
	"io"

	"github.com/pableeee/steg/cipher"
	cur "github.com/pableeee/steg/cursors"
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

	cursor := cur.NewRNGCursor(img,
		cur.UseGreenBit(),
		cur.UseBlueBit(),
		cur.WithSeed(int64(seed)),
	)

	middle := cur.CipherMiddleware(cursor, cipher.NewCipher(0, pass))
	r := reader{
		hashFunc: md5.New(),
		cursor:   cur.CursorAdapter(middle),
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

	cursor := cur.NewRNGCursor(m,
		cur.UseGreenBit(),
		cur.UseBlueBit(),
		cur.WithSeed(int64(seed)),
	)

	middle := cur.CipherMiddleware(cursor, cipher.NewCipher(0, pass))
	w := writer{
		hashFunc: md5.New(),
		cursor:   cur.CursorAdapter(middle),
	}

	return w.Write(r)
}
