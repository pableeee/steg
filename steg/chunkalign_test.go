package steg_test

import (
	"bytes"
	"image"
	"image/color"
	"math/rand"
	"testing"

	"github.com/pableeee/steg/steg"
	"github.com/stretchr/testify/require"
)

// TestParallelMultiChunkRoundTrip fills a carrier to capacity so the padded
// payload spans many worker chunks. Chunk boundaries must land on pixel
// boundaries; if they do not, two workers read-modify-write the same pixel and
// one worker's bits are lost, which surfaces here as a MAC failure.
//
// The default configuration (3 channels, 1 bit/channel) is the interesting one:
// the payload region starts at bit 160, and 160 mod 3 = 1, so a naive
// byte-aligned chunking scheme puts every boundary mid-pixel.
func TestParallelMultiChunkRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		bitsPerChannel, chans int
	}{
		{"3ch_1bpc", 1, 3},
		{"2ch_1bpc", 1, 2},
		{"1ch_1bpc", 1, 1},
		{"3ch_2bpc", 2, 3},
		{"3ch_4bpc", 4, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pass := []byte("testpass")
			m := newNoisyImage(512, 512)

			capacity := steg.CapacityBytes(m, tc.bitsPerChannel, tc.chans)
			require.Greater(t, capacity, 0)

			payload := make([]byte, capacity)
			for i := range payload {
				payload[i] = byte(i * 7)
			}

			// Repeat: the lost update is a narrow interleaving window, so a
			// single pass can pass by luck.
			for i := 0; i < 10; i++ {
				dst := newNoisyImage(512, 512)
				err := steg.EncodeParallel(dst, pass, bytes.NewReader(payload), tc.bitsPerChannel, tc.chans)
				require.NoError(t, err)

				got, err := steg.DecodeParallel(dst, pass, tc.bitsPerChannel, tc.chans)
				require.NoError(t, err, "iteration %d", i)
				require.Equal(t, payload, got, "iteration %d", i)
			}
		})
	}
}

func newNoisyImage(w, h int) *image.RGBA {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	rng := rand.New(rand.NewSource(42))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.Set(x, y, color.RGBA{
				uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255,
			})
		}
	}
	return m
}
