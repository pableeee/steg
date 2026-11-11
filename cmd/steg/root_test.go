package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pableeee/steg/steg"
)

const testPass = "correct horse battery staple"

// setGlobals sets the package-level flag state the commands read and restores
// it when the test ends. Tests in this file must not run in parallel.
func setGlobals(t *testing.T, par bool, bpc, ch int) {
	t.Helper()
	oldEnc, oldDec, oldTV := encoderFlags, decoderFlags, testVisualFlags
	oldPar, oldBpc, oldCh := parallel, bitsPerChannel, channels
	t.Cleanup(func() {
		encoderFlags, decoderFlags, testVisualFlags = oldEnc, oldDec, oldTV
		parallel, bitsPerChannel, channels = oldPar, oldBpc, oldCh
	})
	parallel, bitsPerChannel, channels = par, bpc, ch
}

func encodeFile(t *testing.T, carrier, msg, out string, par bool, bpc, ch int) error {
	t.Helper()
	setGlobals(t, par, bpc, ch)
	encoderFlags.inputImage = carrier
	encoderFlags.inputMessage = msg
	encoderFlags.outputImage = out
	encoderFlags.key = testPass
	return runEncode()
}

func decodeFile(t *testing.T, img string, par bool, bpc, ch int) ([]byte, error) {
	t.Helper()
	setGlobals(t, par, bpc, ch)
	out := filepath.Join(t.TempDir(), "decoded.bin")
	decoderFlags.inputFile = img
	decoderFlags.outputFile = out
	decoderFlags.key = testPass
	if err := runDecode(); err != nil {
		return nil, err
	}
	return os.ReadFile(out)
}

func writeCarrier(t *testing.T, path string, img image.Image) {
	t.Helper()
	if err := encodeImage(path, img); err != nil {
		t.Fatalf("write carrier: %v", err)
	}
}

func writePayload(t *testing.T, dir string, n int, seed int64) (string, []byte) {
	t.Helper()
	p := make([]byte, n)
	rand.New(rand.NewSource(seed)).Read(p)
	path := filepath.Join(dir, "payload.bin")
	if err := os.WriteFile(path, p, 0600); err != nil {
		t.Fatal(err)
	}
	return path, p
}

// noisyNRGBA returns a w×h image with random colours and alpha 255.
func noisyNRGBA(w, h int, seed int64) *image.NRGBA {
	r := rand.New(rand.NewSource(seed))
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	r.Read(img.Pix)
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 0xff
	}
	return img
}

// withSemiTransparentRegion makes the top-left quarter alpha 128.
func withSemiTransparentRegion(img *image.NRGBA) *image.NRGBA {
	b := img.Bounds()
	for y := 0; y < b.Dy()/2; y++ {
		for x := 0; x < b.Dx()/2; x++ {
			img.Pix[img.PixOffset(x, y)+3] = 128
		}
	}
	return img
}

// withAlphaZeroPixel makes exactly one pixel fully transparent.
func withAlphaZeroPixel(img *image.NRGBA) *image.NRGBA {
	img.Pix[img.PixOffset(7, 5)+3] = 0
	return img
}

// assertNoStrayFiles fails if dir contains anything other than want.
func assertOnlyFiles(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{}
	for _, w := range want {
		allowed[w] = true
	}
	for _, e := range entries {
		if !allowed[e.Name()] {
			t.Errorf("unexpected file left in output dir: %s", e.Name())
		}
	}
}

func roundTrip(t *testing.T, carrierImg image.Image, carrierName, outName string, par bool, bpc, ch int) {
	t.Helper()
	dir := t.TempDir()
	carrier := filepath.Join(dir, carrierName)
	writeCarrier(t, carrier, carrierImg)
	msg, payload := writePayload(t, dir, 300, 1)
	out := filepath.Join(dir, outName)

	if err := encodeFile(t, carrier, msg, out, par, bpc, ch); err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := decodeFile(t, out, par, bpc, ch)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("decoded payload differs from original")
	}
	assertOnlyFiles(t, dir, carrierName, "payload.bin", outName)
}

func TestOpaquePNGRoundTrip(t *testing.T) {
	for _, par := range []bool{false, true} {
		t.Run(fmt.Sprintf("parallel=%v", par), func(t *testing.T) {
			roundTrip(t, noisyNRGBA(64, 64, 2), "carrier.png", "out.png", par, 1, 3)
		})
	}
}

func TestBMPOutputRoundTrip(t *testing.T) {
	roundTrip(t, noisyNRGBA(64, 64, 3), "carrier.png", "out.bmp", false, 1, 3)
}

func TestTIFFOutputRoundTrip(t *testing.T) {
	roundTrip(t, noisyNRGBA(64, 64, 4), "carrier.png", "out.tiff", true, 2, 2)
}

func Test16BitPNGCarrier(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	img := image.NewNRGBA64(image.Rect(0, 0, 64, 64))
	r.Read(img.Pix)
	for i := 6; i < len(img.Pix); i += 8 {
		img.Pix[i], img.Pix[i+1] = 0xff, 0xff
	}
	roundTrip(t, img, "carrier16.png", "out.png", false, 1, 3)
}

func TestGrayPNGCarrier(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 64, 64))
	rand.New(rand.NewSource(6)).Read(img.Pix)
	roundTrip(t, img, "gray.png", "out.png", false, 1, 3)
}

// TestNonOpaqueCarrierRejected covers the original data-loss bug: PNG and BMP
// store straight alpha, so writing toDrawImage's premultiplied pixels destroys
// the hidden bits. Encode must fail and leave nothing at the output path.
// TIFF would round-trip, but is rejected as well so every format behaves the
// same (see checkCarrierAlpha).
func TestNonOpaqueCarrierRejected(t *testing.T) {
	cases := []struct {
		name string
		img  *image.NRGBA
		out  string
		par  bool
	}{
		{"semi-transparent region to PNG", withSemiTransparentRegion(noisyNRGBA(64, 64, 7)), "out.png", false},
		{"single alpha-0 pixel to PNG", withAlphaZeroPixel(noisyNRGBA(64, 64, 8)), "out.png", false},
		{"single alpha-0 pixel to PNG parallel", withAlphaZeroPixel(noisyNRGBA(64, 64, 8)), "out.png", true},
		{"single alpha-0 pixel to BMP", withAlphaZeroPixel(noisyNRGBA(64, 64, 9)), "out.bmp", false},
		{"semi-transparent region to BMP", withSemiTransparentRegion(noisyNRGBA(64, 64, 10)), "out.bmp", false},
		{"single alpha-0 pixel to TIFF", withAlphaZeroPixel(noisyNRGBA(64, 64, 12)), "out.tiff", false},
		{"semi-transparent region to TIF parallel", withSemiTransparentRegion(noisyNRGBA(64, 64, 13)), "out.tif", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			carrier := filepath.Join(dir, "carrier.png")
			writeCarrier(t, carrier, tc.img)
			msg, _ := writePayload(t, dir, 100, 11)
			out := filepath.Join(dir, tc.out)

			err := encodeFile(t, carrier, msg, out, tc.par, 1, 3)
			if err == nil {
				t.Fatal("encode succeeded with a non-opaque carrier")
			}
			if !strings.Contains(err.Error(), "transparency") ||
				!strings.Contains(err.Error(), "alpha channel") {
				t.Errorf("error does not explain the transparency problem: %v", err)
			}
			if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
				t.Errorf("output file exists after failed encode (stat err: %v)", statErr)
			}
			assertOnlyFiles(t, dir, "carrier.png", "payload.bin")
		})
	}
}

// TestFailedEncodeKeepsExistingOutput checks that a failed encode does not
// clobber a file already at the output path, both for the early alpha check
// and for a payload that does not fit.
func TestFailedEncodeKeepsExistingOutput(t *testing.T) {
	cases := []struct {
		name    string
		img     *image.NRGBA
		payload int
	}{
		{"non-opaque carrier", withAlphaZeroPixel(noisyNRGBA(64, 64, 40)), 100},
		{"payload too large", noisyNRGBA(16, 16, 41), 4096},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			carrier := filepath.Join(dir, "carrier.png")
			writeCarrier(t, carrier, tc.img)
			msg, _ := writePayload(t, dir, tc.payload, 42)
			out := filepath.Join(dir, "out.png")
			original := []byte("precious existing content")
			if err := os.WriteFile(out, original, 0600); err != nil {
				t.Fatal(err)
			}

			if err := encodeFile(t, carrier, msg, out, false, 1, 3); err == nil {
				t.Fatal("encode succeeded unexpectedly")
			}
			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, original) {
				t.Fatal("pre-existing output file was modified by a failed encode")
			}
			assertOnlyFiles(t, dir, "carrier.png", "payload.bin", "out.png")
		})
	}
}

// TestWriteVerifiedCatchesLossyOutput bypasses the early alpha check and hands
// writeVerified exactly what the old code wrote: a premultiplied image with an
// alpha-0 pixel, saved as PNG. Verification must catch the loss, remove the
// temp file and leave the existing output alone.
func TestWriteVerifiedCatchesLossyOutput(t *testing.T) {
	for _, ext := range []string{".png", ".bmp"} {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			cimg := toDrawImage(withSemiTransparentRegion(withAlphaZeroPixel(noisyNRGBA(64, 64, 50))))
			payload := bytes.Repeat([]byte("hidden"), 30)
			if err := steg.Encode(cimg, []byte(testPass), bytes.NewReader(payload), 1, 3); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(dir, "out"+ext)
			original := []byte("existing")
			if err := os.WriteFile(out, original, 0600); err != nil {
				t.Fatal(err)
			}

			err := writeVerified(out, cimg, []byte(testPass), payload, 1, 3, false)
			if err == nil || !strings.Contains(err.Error(), "verification failed") {
				t.Fatalf("want verification failure, got %v", err)
			}
			got, _ := os.ReadFile(out)
			if !bytes.Equal(got, original) {
				t.Fatal("existing output was modified")
			}
			assertOnlyFiles(t, dir, "out"+ext)
		})
	}
}

// TestEncodeOverwritesExistingOnSuccess checks the rename path replaces a
// pre-existing output file when verification passes.
func TestEncodeOverwritesExistingOnSuccess(t *testing.T) {
	dir := t.TempDir()
	carrier := filepath.Join(dir, "carrier.png")
	writeCarrier(t, carrier, noisyNRGBA(64, 64, 60))
	msg, payload := writePayload(t, dir, 200, 61)
	out := filepath.Join(dir, "out.png")
	if err := os.WriteFile(out, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := encodeFile(t, carrier, msg, out, true, 1, 3); err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := decodeFile(t, out, false, 1, 3)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("decoded payload differs")
	}
	assertOnlyFiles(t, dir, "carrier.png", "payload.bin", "out.png")
}

func TestTestVisualRejectsNonOpaqueCarrier(t *testing.T) {
	setGlobals(t, false, 1, 3)
	dir := t.TempDir()
	carrier := filepath.Join(dir, "carrier.png")
	writeCarrier(t, carrier, withAlphaZeroPixel(noisyNRGBA(32, 32, 70)))
	outDir := filepath.Join(dir, "visual")
	testVisualFlags.inputImage = carrier
	testVisualFlags.outputDir = outDir
	testVisualFlags.key = testPass

	err := runTestVisual()
	if err == nil || !strings.Contains(err.Error(), "transparency") {
		t.Fatalf("want transparency error, got %v", err)
	}
	if _, statErr := os.Stat(outDir); !os.IsNotExist(statErr) {
		t.Errorf("output dir created despite rejection")
	}
}

func TestIsOpaqueFallback(t *testing.T) {
	// A type without an Opaque method exercises the pixel-scan fallback.
	img := withAlphaZeroPixel(noisyNRGBA(8, 8, 80))
	if isOpaque(noOpaque{img}) {
		t.Error("alpha-0 pixel not detected by fallback scan")
	}
	if !isOpaque(noOpaque{noisyNRGBA(8, 8, 81)}) {
		t.Error("opaque image reported as non-opaque")
	}
}

type noOpaque struct{ m *image.NRGBA }

func (n noOpaque) ColorModel() color.Model { return n.m.ColorModel() }
func (n noOpaque) Bounds() image.Rectangle { return n.m.Bounds() }
func (n noOpaque) At(x, y int) color.Color { return n.m.At(x, y) }
