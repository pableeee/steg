package main

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"log"
	"os"
	"path/filepath"

	"github.com/pableeee/steg/steg"
	"github.com/spf13/cobra"
)

var parallel bool
var bitsPerChannel int
var channels int

var (
	rootCmd = &cobra.Command{
		Use:   "steg",
		Short: "Service command-line interface",
	}

	encodeCmd = &cobra.Command{
		Use:   "encode",
		Short: "Encodes inputfile into the provided image",
		Long:  "Encodes inputfile into the provided image",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEncode()
		},
	}

	encoderFlags = struct {
		inputImage,
		inputMessage,
		outputImage,
		key string
	}{}

	decodeCmd = &cobra.Command{
		Use:   "decode",
		Short: "Decodes a messages embedded on the provided image",
		Long:  "Decodes a messages embedded on the provided image",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDecode()
		},
	}

	decoderFlags = struct {
		inputFile,
		outputFile,
		key string
	}{}

	capacityCmd = &cobra.Command{
		Use:   "capacity",
		Short: "Show byte capacity of an image across channel and bit configurations",
		Long:  "Prints a table of usable byte capacity for every combination of active channels (1–3) and bits per channel (1, 2, 4, 8)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCapacity()
		},
	}

	capacityFlags = struct{ inputImage string }{}

	testVisualCmd = &cobra.Command{
		Use:   "test-visual",
		Short: "Generate test images at every encoding intensity for visual comparison",
		Long:  "Encodes the input image filled to capacity at every (channels × bits-per-channel) combination and writes the results to an output directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTestVisual()
		},
	}

	testVisualFlags = struct {
		inputImage,
		outputDir,
		key string
	}{}
)

func init() {
	encodeCmd.Flags().StringVarP(
		&encoderFlags.inputImage, "input_image", "i", "", "Input image used as medium.",
	)
	encodeCmd.Flags().StringVarP(
		&encoderFlags.inputMessage, "input_file", "f", "", "Message the will be encoded into the output image.",
	)
	encodeCmd.Flags().StringVarP(
		&encoderFlags.outputImage, "output_image", "o", "", "Image containing the coded message.",
	)
	encodeCmd.Flags().StringVarP(
		&encoderFlags.key, "password", "p", "", "passphrase to cipher the contents.",
	)
	encodeCmd.Flags().BoolVarP(&parallel, "parallel", "P", false, "use parallel encode")
	encodeCmd.Flags().IntVarP(&bitsPerChannel, "bits-per-channel", "b", 1, "number of LSBs to use per color channel (1-8)")
	encodeCmd.Flags().IntVarP(&channels, "channels", "c", 3, "number of color channels to use: 1=R, 2=R+G, 3=R+G+B")
	encodeCmd.MarkFlagRequired("password")

	decodeCmd.Flags().StringVarP(
		&decoderFlags.inputFile, "input_image", "i", "", "Image containing the coded message.",
	)
	decodeCmd.Flags().StringVarP(
		&decoderFlags.outputFile, "output_file", "o", "", "Path for the output file containing the coded data.",
	)
	decodeCmd.Flags().StringVarP(
		&decoderFlags.key, "password", "p", "", "passphrase to extract the contents.",
	)
	decodeCmd.Flags().BoolVarP(&parallel, "parallel", "P", false, "use parallel decode")
	decodeCmd.Flags().IntVarP(&bitsPerChannel, "bits-per-channel", "b", 1, "number of LSBs to use per color channel (1-8)")
	decodeCmd.Flags().IntVarP(&channels, "channels", "c", 3, "number of color channels to use: 1=R, 2=R+G, 3=R+G+B")
	decodeCmd.MarkFlagRequired("password")

	capacityCmd.Flags().StringVarP(
		&capacityFlags.inputImage, "input_image", "i", "", "PNG image to measure.",
	)
	capacityCmd.MarkFlagRequired("input_image")

	testVisualCmd.Flags().StringVarP(
		&testVisualFlags.inputImage, "input_image", "i", "", "Carrier PNG image.",
	)
	testVisualCmd.Flags().StringVarP(
		&testVisualFlags.outputDir, "output_dir", "o", "", "Directory to write output images into.",
	)
	testVisualCmd.Flags().StringVarP(
		&testVisualFlags.key, "password", "p", "", "passphrase used for encoding.",
	)
	testVisualCmd.MarkFlagRequired("input_image")
	testVisualCmd.MarkFlagRequired("output_dir")
	testVisualCmd.MarkFlagRequired("password")

	rootCmd.AddCommand(encodeCmd)
	rootCmd.AddCommand(decodeCmd)
	rootCmd.AddCommand(capacityCmd)
	rootCmd.AddCommand(testVisualCmd)
}

func toDrawImage(src image.Image) draw.Image {
	bounds := src.Bounds()
	cimg := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(cimg, cimg.Bounds(), src, bounds.Min, draw.Src)
	return cimg
}

func runEncode() error {
	if bitsPerChannel < 1 || bitsPerChannel > 8 {
		return fmt.Errorf("--bits-per-channel must be between 1 and 8, got %d", bitsPerChannel)
	}
	if channels < 1 || channels > 3 {
		return fmt.Errorf("--channels must be between 1 and 3, got %d", channels)
	}

	f, err := os.Open(encoderFlags.inputImage)
	if err != nil {
		return err
	}

	src, err := png.Decode(bufio.NewReader(f))
	if err != nil {
		return err
	}

	cimg := toDrawImage(src)
	fmsg, err := os.Open(encoderFlags.inputMessage)
	if err != nil {
		return err
	}

	out, err := os.Create(encoderFlags.outputImage)
	if err != nil {
		return fmt.Errorf("unable to create output file: %w", err)
	}
	defer out.Close()

	if parallel {
		err = steg.EncodeParallel(cimg, []byte(encoderFlags.key), bufio.NewReader(fmsg), bitsPerChannel, channels)
	} else {
		err = steg.Encode(cimg, []byte(encoderFlags.key), bufio.NewReader(fmsg), bitsPerChannel, channels)
	}
	if err != nil {
		return err
	}

	err = png.Encode(out, cimg)
	if err != nil {
		log.Fatal(err)
	}

	return nil
}

func runDecode() error {
	if bitsPerChannel < 1 || bitsPerChannel > 8 {
		return fmt.Errorf("--bits-per-channel must be between 1 and 8, got %d", bitsPerChannel)
	}
	if channels < 1 || channels > 3 {
		return fmt.Errorf("--channels must be between 1 and 3, got %d", channels)
	}

	f, err := os.Open(decoderFlags.inputFile)
	if err != nil {
		return err
	}

	src, err := png.Decode(bufio.NewReader(f))
	if err != nil {
		return err
	}

	out, err := os.Create(decoderFlags.outputFile)
	if err != nil {
		return fmt.Errorf("unable to create output file: %w", err)
	}
	defer out.Close()

	var b []byte
	if parallel {
		b, err = steg.DecodeParallel(toDrawImage(src), []byte(decoderFlags.key), bitsPerChannel, channels)
	} else {
		b, err = steg.Decode(toDrawImage(src), []byte(decoderFlags.key), bitsPerChannel, channels)
	}
	if err != nil {
		return err
	}

	_, err = out.Write(b)
	if err != nil {
		return err
	}

	return nil
}

// imageCapacity returns the usable byte capacity for the given image dimensions,
// channel count, and bits per channel. The 40-byte overhead covers the 4-byte
// plaintext nonce, 4-byte encrypted length, and 32-byte encrypted HMAC tag.
func imageCapacity(w, h, ch, bpc int) int {
	total := w * h * ch * bpc / 8
	if total <= 40 {
		return 0
	}
	return total - 40
}

func runCapacity() error {
	f, err := os.Open(capacityFlags.inputImage)
	if err != nil {
		return err
	}
	defer f.Close()

	src, err := png.Decode(bufio.NewReader(f))
	if err != nil {
		return err
	}

	b := src.Bounds()
	w, h := b.Max.X, b.Max.Y

	fmt.Printf("%s — %d × %d px\n\n", filepath.Base(capacityFlags.inputImage), w, h)

	bpcValues := []int{1, 2, 4, 8}
	chNames := []string{"1 channel  (R)      ", "2 channels (R+G)    ", "3 channels (R+G+B)  "}

	const col = 15
	fmt.Printf("%-22s", "")
	for _, bpc := range bpcValues {
		label := fmt.Sprintf("%d bits/ch", bpc)
		fmt.Printf("%*s", col, label)
	}
	fmt.Println()

	for ch := 1; ch <= 3; ch++ {
		fmt.Printf("  %s", chNames[ch-1])
		for _, bpc := range bpcValues {
			cap := imageCapacity(w, h, ch, bpc)
			fmt.Printf("%*s", col, humanBytes(cap))
		}
		fmt.Println()
	}

	fmt.Println("\nOverhead: 40 B (4 nonce + 4 length + 32 HMAC).")
	return nil
}

// humanBytes formats n as a human-readable byte size (B, KB, MB, GB).
func humanBytes(n int) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.2f KB", float64(n)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// formatBytes formats an integer with thousands separators.
func formatBytes(n int) string {
	s := fmt.Sprintf("%d", n)
	out := make([]byte, 0, len(s)+len(s)/3)
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(c))
	}
	return string(out)
}

func runTestVisual() error {
	// Open and decode the carrier image once; we'll copy it for each run.
	f, err := os.Open(testVisualFlags.inputImage)
	if err != nil {
		return err
	}
	defer f.Close()

	src, err := png.Decode(bufio.NewReader(f))
	if err != nil {
		return err
	}

	if err = os.MkdirAll(testVisualFlags.outputDir, 0755); err != nil {
		return fmt.Errorf("cannot create output directory: %w", err)
	}

	b := src.Bounds()
	w, h := b.Max.X, b.Max.Y
	pass := []byte(testVisualFlags.key)

	bpcValues := []int{1, 2, 4, 8}
	total := 3 * len(bpcValues)
	written := 0

	fmt.Printf("Generating %d test images in %s ...\n", total, testVisualFlags.outputDir)

	for ch := 1; ch <= 3; ch++ {
		for _, bpc := range bpcValues {
			cap := imageCapacity(w, h, ch, bpc)
			name := fmt.Sprintf("visual_ch%d_b%d.png", ch, bpc)
			outPath := filepath.Join(testVisualFlags.outputDir, name)

			if cap <= 0 {
				fmt.Printf("  ch=%d  bpc=%d  →  %-26s (skipped — image too small)\n", ch, bpc, name)
				continue
			}

			// Build repeating payload at full capacity.
			payload := make([]byte, cap)
			for i := range payload {
				payload[i] = byte(i % 256)
			}

			// Fresh copy of the carrier so writes don't accumulate across runs.
			cimg := toDrawImage(src)

			if err = steg.Encode(cimg, pass, bytes.NewReader(payload), bpc, ch); err != nil {
				return fmt.Errorf("encode ch=%d bpc=%d: %w", ch, bpc, err)
			}

			outF, err := os.Create(outPath)
			if err != nil {
				return fmt.Errorf("create %s: %w", outPath, err)
			}
			if err = png.Encode(outF, cimg); err != nil {
				outF.Close()
				return fmt.Errorf("png encode %s: %w", outPath, err)
			}
			outF.Close()

			fmt.Printf("  ch=%d  bpc=%d  →  %-26s (%s bytes)\n", ch, bpc, name, formatBytes(cap))
			written++
		}
	}

	fmt.Printf("Done. %d images written.\n", written)
	return nil
}
