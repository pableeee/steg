package steg

import (
	"crypto/md5"
	"image/draw"
	"io"

	"github.com/pableeee/steg/cipher"
	cur "github.com/pableeee/steg/cursors"
)

func Decode(img draw.Image, pass []byte) ([]byte, error) {
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

func Encode(m draw.Image, pass []byte, r io.Reader) error {
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
