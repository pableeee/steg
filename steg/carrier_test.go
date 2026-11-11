package steg_test

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pableeee/steg/cursors"
	"github.com/pableeee/steg/steg"
)

// entryPoints runs each of the four public operations on m. Decoders get a
// fixed password; for the rejection tests below only the error matters.
var entryPoints = []struct {
	name string
	run  func(m draw.Image, bpc, ch int) error
}{
	{"Encode", func(m draw.Image, bpc, ch int) error {
		return steg.Encode(m, []byte("pw"), bytes.NewReader([]byte("payload")), bpc, ch)
	}},
	{"EncodeParallel", func(m draw.Image, bpc, ch int) error {
		return steg.EncodeParallel(m, []byte("pw"), bytes.NewReader([]byte("payload")), bpc, ch)
	}},
	{"Decode", func(m draw.Image, bpc, ch int) error {
		_, err := steg.Decode(m, []byte("pw"), bpc, ch)
		return err
	}},
	{"DecodeParallel", func(m draw.Image, bpc, ch int) error {
		_, err := steg.DecodeParallel(m, []byte("pw"), bpc, ch)
		return err
	}},
}

// snapshot captures every pixel of m through its own color model, so any
// modification, whatever the image type, shows up as a difference.
func snapshot(m image.Image) []color.Color {
	b := m.Bounds()
	out := make([]color.Color, 0, b.Dx()*b.Dy())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			out = append(out, m.At(x, y))
		}
	}
	return out
}

// rgbaWrapper is a draw.Image that embeds a supported type. The library cannot
// know how such a wrapper maps At/Set to the underlying pixels, so it is
// rejected like any other foreign type.
type rgbaWrapper struct{ *image.RGBA }

// TestUnsupportedCarriersRejected checks every entry point refuses carriers
// whose pixels cannot be addressed exactly, before modifying any pixel. Before
// this check, Gray, NRGBA64 and Paletted carriers encoded "successfully" into
// undecodable images and RGBA64 carriers had their high bytes destroyed.
func TestUnsupportedCarriersRejected(t *testing.T) {
	const w, h = 64, 64
	r := image.Rect(0, 0, w, h)
	rng := rand.New(rand.NewSource(5))
	pal := make(color.Palette, 256)
	for i := range pal {
		pal[i] = color.RGBA{uint8(i), uint8(255 - i), uint8(i * 7), 255}
	}
	makers := map[string]func() draw.Image{
		"Gray":     func() draw.Image { m := image.NewGray(r); rng.Read(m.Pix); return m },
		"RGBA64":   func() draw.Image { m := image.NewRGBA64(r); rng.Read(m.Pix); return m },
		"NRGBA64":  func() draw.Image { m := image.NewNRGBA64(r); rng.Read(m.Pix); return m },
		"Paletted": func() draw.Image { m := image.NewPaletted(r, pal); rng.Read(m.Pix); return m },
		"Wrapper":  func() draw.Image { return rgbaWrapper{noisyImage(w, h, 9)} },
	}
	for name, mk := range makers {
		for _, ep := range entryPoints {
			t.Run(name+"/"+ep.name, func(t *testing.T) {
				m := mk()
				before := snapshot(m)
				err := ep.run(m, 1, 3)
				require.ErrorIs(t, err, cursors.ErrUnsupportedImage)
				require.ErrorContains(t, err, "*image.RGBA and *image.NRGBA")
				require.Equal(t, before, snapshot(m), "carrier was modified")
			})
		}
	}
}

// TestInvalidSettingsRejected checks every entry point refuses settings
// outside bitsPerChannel 1..8 and channels 1..3 before modifying any pixel,
// and that the capacity functions report 0 for them. Before, bitsPerChannel=9
// encoded into an undecodable image and channels=4 failed half-way through.
func TestInvalidSettingsRejected(t *testing.T) {
	for _, tc := range []struct {
		bpc, ch int
		msg     string
	}{
		{0, 3, "bits per channel"}, {9, 3, "bits per channel"}, {-1, 3, "bits per channel"},
		{1, 0, "channels"}, {1, 4, "channels"}, {1, -1, "channels"},
	} {
		require.Zero(t, steg.CapacityForDims(500, 500, tc.bpc, tc.ch), "bpc=%d ch=%d", tc.bpc, tc.ch)
		require.Zero(t, steg.CapacityBytes(image.NewRGBA(image.Rect(0, 0, 500, 500)), tc.bpc, tc.ch))
		for _, ep := range entryPoints {
			m := noisyImage(100, 100, 3)
			before := bytes.Clone(m.Pix)
			err := ep.run(m, tc.bpc, tc.ch)
			require.ErrorContains(t, err, tc.msg, "%s bpc=%d ch=%d", ep.name, tc.bpc, tc.ch)
			require.Equal(t, before, m.Pix, "%s bpc=%d ch=%d modified the carrier", ep.name, tc.bpc, tc.ch)
		}
	}
}

// TestCarrierDimensionLimits covers dimensions the format cannot index or
// describe. The carriers have no pixel storage at all, so any access before
// validation would panic.
func TestCarrierDimensionLimits(t *testing.T) {
	require.Zero(t, steg.CapacityForDims(0, 10, 1, 3))
	require.Zero(t, steg.CapacityForDims(10, -1, 1, 3))
	require.Zero(t, steg.CapacityForDims(1<<40, 1<<40, 1, 3), "w*h overflows")
	require.Zero(t, steg.CapacityForDims(65536, 65536, 1, 1), "2^32 pixels exceed the uint32 index")
	require.Zero(t, steg.CapacityForDims(65536, 65535, 8, 3), "capacity exceeds the 4 GiB length field")
	require.Equal(t, 65536*65535/8-steg.Overhead, steg.CapacityForDims(65536, 65535, 1, 1))

	for _, tc := range []struct {
		r   image.Rectangle
		bpc int
		msg string
	}{
		{image.Rect(0, 0, 65536, 65536), 1, "too large"},
		{image.Rect(0, 0, 65536, 65535), 8, "4 GiB"},
		{image.Rect(0, 0, 0, 10), 1, "invalid image dimensions"},
	} {
		for _, ep := range entryPoints {
			err := ep.run(&image.RGBA{Rect: tc.r}, tc.bpc, 3)
			require.ErrorContains(t, err, tc.msg, "%s %v", ep.name, tc.r)
		}
	}
}

// TestNRGBATransparentRoundTrip checks NRGBA carriers with semi-transparent
// and fully transparent pixels round-trip through every encode/decode
// combination, and that alpha is never modified. Reading through At().RGBA()
// premultiplied the colour, so these carriers used to be undecodable.
func TestNRGBATransparentRoundTrip(t *testing.T) {
	alphas := []uint8{0, 1, 77, 128, 254, 255}
	for _, s := range []struct{ bpc, ch int }{{1, 3}, {2, 2}, {8, 1}, {3, 3}} {
		payload := subImagePayload(steg.CapacityForDims(160, 120, s.bpc, s.ch) / 2)
		for _, enc := range []struct {
			name string
			fn   func(draw.Image, []byte, *bytes.Reader, int, int) error
		}{
			{"Encode", func(m draw.Image, p []byte, r *bytes.Reader, b, c int) error { return steg.Encode(m, p, r, b, c) }},
			{"EncodeParallel", func(m draw.Image, p []byte, r *bytes.Reader, b, c int) error {
				return steg.EncodeParallel(m, p, r, b, c)
			}},
		} {
			for _, dec := range []struct {
				name string
				fn   func(draw.Image, []byte, int, int) ([]byte, error)
			}{{"Decode", steg.Decode}, {"DecodeParallel", steg.DecodeParallel}} {
				m := image.NewNRGBA(image.Rect(0, 0, 160, 120))
				rand.New(rand.NewSource(int64(s.bpc*10 + s.ch))).Read(m.Pix)
				for i := 3; i < len(m.Pix); i += 4 {
					m.Pix[i] = alphas[(i/4)%len(alphas)]
				}
				before := bytes.Clone(m.Pix)

				require.NoError(t, enc.fn(m, []byte("pw"), bytes.NewReader(payload), s.bpc, s.ch))
				got, err := dec.fn(m, []byte("pw"), s.bpc, s.ch)
				require.NoError(t, err, "%s/%s bpc=%d ch=%d", enc.name, dec.name, s.bpc, s.ch)
				require.Equal(t, payload, got, "%s/%s bpc=%d ch=%d", enc.name, dec.name, s.bpc, s.ch)
				for i := 3; i < len(m.Pix); i += 4 {
					require.Equal(t, before[i], m.Pix[i], "alpha of pixel %d changed", i/4)
				}
			}
		}
	}
}

// TestNRGBASubImageRoundTrip covers an NRGBA sub-image (non-zero origin,
// stride wider than the sub-image) through the parallel paths.
func TestNRGBASubImageRoundTrip(t *testing.T) {
	full := image.NewNRGBA(image.Rect(0, 0, 300, 300))
	rand.New(rand.NewSource(17)).Read(full.Pix)
	sub := full.SubImage(image.Rect(37, 81, 290, 260)).(*image.NRGBA)
	payload := subImagePayload(10000)
	require.NoError(t, steg.EncodeParallel(sub, []byte("pw"), bytes.NewReader(payload), 1, 3))
	for _, dec := range []func(draw.Image, []byte, int, int) ([]byte, error){steg.Decode, steg.DecodeParallel} {
		got, err := dec(sub, []byte("pw"), 1, 3)
		require.NoError(t, err)
		require.Equal(t, payload, got)
	}
}

// TestFormatFixtures decodes carriers written by the build before the cursor
// switched from At/Set to direct Pix access (commit ec9c05c). The files hold
// the raw RGBA Pix bytes of 24×24 opaque images, converted losslessly from the
// PNGs that build produced. The same bytes must also decode as an NRGBA
// carrier: for opaque pixels the two types store identical values.
func TestFormatFixtures(t *testing.T) {
	for _, fx := range []struct {
		file    string
		bpc, ch int
		payload string
	}{
		{"v2_24x24_2ch_2bit.rgba", 2, 2,
			"v2 format fixture written by steg ec9c05c, 2 channels x 2 bits, sequential encode."},
		{"v2_24x24_3ch_1bit_parallel.rgba", 1, 3,
			"v2 format fixture written by steg ec9c05c, 3 channels x 1 bit, parallel."},
	} {
		pix, err := os.ReadFile(filepath.Join("testdata", fx.file))
		require.NoError(t, err)
		r := image.Rect(0, 0, 24, 24)
		require.Len(t, pix, 24*24*4)
		carriers := []draw.Image{
			&image.RGBA{Pix: pix, Stride: 24 * 4, Rect: r},
			&image.NRGBA{Pix: bytes.Clone(pix), Stride: 24 * 4, Rect: r},
		}
		for _, m := range carriers {
			for _, dec := range []func(draw.Image, []byte, int, int) ([]byte, error){steg.Decode, steg.DecodeParallel} {
				got, err := dec(m, []byte("fixture-pass"), fx.bpc, fx.ch)
				require.NoError(t, err, "%s as %T", fx.file, m)
				require.Equal(t, fx.payload, string(got), "%s as %T", fx.file, m)
			}
		}
	}
}

func noisyImage(w, h int, seed int64) *image.RGBA {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	rand.New(rand.NewSource(seed)).Read(m.Pix)
	return m
}
