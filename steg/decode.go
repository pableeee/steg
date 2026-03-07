package steg

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"image/draw"
	"io"

	"github.com/pableeee/steg/cipher"
	"github.com/pableeee/steg/cursors"
	"github.com/pableeee/steg/steg/container"
)

func Decode(m draw.Image, pass []byte, bitsPerChannel, channels int) ([]byte, error) {
	seed, encKey, macKey, kdfNonce, err := deriveKeys(pass)
	if err != nil {
		return nil, err
	}

	cur := cursors.NewRNGCursor(m, cursorOptions(seed, bitsPerChannel, channels)...)

	// Decrypt the encrypted nonce from image bytes 0–3 using the bootstrap cipher.
	bootstrapCipher, err := cipher.NewCipher(kdfNonce, encKey)
	if err != nil {
		return nil, err
	}
	bootstrapAdapter := cursors.CursorAdapter(cursors.CipherMiddleware(cur, bootstrapCipher))
	var rawNonce [4]byte
	if _, err = io.ReadFull(bootstrapAdapter, rawNonce[:]); err != nil {
		return nil, err
	}

	// Reconstruct the payload cipher with the recovered random nonce; seek
	// cursor and cipher past the 4 encrypted nonce bytes (bit 32).
	randomNonce := binary.BigEndian.Uint32(rawNonce[:])
	payloadCipher, err := cipher.NewCipher(randomNonce, encKey)
	if err != nil {
		return nil, err
	}
	payloadCM := cursors.CipherMiddleware(cur, payloadCipher)
	if _, err = payloadCM.Seek(32, io.SeekStart); err != nil {
		return nil, err
	}

	adapter := cursors.CursorAdapter(payloadCM)
	mac := hmac.New(sha256.New, macKey)
	padded, err := container.ReadPayload(adapter, mac)
	if err != nil {
		return nil, err
	}
	return extractRealPayload(padded)
}
