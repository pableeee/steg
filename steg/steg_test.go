package steg

import (
	"bytes"
	"image"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeDecode(t *testing.T) {
	pass := []byte("test")
	payload := []byte("test payload")

	t.Run("should be able to retrieve the payload from the encoded image", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 100, 50))
		err := Encode(img, pass, bytes.NewReader(payload))
		require.NoError(t, err)

		decoded, err := Decode(img, pass)
		require.NoError(t, err)

		assert.Equal(t, payload, decoded)
	})

	t.Run("should fail to retrieve the payload on a wrong password", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 100, 50))
		err := Encode(img, pass, bytes.NewReader(payload))
		require.NoError(t, err)

		_, err = Decode(img, []byte("wrong pass"))
		require.Error(t, err)
	})

	t.Run("should fail to write the payload on a small image", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 1, 5))
		err := Encode(img, pass, bytes.NewReader(payload))
		require.Error(t, err)
	})
}
