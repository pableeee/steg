package steg_test

import (
	"bytes"
	"image"
	"testing"

	"github.com/pableeee/steg/steg"
	"github.com/stretchr/testify/require"
)

// TestSubImageRoundTrip covers carriers whose bounds do not start at (0,0).
// The cursor generates its pixel sequence over [0,Dx)×[0,Dy) and offsets by
// Bounds().Min on access; without that offset it addresses coordinates outside
// the sub-image, where At returns the zero color and Set is a no-op.
func TestSubImageRoundTrip(t *testing.T) {
	full := newNoisyImage(256, 256)
	sub, ok := full.SubImage(image.Rect(64, 64, 192, 192)).(*image.RGBA)
	require.True(t, ok)
	require.Equal(t, image.Pt(64, 64), sub.Bounds().Min)

	pass := []byte("testpass")
	payload := []byte("payload hidden in a sub-image with a non-zero origin")

	require.NoError(t, steg.Encode(sub, pass, bytes.NewReader(payload), 1, 3))

	got, err := steg.Decode(sub, pass, 1, 3)
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

// TestSubImageWritesStayInBounds verifies the encoder only touches pixels
// inside the sub-image's bounds, leaving the surrounding region untouched.
func TestSubImageWritesStayInBounds(t *testing.T) {
	full := newNoisyImage(256, 256)
	before := make([]byte, len(full.Pix))
	copy(before, full.Pix)

	sub := full.SubImage(image.Rect(64, 64, 192, 192)).(*image.RGBA)
	payload := []byte("bounded write")
	require.NoError(t, steg.Encode(sub, []byte("testpass"), bytes.NewReader(payload), 1, 3))

	r := sub.Bounds()
	for y := full.Bounds().Min.Y; y < full.Bounds().Max.Y; y++ {
		for x := full.Bounds().Min.X; x < full.Bounds().Max.X; x++ {
			if image.Pt(x, y).In(r) {
				continue
			}
			off := full.PixOffset(x, y)
			require.Equal(t, before[off:off+4], full.Pix[off:off+4],
				"pixel (%d,%d) outside sub-image bounds was modified", x, y)
		}
	}
}
