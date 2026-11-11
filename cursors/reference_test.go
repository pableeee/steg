package cursors_test

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pableeee/steg/cursors"
)

// atSetWrite is the cursor's write algorithm before it switched to direct Pix
// access: load the pixel with At().RGBA() (16 bits per channel), set bit k of
// the 16-bit value, store with Set(color.RGBA{uint8(r), ...}).
func atSetWrite(m draw.Image, order []uint32, bpc, ch int, data []byte) {
	w := m.Bounds().Dx()
	min := m.Bounds().Min
	bpp := bpc * ch
	for bit := 0; bit < len(data)*8; bit++ {
		idx := int(order[bit/bpp])
		x, y := min.X+idx%w, min.Y+idx/w
		slot := bit % bpp
		k := uint(bpc - 1 - slot%bpc)
		v := uint32(data[bit/8]>>(7-bit%8)) & 1
		r, g, b, a := m.At(x, y).RGBA()
		c := [3]*uint32{&r, &g, &b}[slot/bpc]
		*c = *c&^(1<<k) | v<<k
		m.Set(x, y, color.RGBA{uint8(r), uint8(g), uint8(b), uint8(a)})
	}
}

// atSetRead is the matching read algorithm: bit k of the 16-bit value.
func atSetRead(m image.Image, order []uint32, bpc, ch, n int) []byte {
	w := m.Bounds().Dx()
	min := m.Bounds().Min
	bpp := bpc * ch
	out := make([]byte, n)
	for bit := 0; bit < n*8; bit++ {
		idx := int(order[bit/bpp])
		r, g, b, _ := m.At(min.X+idx%w, min.Y+idx/w).RGBA()
		slot := bit % bpp
		v := [3]uint32{r, g, b}[slot/bpc] >> uint(bpc-1-slot%bpc) & 1
		out[bit/8] |= byte(v) << (7 - bit%8)
	}
	return out
}

// TestCursorMatchesAtSetReference proves the switch to direct Pix access does
// not change the on-image format. For an *image.RGBA, At().RGBA() returns each
// 8-bit channel c as c*0x101, whose low byte is c, so modifying bit k < 8 of
// the 16-bit value and storing uint8(value) is exactly modifying bit k of the
// Pix byte; alpha is returned and stored unchanged. The same holds for an
// opaque *image.NRGBA (premultiplying by 0xffff is the identity). This checks
// it for every setting, on a sub-image with a wide stride, by comparing the
// bytes both algorithms produce and the bits both read back.
func TestCursorMatchesAtSetReference(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const W, H = 23, 19
	r := image.Rect(3, 5, 3+W, 5+H)

	for ch := 1; ch <= 3; ch++ {
		for bpc := 1; bpc <= 8; bpc++ {
			order, err := cursors.NewPixelOrder(W, H, 7)
			require.NoError(t, err)
			cursors.FinishPixelOrder(order, 7, [32]byte{byte(ch), byte(bpc)})
			data := make([]byte, W*H*ch*bpc/8)
			rng.Read(data)

			rgba := image.NewRGBA(image.Rect(0, 0, 40, 40))
			rng.Read(rgba.Pix) // includes non-opaque, even invalid premultiplied, pixels
			nrgba := image.NewNRGBA(image.Rect(0, 0, 40, 40))
			rng.Read(nrgba.Pix)
			for i := 3; i < len(nrgba.Pix); i += 4 {
				nrgba.Pix[i] = 255
			}

			for _, full := range []draw.Image{rgba, nrgba} {
				ref, got := cloneImage(full), cloneImage(full)
				refSub := ref.(interface {
					SubImage(image.Rectangle) image.Image
				}).SubImage(r).(draw.Image)
				gotSub := got.(interface {
					SubImage(image.Rectangle) image.Image
				}).SubImage(r).(draw.Image)

				atSetWrite(refSub, order, bpc, ch, data)

				opts := []cursors.Option{cursors.WithSharedPoints(order), cursors.WithBitsPerChannel(bpc)}
				if ch >= 2 {
					opts = append(opts, cursors.UseGreenBit())
				}
				if ch >= 3 {
					opts = append(opts, cursors.UseBlueBit())
				}
				cur, err := cursors.NewRNGCursor(gotSub, opts...)
				require.NoError(t, err)
				rw := cursors.CursorAdapter(cur)
				_, err = rw.Write(data)
				require.NoError(t, err)

				require.Equal(t, pixOf(ref), pixOf(got), "%T ch=%d bpc=%d: written bytes differ", full, ch, bpc)

				_, err = rw.Seek(0, 0)
				require.NoError(t, err)
				readBack := make([]byte, len(data))
				_, err = rw.Read(readBack)
				require.NoError(t, err)
				require.Equal(t, data, readBack)
				require.Equal(t, atSetRead(gotSub, order, bpc, ch, len(data)), readBack,
					"%T ch=%d bpc=%d: read bits differ", full, ch, bpc)
			}
		}
	}
}

func cloneImage(m draw.Image) draw.Image {
	switch m := m.(type) {
	case *image.RGBA:
		c := *m
		c.Pix = bytes.Clone(m.Pix)
		return &c
	case *image.NRGBA:
		c := *m
		c.Pix = bytes.Clone(m.Pix)
		return &c
	}
	panic("unsupported")
}

func pixOf(m draw.Image) []byte {
	switch m := m.(type) {
	case *image.RGBA:
		return m.Pix
	case *image.NRGBA:
		return m.Pix
	}
	panic("unsupported")
}
