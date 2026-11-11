package cursors_test

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"math"
	"math/rand"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pableeee/steg/cursors"
	"github.com/pableeee/steg/steg/analysis"
)

func channelOpts(ch int) []cursors.Option {
	var opts []cursors.Option
	if ch >= 2 {
		opts = append(opts, cursors.UseGreenBit())
	}
	if ch >= 3 {
		opts = append(opts, cursors.UseBlueBit())
	}
	return opts
}

func subImage(m draw.Image, r image.Rectangle) draw.Image {
	return m.(interface {
		SubImage(image.Rectangle) image.Image
	}).SubImage(r).(draw.Image)
}

func stride(m draw.Image) int {
	switch m := m.(type) {
	case *image.RGBA:
		return m.Stride
	case *image.NRGBA:
		return m.Stride
	}
	panic("unsupported")
}

// writeFull writes data through a cursor over m and returns the cursor's
// adapter rewound to the start.
func writeFull(t testing.TB, m draw.Image, data []byte, opts ...cursors.Option) io.ReadWriteSeeker {
	t.Helper()
	cur, err := cursors.NewRNGCursor(m, opts...)
	require.NoError(t, err)
	rw := cursors.CursorAdapter(cur)
	_, err = rw.Write(data)
	require.NoError(t, err)
	_, err = rw.Seek(0, io.SeekStart)
	require.NoError(t, err)
	return rw
}

// checkMatchingDiff checks every byte of after against before: only the used
// channels of pixels inside r (in full-image coordinates) may change, each by
// exactly ±1 and only when its bit 0 had to flip. It returns the number of +1
// and −1 changes.
func checkMatchingDiff(t *testing.T, before, after []byte, strd int, r image.Rectangle, ch int) (up, down int) {
	t.Helper()
	require.Len(t, after, len(before))
	for i := range before {
		x, y, c := (i%strd)/4, i/strd, i%4
		usable := image.Pt(x, y).In(r) && c < ch
		o, n := int(before[i]), int(after[i])
		if !usable {
			require.Equal(t, o, n, "byte %d (x=%d y=%d c=%d) outside the written slots changed", i, x, y, c)
			continue
		}
		switch n - o {
		case 0:
		case 1:
			up++
		case -1:
			down++
		default:
			t.Fatalf("byte %d changed by %d (%d -> %d)", i, n-o, o, n)
		}
	}
	return up, down
}

// TestLSBMatchingRoundTrip writes random data with matching on, for 1–3
// channels, both carrier types, the full image and a sub-image with a wide
// stride, and checks the bits read back (through ReadByte and through the
// independent At-based reader) and the ±1 / untouched / alpha rules.
func TestLSBMatchingRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	full := image.Rect(0, 0, 40, 40)
	for _, r := range []image.Rectangle{full, image.Rect(3, 5, 26, 24)} {
		W, H := r.Dx(), r.Dy()
		for ch := 1; ch <= 3; ch++ {
			order, err := cursors.NewPixelOrder(W, H, 7)
			require.NoError(t, err)
			cursors.FinishPixelOrder(order, 7, [32]byte{byte(ch), 9})
			data := make([]byte, W*H*ch/8)
			rng.Read(data)

			rgba := image.NewRGBA(full)
			rng.Read(rgba.Pix)
			nrgba := image.NewNRGBA(full)
			rng.Read(nrgba.Pix)
			for i := 3; i < len(nrgba.Pix); i += 4 {
				nrgba.Pix[i] = 255 // opaque, so the At-based reader sees exact values
			}
			for _, img := range []draw.Image{rgba, nrgba} {
				name := fmt.Sprintf("%T %v ch=%d", img, r, ch)
				before := bytes.Clone(pixOf(img))
				sub := subImage(img, r)
				opts := append(channelOpts(ch), cursors.WithSharedPoints(order), cursors.WithLSBMatching())
				rw := writeFull(t, sub, data, opts...)

				readBack := make([]byte, len(data))
				_, err := io.ReadFull(rw, readBack)
				require.NoError(t, err, name)
				require.Equal(t, data, readBack, name)
				require.Equal(t, data, atSetRead(sub, order, 1, ch, len(data)), name)

				up, down := checkMatchingDiff(t, before, pixOf(img), stride(img), r, ch)
				require.Positive(t, up, name)
				require.Positive(t, down, name)
			}
		}
	}
}

// TestLSBMatchingSaturatedCarrier checks 0 and 255 never wrap: 0 can only
// become 1 and 255 only 254.
func TestLSBMatchingSaturatedCarrier(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for _, img := range []draw.Image{image.NewRGBA(image.Rect(0, 0, 64, 64)), image.NewNRGBA(image.Rect(0, 0, 64, 64))} {
		pix := pixOf(img)
		for i := range pix {
			pix[i] = byte(rng.Intn(2)) * 255
		}
		before := bytes.Clone(pix)
		data := make([]byte, 64*64*3/8)
		rng.Read(data)
		opts := append(channelOpts(3), cursors.WithLSBMatching())
		rw := writeFull(t, img, data, opts...)

		readBack := make([]byte, len(data))
		_, err := io.ReadFull(rw, readBack)
		require.NoError(t, err)
		require.Equal(t, data, readBack)

		changed := 0
		for i := range pix {
			if pix[i] == before[i] {
				continue
			}
			changed++
			require.NotEqual(t, 3, i%4, "alpha changed")
			switch before[i] {
			case 0:
				require.EqualValues(t, 1, pix[i])
			case 255:
				require.EqualValues(t, 254, pix[i])
			}
		}
		require.Greater(t, changed, len(data)*8/3, "about half the slots must change")
	}
}

// TestLSBMatchingDirections checks both directions occur about equally often
// when neither is forced, and that two encodes of the same input differ
// (each cursor has its own randomly seeded generator).
func TestLSBMatchingDirections(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	carrier := image.NewRGBA(image.Rect(0, 0, 200, 200))
	for i := range carrier.Pix {
		carrier.Pix[i] = byte(2 + rng.Intn(252)) // 2..253
	}
	data := make([]byte, 200*200*3/8)
	rng.Read(data)

	a, b := cloneImage(carrier), cloneImage(carrier)
	opts := append(channelOpts(3), cursors.WithLSBMatching())
	writeFull(t, a, data, opts...)
	writeFull(t, b, data, opts...)

	up, down := checkMatchingDiff(t, carrier.Pix, pixOf(a), carrier.Stride, carrier.Rect, 3)
	total := up + down
	require.GreaterOrEqual(t, total, 10000)
	frac := float64(up) / float64(total)
	t.Logf("changes: %d (+1: %d, -1: %d, fraction +1 = %.4f)", total, up, down, frac)
	require.InDelta(t, 0.5, frac, 0.05)

	require.NotEqual(t, pixOf(a), pixOf(b), "directions must not be reproducible")
}

// TestLSBMatchingIgnoredAboveOneBit checks that with more than 1 bit per
// channel the option changes nothing: the bytes equal those written by a
// cursor without it.
func TestLSBMatchingIgnoredAboveOneBit(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for ch := 1; ch <= 3; ch++ {
		for bpc := 2; bpc <= 8; bpc++ {
			carrier := image.NewRGBA(image.Rect(0, 0, 17, 13))
			rng.Read(carrier.Pix)
			data := make([]byte, 17*13*ch*bpc/8)
			rng.Read(data)
			order, err := cursors.NewPixelOrder(17, 13, 3)
			require.NoError(t, err)
			cursors.FinishPixelOrder(order, 3, [32]byte{byte(ch), byte(bpc)})

			plain, matched := cloneImage(carrier), cloneImage(carrier)
			opts := append(channelOpts(ch), cursors.WithSharedPoints(order), cursors.WithBitsPerChannel(bpc))
			writeFull(t, plain, data, opts...)
			writeFull(t, matched, data, append(opts, cursors.WithLSBMatching())...)
			require.Equal(t, pixOf(plain), pixOf(matched), "ch=%d bpc=%d", ch, bpc)
		}
	}
}

// TestLSBMatchingConcurrent runs two matching cursors over disjoint halves of
// the same image from two goroutines; run with -race.
func TestLSBMatchingConcurrent(t *testing.T) {
	rng := rand.New(rand.NewSource(6))
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	rng.Read(img.Pix)
	order, err := cursors.NewPixelOrder(64, 64, 3)
	require.NoError(t, err)
	cursors.FinishPixelOrder(order, 3, [32]byte{6})
	halves := [][]uint32{order[:len(order)/2], order[len(order)/2:]}
	data := [2][]byte{make([]byte, len(halves[0])*3/8), make([]byte, len(halves[1])*3/8)}
	rng.Read(data[0])
	rng.Read(data[1])

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for w := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			opts := append(channelOpts(3), cursors.WithSharedPoints(halves[w]), cursors.WithLSBMatching())
			cur, err := cursors.NewRNGCursor(img, opts...)
			if err == nil {
				_, err = cursors.CursorAdapter(cur).Write(data[w])
			}
			errs[w] = err
		}()
	}
	wg.Wait()
	for w := range 2 {
		require.NoError(t, errs[w])
		rw := writeFull(t, img, nil, append(channelOpts(3), cursors.WithSharedPoints(halves[w]))...)
		readBack := make([]byte, len(data[w]))
		_, err := io.ReadFull(rw, readBack)
		require.NoError(t, err)
		require.Equal(t, data[w], readBack, "worker %d", w)
	}
}

// photoLikeImage is a copy of the steg/analysis test model: smooth gradients
// plus mild Gaussian noise, like a real 8-bit photograph.
func photoLikeImage(w, h int) *image.RGBA {
	rng := rand.New(rand.NewSource(1))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			fx, fy := float64(x)/float64(w), float64(y)/float64(h)
			base := [3]float64{
				60 + 120*fx + 25*math.Sin(7*fy+3*fx),
				80 + 90*fy + 30*math.Cos(5*fx-2*fy),
				110 + 50*fx*fy + 35*math.Sin(6*(fx+fy)),
			}
			var c [3]uint8
			for i, v := range base {
				v += 2 * rng.NormFloat64()
				c[i] = uint8(math.Max(0, math.Min(255, math.Round(v))))
			}
			img.Set(x, y, color.RGBA{R: c[0], G: c[1], B: c[2], A: 255})
		}
	}
	return img
}

// pairImbalance is Σ|h[2k] − h[2k+1]| / Σ(h[2k] + h[2k+1]) over one channel:
// LSB replacement drives it towards 0.
func pairImbalance(pix []byte, c int) float64 {
	var h [256]int
	for i := c; i < len(pix); i += 4 {
		h[pix[i]]++
	}
	diff, sum := 0, 0
	for k := 0; k < 256; k += 2 {
		d := h[k] - h[k+1]
		if d < 0 {
			d = -d
		}
		diff += d
		sum += h[k] + h[k+1]
	}
	return float64(diff) / float64(sum)
}

// TestLSBMatchingSteganalysis fills every slot of a photo-like carrier with
// random bits, once by replacement and once by matching, and logs what the
// steg/analysis detectors report. It asserts nothing about the numbers.
func TestLSBMatchingSteganalysis(t *testing.T) {
	const W, H = 512, 512
	clean := photoLikeImage(W, H)
	data := make([]byte, W*H*3/8)
	rand.New(rand.NewSource(7)).Read(data)

	repl, match := cloneImage(clean), cloneImage(clean)
	writeFull(t, repl, data, channelOpts(3)...)
	writeFull(t, match, data, append(channelOpts(3), cursors.WithLSBMatching())...)

	for _, tc := range []struct {
		name string
		img  draw.Image
	}{{"clean", clean}, {"replacement", repl}, {"matching", match}} {
		res := analysis.Analyze(tc.img)
		for c := range 3 {
			cs, rs := res.ChiSquare[c], res.RS[c]
			t.Logf("%-11s %s: chi2 p=%.4f (suspicious=%v)  RS asym=%+.4f (suspicious=%v)  pair-imbalance=%.4f",
				tc.name, cs.Channel, cs.PValue, cs.Suspicious, rs.Asymmetry, rs.Suspicious,
				pairImbalance(pixOf(tc.img), c))
		}
		t.Logf("%-11s verdict: %s", tc.name, res.Verdict)
	}
}

func BenchmarkWriteByte(b *testing.B) {
	const W, H = 1024, 1024
	rng := rand.New(rand.NewSource(8))
	order, err := cursors.NewPixelOrder(W, H, 3)
	require.NoError(b, err)
	cursors.FinishPixelOrder(order, 3, [32]byte{8})
	data := make([]byte, 4093) // prime: later passes over the carrier write shifted data
	rng.Read(data)

	for _, mode := range []struct {
		name string
		opts []cursors.Option
	}{
		{"replacement", nil},
		{"matching", []cursors.Option{cursors.WithLSBMatching()}},
	} {
		b.Run(mode.name, func(b *testing.B) {
			img := image.NewRGBA(image.Rect(0, 0, W, H))
			rng.Read(img.Pix)
			opts := append(channelOpts(3), cursors.WithSharedPoints(order))
			cur, err := cursors.NewRNGCursor(img, append(opts, mode.opts...)...)
			require.NoError(b, err)
			capBytes := W * H * 3 / 8
			b.SetBytes(1)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if i%capBytes == 0 {
					_, _ = cur.Seek(0, io.SeekStart)
				}
				if err := cur.WriteByte(data[i%len(data)]); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
