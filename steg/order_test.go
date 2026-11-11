package steg

import (
	"bytes"
	"image"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pableeee/steg/cursors"
)

func noisyRGBA(w, h int, seed int64) *image.RGBA {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	rand.New(rand.NewSource(seed)).Read(m.Pix)
	return m
}

func cloneRGBA(m *image.RGBA) *image.RGBA {
	c := image.NewRGBA(m.Rect)
	copy(c.Pix, m.Pix)
	return c
}

// TestOrderSaltPrefixSharedAcrossEncodes encodes the same payload with the same
// password into two identical carriers. Both must put the salt in the same
// public pixels, but each encode draws a fresh salt, so the keys and therefore
// the rest of the order must differ.
func TestOrderSaltPrefixSharedAcrossEncodes(t *testing.T) {
	const bpc, ch = 1, 3
	pass := []byte("same password")
	payload := []byte("same payload")
	base := noisyRGBA(120, 90, 7)
	m1, m2 := cloneRGBA(base), cloneRGBA(base)

	require.NoError(t, Encode(m1, pass, bytes.NewReader(payload), bpc, ch))
	require.NoError(t, EncodeParallel(m2, pass, bytes.NewReader(payload), bpc, ch))

	keys1, points1, err := prepareDecode(m1, pass, bpc, ch)
	require.NoError(t, err)
	keys2, points2, err := prepareDecode(m2, pass, bpc, ch)
	require.NoError(t, err)

	k := saltPixels(bpc * ch)
	require.Equal(t, 43, k)
	public, err := cursors.NewPixelOrder(120, 90, k)
	require.NoError(t, err)
	require.Equal(t, public[:k], points1[:k], "salt pixels must not depend on any secret")
	require.Equal(t, public[:k], points2[:k], "salt pixels must not depend on any secret")

	require.NotEqual(t, keys1.orderKey, keys2.orderKey, "fresh salts must yield different order keys")
	require.NotEqual(t, points1[k:], points2[k:], "secret part of the order must differ between encodes")

	for _, m := range []*image.RGBA{m1, m2} {
		got, err := Decode(m, pass, bpc, ch)
		require.NoError(t, err)
		require.Equal(t, payload, got)
	}
}

// TestOrderWrongPasswordFails checks a wrong password is rejected by both
// decoders: it reads the right salt but derives a different order and keys.
func TestOrderWrongPasswordFails(t *testing.T) {
	m := noisyRGBA(100, 100, 3)
	require.NoError(t, EncodeParallel(m, []byte("right"), bytes.NewReader([]byte("secret")), 1, 3))

	_, err := Decode(m, []byte("wrong"), 1, 3)
	require.Error(t, err)
	_, err = DecodeParallel(m, []byte("wrong"), 1, 3)
	require.Error(t, err)

	_, err = Decode(m, nil, 1, 3)
	require.Error(t, err)
	_, err = DecodeParallel(m, nil, 1, 3)
	require.Error(t, err)
}

// TestCarrierSmallerThanSaltPrefix covers carriers with fewer pixels than the
// salt occupies (k = ceil(128 / bitsPerPixel)). Encoders must report the
// existing "too small" error; decoders must error rather than panic or index
// out of range.
func TestCarrierSmallerThanSaltPrefix(t *testing.T) {
	full := noisyRGBA(64, 64, 11)
	for _, tc := range []struct {
		name     string
		img      *image.RGBA
		bpc, ch  int
		wantKGtN bool
	}{
		{"1x1", image.NewRGBA(image.Rect(0, 0, 1, 1)), 1, 3, true},
		{"6x7_3bpp", noisyRGBA(6, 7, 1), 1, 3, true},                                          // 42 < 43
		{"10x12_1bpp", noisyRGBA(10, 12, 2), 1, 1, true},                                      // 120 < 128
		{"sub_5x8_3bpp", full.SubImage(image.Rect(30, 20, 35, 28)).(*image.RGBA), 1, 3, true}, // 40 < 43
		{"1x43_3bpp", noisyRGBA(1, 43, 3), 1, 3, false},                                       // exactly k
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := tc.img.Bounds()
			require.Equal(t, tc.wantKGtN, saltPixels(tc.bpc*tc.ch) > b.Dx()*b.Dy())

			for _, enc := range []func() error{
				func() error {
					return Encode(cloneRGBA(tc.img), []byte("pw"), bytes.NewReader([]byte("x")), tc.bpc, tc.ch)
				},
				func() error {
					return EncodeParallel(cloneRGBA(tc.img), []byte("pw"), bytes.NewReader([]byte("x")), tc.bpc, tc.ch)
				},
			} {
				require.ErrorContains(t, enc(), "image too small")
			}

			_, err := Decode(tc.img, []byte("pw"), tc.bpc, tc.ch)
			require.Error(t, err)
			_, err = DecodeParallel(tc.img, []byte("pw"), tc.bpc, tc.ch)
			require.Error(t, err)
		})
	}
}
