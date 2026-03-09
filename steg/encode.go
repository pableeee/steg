package steg

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"image/draw"
	"io"

	"github.com/pableeee/steg/cipher"
	"github.com/pableeee/steg/cursors"
	"github.com/pableeee/steg/steg/container"
)

func Encode(m draw.Image, pass []byte, r io.Reader, bitsPerChannel, channels int) error {
	seed, err := deriveSeed(pass)
	if err != nil {
		return err
	}

	realPayload, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	padded, err := buildPaddedPayload(m, realPayload, bitsPerChannel, channels)
	if err != nil {
		return err
	}

	cur := cursors.NewRNGCursor(m, cursorOptions(seed, bitsPerChannel, channels)...)

	// Generate a random per-encode salt and write it in plaintext to image bytes
	// 0–15 (bits 0–127). The salt does not need to be secret; its purpose is
	// uniqueness so that each encode derives independent main keys.
	var randomSalt [16]byte
	if _, err = rand.Read(randomSalt[:]); err != nil {
		return err
	}
	saltAdapter := cursors.CursorAdapter(cur)
	if _, err = saltAdapter.Write(randomSalt[:]); err != nil {
		return err
	}

	// Derive main keys from the random salt; seek cursor and cipher to bit 128.
	encKey, macKey, payloadNonce, err := deriveMainKeys(pass, randomSalt[:])
	if err != nil {
		return err
	}
	payloadCipher, err := cipher.NewCipher(payloadNonce, encKey)
	if err != nil {
		return err
	}
	payloadCM := cursors.CipherMiddleware(cur, payloadCipher)
	if _, err = payloadCM.Seek(128, io.SeekStart); err != nil {
		return err
	}

	adapter := cursors.CursorAdapter(payloadCM)
	mac := hmac.New(sha256.New, macKey)
	if err = container.WritePayload(adapter, bytes.NewReader(padded), mac); err != nil {
		return err
	}
	cur.Flush()
	return nil
}
