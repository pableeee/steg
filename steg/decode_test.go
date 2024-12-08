package steg_test

import (
	"bytes"
	"image"
	"testing"

	"github.com/pableeee/steg/steg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeRoundTrip(t *testing.T) {
	pass := []byte("decode-test-pass")
	payload := []byte("another secret")

	m := image.NewRGBA(image.Rect(0, 0, 100, 50))
	err := steg.Encode(m, pass, bytes.NewReader(payload))
	require.NoError(t, err)

	readData, err := steg.Decode(m, pass)
	require.NoError(t, err)
	assert.Equal(t, payload, readData)
}

func TestDecodeWrongPassword(t *testing.T) {
	pass := []byte("correct-pass")
	payload := []byte("hidden message")

	m := image.NewRGBA(image.Rect(0, 0, 100, 50))
	err := steg.Encode(m, pass, bytes.NewReader(payload))
	require.NoError(t, err)

	// Try decoding with a wrong password
	_, err = steg.Decode(m, []byte("wrong-pass"))
	assert.Error(t, err)
}

// TODO: test corrupted data, you can encode first, then manually alter the image pixels or bits.
// This is advanced and depends on your `cursors` implementation. A simpler test is just to trust
// that `Decode` will fail if the checksum doesn't match after we've tested that thoroughly in container_test.go.
