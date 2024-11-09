package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"os"

	"github.com/pableeee/steg/steg"
	"github.com/spf13/cobra"
)

var (
	testCmd = &cobra.Command{
		Use:   "test",
		Short: "Encodes a test file for visual testing purposes",
		Long:  "Encodes a test file for visual testing purposes",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEncodeTest()
		},
	}

	testFlags = struct {
		outputFile,
		key string
		length,
		width int
		bitCount uint8
	}{}
)

func init() {
	testCmd.Flags().StringVarP(&testFlags.outputFile, "output", "o", "", "The path for output path")
	testCmd.Flags().StringVarP(&testFlags.outputFile, "password", "p", "YELLOW SUBMARINE", "passphrase to extract the contents.")
	testCmd.Flags().IntVarP(&testFlags.length, "length", "l", 100, "pixel length of the output image.")
	testCmd.Flags().IntVarP(&testFlags.width, "width", "w", 100, "pixel with of the output image.")
	testCmd.Flags().Uint8VarP(&testFlags.bitCount, "bit_count", "c", 1, "modified bits per pixel per RGB")
}

type fakeReader struct {
	max, cursor int64
}

func (r *fakeReader) Read(p []byte) (n int, err error) {
	left := r.cursor + r.max
	if left <= 0 {
		return int(r.cursor), io.EOF
	}

	n = len(p)
	if left < int64(len(p)) {
		n = int(left)
		r.cursor += left
		err = io.EOF
	}

	for i := 0; i < n; i++ {
		p[i] = 0xff
	}

	return n, err
}

func runEncodeTest() error {
	cimg := image.NewRGBA(image.Rect(0, 0, testFlags.length, testFlags.width))
	targetPixels := ((testFlags.length * testFlags.width) / 2) * 3
	black := color.RGBA{0, 0, 0, 255}
	draw.Draw(cimg, cimg.Bounds(), &image.Uniform{black}, image.ZP, draw.Src)

	out, err := os.Create(testFlags.outputFile)
	if err != nil {
		return fmt.Errorf("unable to create output file: %w", err)
	}
	defer out.Close()

	steg.Encode(cimg, []byte(encoderFlags.key), &fakeReader{max: int64(targetPixels)})

	err = png.Encode(out, cimg)
	if err != nil {
		return fmt.Errorf("unable to encode test image: %w", err)
	}

	return nil
}
