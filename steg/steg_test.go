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
		err := Encode(img, pass, bytes.NewReader(payload), 1)
		require.NoError(t, err)

		decoded, err := Decode(img, pass, 1)
		require.NoError(t, err)

		assert.Equal(t, payload, decoded)
	})

	t.Run("should fail to retrieve the payload on a wrong password", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 100, 50))
		err := Encode(img, pass, bytes.NewReader(payload), 1)
		require.NoError(t, err)

		_, err = Decode(img, []byte("wrong pass"), 1)
		require.Error(t, err)
	})

	t.Run("should fail to write the payload on a small image", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 1, 5))
		err := Encode(img, pass, bytes.NewReader(payload), 1)
		require.Error(t, err)
	})
}

func TestMultiBitRoundTrip(t *testing.T) {
	pass := []byte("multibit-pass")
	// 100×100 image: capacity = 100*100*3*N bits / 8 bytes - 40 overhead
	// At N=1: 3750-40=3710 bytes. 12 bytes fits easily for all N.
	payload := []byte("hello, world!")

	for _, n := range []int{1, 2, 4, 8} {
		n := n
		t.Run("roundtrip", func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, 100, 100))
			err := Encode(img, pass, bytes.NewReader(payload), n)
			require.NoError(t, err)

			got, err := Decode(img, pass, n)
			require.NoError(t, err)
			assert.Equal(t, payload, got)
		})
	}

	t.Run("wrong bitsPerChannel is detectable", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 100, 100))
		err := Encode(img, pass, bytes.NewReader(payload), 2)
		require.NoError(t, err)

		_, err = Decode(img, pass, 1)
		require.Error(t, err, "decoding with wrong bitsPerChannel should fail MAC verification")
	})
}
