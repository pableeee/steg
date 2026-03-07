package steg

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"image/draw"
	"io"

	"github.com/pableeee/steg/cipher"
	"github.com/pableeee/steg/cursors"
	"github.com/pableeee/steg/steg/container"
)

func Encode(m draw.Image, pass []byte, r io.Reader, bitsPerChannel, channels int) error {
	seed, encKey, macKey, kdfNonce, err := deriveKeys(pass)
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

	// Generate a random per-encode nonce and write it encrypted to image bytes
	// 0–3. The bootstrap cipher (keyed with the KDF-derived nonce) encrypts it
	// so no plaintext appears on the image, yet each encode uses a unique
	// keystream regardless of password reuse.
	var rawNonce [4]byte
	if _, err = rand.Read(rawNonce[:]); err != nil {
		return err
	}
	bootstrapCipher, err := cipher.NewCipher(kdfNonce, encKey)
	if err != nil {
		return err
	}
	bootstrapAdapter := cursors.CursorAdapter(cursors.CipherMiddleware(cur, bootstrapCipher))
	if _, err = bootstrapAdapter.Write(rawNonce[:]); err != nil {
		return err
	}

	// Init the payload cipher with the random nonce; seek cursor and cipher
	// past the 4 encrypted nonce bytes (bit 32).
	randomNonce := binary.BigEndian.Uint32(rawNonce[:])
	payloadCipher, err := cipher.NewCipher(randomNonce, encKey)
	if err != nil {
		return err
	}
	payloadCM := cursors.CipherMiddleware(cur, payloadCipher)
	if _, err = payloadCM.Seek(32, io.SeekStart); err != nil {
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
