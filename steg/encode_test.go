package steg_test

import (
	"bytes"
	"image"
	"testing"

	"github.com/pableeee/steg/steg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeRoundTrip(t *testing.T) {
	pass := []byte("testpass")
	payload := []byte("this is a secret")

	// Create a sufficiently large image
	m := image.NewRGBA(image.Rect(0, 0, 100, 50))

	// Encode the payload
	err := steg.Encode(m, pass, bytes.NewReader(payload))
	require.NoError(t, err)

	// Decode the payload
	readData, err := steg.Decode(m, pass)
	require.NoError(t, err)
	assert.Equal(t, payload, readData)
}

func TestEncodeWithSmallImage(t *testing.T) {
	pass := []byte("testpass")
	payload := []byte("this is a secret")

	// Create a small image that might not hold the payload
	m := image.NewRGBA(image.Rect(0, 0, 1, 1))

	// Encoding should fail due to insufficient space
	err := steg.Encode(m, pass, bytes.NewReader(payload))
	assert.Error(t, err)
}
