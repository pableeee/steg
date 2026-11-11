package steg_test

import (
	"bytes"
	"image"
	"testing"

	"github.com/pableeee/steg/steg"
	"github.com/stretchr/testify/require"
)

// TestSubImageRoundTrip covers carriers whose bounds do not start at (0,0).
// The pixel order indexes [0,Dx)×[0,Dy), and the cursor maps an index to an
// offset in the sub-image's Pix slice, which starts at Bounds().Min and has the
// parent's Stride; getting either wrong addresses the wrong pixels.
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

// subImageCases are carriers with a non-zero origin. The large case spans many
// worker chunks (chunk size is lcmBytes(8, channels*bitsPerChannel)*1024 bytes,
// and the padded block for these payloads is a 32 KiB bucket, still many chunks).
var subImageCases = []struct {
	name                     string
	fullW, fullH             int
	rect                     image.Rectangle
	bitsPerChannel, channels int
	payloadLen               int
}{
	{"small_1bit_3ch", 256, 256, image.Rect(64, 64, 192, 192), 1, 3, 64},
	// 320×363 px × 3 bits = 43560 B capacity ≈ 14 chunks of 3072 B.
	{"multichunk_1bit_3ch", 512, 512, image.Rect(100, 37, 420, 400), 1, 3, 20000},
	// 300×250 px × 4 bits = 37500 B capacity ≈ 9 chunks of 4096 B.
	{"multichunk_2bit_2ch", 400, 400, image.Rect(50, 120, 350, 370), 2, 2, 30000},
}

func subImagePayload(n int) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = byte(i*31 + 7)
	}
	return p
}

// TestSubImageParallelRoundTrip checks that the parallel and sequential paths
// address the same pixels on a carrier with a non-zero origin, in every
// encode/decode combination.
func TestSubImageParallelRoundTrip(t *testing.T) {
	pass := []byte("testpass")
	for _, tc := range subImageCases {
		t.Run(tc.name, func(t *testing.T) {
			payload := subImagePayload(tc.payloadLen)
			newSub := func() *image.RGBA {
				sub := newNoisyImage(tc.fullW, tc.fullH).SubImage(tc.rect).(*image.RGBA)
				require.Equal(t, tc.rect.Min, sub.Bounds().Min)
				return sub
			}

			t.Run("EncodeParallel_DecodeParallel", func(t *testing.T) {
				sub := newSub()
				require.NoError(t, steg.EncodeParallel(sub, pass, bytes.NewReader(payload), tc.bitsPerChannel, tc.channels))
				got, err := steg.DecodeParallel(sub, pass, tc.bitsPerChannel, tc.channels)
				require.NoError(t, err)
				require.Equal(t, payload, got)
			})

			t.Run("EncodeParallel_Decode", func(t *testing.T) {
				sub := newSub()
				require.NoError(t, steg.EncodeParallel(sub, pass, bytes.NewReader(payload), tc.bitsPerChannel, tc.channels))
				got, err := steg.Decode(sub, pass, tc.bitsPerChannel, tc.channels)
				require.NoError(t, err)
				require.Equal(t, payload, got)
			})

			t.Run("Encode_DecodeParallel", func(t *testing.T) {
				sub := newSub()
				require.NoError(t, steg.Encode(sub, pass, bytes.NewReader(payload), tc.bitsPerChannel, tc.channels))
				got, err := steg.DecodeParallel(sub, pass, tc.bitsPerChannel, tc.channels)
				require.NoError(t, err)
				require.Equal(t, payload, got)
			})
		})
	}
}

// TestSubImageParallelWritesStayInBounds verifies EncodeParallel only touches
// pixels inside the carrier's bounds. The cursor writes the shared Pix slice
// directly, without the clipping (*image.RGBA).Set applies, so a cursor that
// addressed pixels outside its sub-image would show up here as modified
// pixels in the parent.
func TestSubImageParallelWritesStayInBounds(t *testing.T) {
	for _, tc := range subImageCases {
		t.Run(tc.name, func(t *testing.T) {
			full := newNoisyImage(tc.fullW, tc.fullH)
			before := make([]byte, len(full.Pix))
			copy(before, full.Pix)

			sub := full.SubImage(tc.rect).(*image.RGBA)
			payload := subImagePayload(tc.payloadLen)
			require.NoError(t, steg.EncodeParallel(sub, []byte("testpass"), bytes.NewReader(payload), tc.bitsPerChannel, tc.channels))

			changedInside := false
			for y := full.Bounds().Min.Y; y < full.Bounds().Max.Y; y++ {
				for x := full.Bounds().Min.X; x < full.Bounds().Max.X; x++ {
					off := full.PixOffset(x, y)
					same := bytes.Equal(before[off:off+4], full.Pix[off:off+4])
					if image.Pt(x, y).In(tc.rect) {
						changedInside = changedInside || !same
						continue
					}
					require.True(t, same, "pixel (%d,%d) outside sub-image bounds was modified", x, y)
				}
			}
			require.True(t, changedInside, "EncodeParallel modified no pixel inside the sub-image")
		})
	}
}
