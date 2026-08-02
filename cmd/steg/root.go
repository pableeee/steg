package main

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/pableeee/steg/steg"
	"github.com/spf13/cobra"
	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
	"golang.org/x/term"
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
	// password is intentionally not required: resolvePassword falls back to
	// STEG_PASSWORD or an interactive prompt.

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

	capacityCmd.Flags().StringVarP(
		&capacityFlags.inputImage, "input_image", "i", "", "Image to measure (PNG, BMP, TIFF).",
	)
	capacityCmd.MarkFlagRequired("input_image")

	testVisualCmd.Flags().StringVarP(
		&testVisualFlags.inputImage, "input_image", "i", "", "Carrier image (PNG, BMP, TIFF).",
	)
	testVisualCmd.Flags().StringVarP(
		&testVisualFlags.outputDir, "output_dir", "o", "", "Directory to write output images into.",
	)
	testVisualCmd.Flags().StringVarP(
		&testVisualFlags.key, "password", "p", "", "passphrase used for encoding.",
	)
	testVisualCmd.MarkFlagRequired("input_image")
	testVisualCmd.MarkFlagRequired("output_dir")

	rootCmd.AddCommand(encodeCmd)
	rootCmd.AddCommand(decodeCmd)
	rootCmd.AddCommand(capacityCmd)
	rootCmd.AddCommand(testVisualCmd)
	rootCmd.AddCommand(detectCmd)
}

// resolvePassword returns the password to use, preferring an explicit flag,
// then the STEG_PASSWORD environment variable, and finally an interactive
// prompt. A password passed via --password is visible to every other process on
// the machine through the process table and is recorded in shell history, so
// the prompt is the safer default when the terminal is interactive.
func resolvePassword(flagValue string, confirm bool) ([]byte, error) {
	if flagValue != "" {
		return []byte(flagValue), nil
	}
	if env := os.Getenv("STEG_PASSWORD"); env != "" {
		return []byte(env), nil
	}
	if !term.IsTerminal(int(syscall.Stdin)) {
		return nil, fmt.Errorf(
			"no password provided: pass --password, set STEG_PASSWORD, or run on a terminal")
	}

	fmt.Fprint(os.Stderr, "Password: ")
	pass, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("unable to read password: %w", err)
	}
	if len(pass) == 0 {
		return nil, fmt.Errorf("password must not be empty")
	}

	if confirm {
		fmt.Fprint(os.Stderr, "Confirm password: ")
		again, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return nil, fmt.Errorf("unable to read password: %w", err)
		}
		if !bytes.Equal(pass, again) {
			return nil, fmt.Errorf("passwords do not match")
		}
	}
	return pass, nil
}

func toDrawImage(src image.Image) draw.Image {
	bounds := src.Bounds()
	cimg := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(cimg, cimg.Bounds(), src, bounds.Min, draw.Src)
	return cimg
}

// decodeImage opens path and decodes it as PNG, BMP, or TIFF based on extension.
func decodeImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	switch strings.ToLower(filepath.Ext(path)) {
	case ".bmp":
		return bmp.Decode(r)
	case ".tif", ".tiff":
		return tiff.Decode(r)
	default:
		return png.Decode(r)
	}
}

// encodeImage writes img to path as PNG, BMP, or TIFF based on extension.
func encodeImage(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("unable to create output file: %w", err)
	}
	defer f.Close()
	switch strings.ToLower(filepath.Ext(path)) {
	case ".bmp":
		return bmp.Encode(f, img)
	case ".tif", ".tiff":
		return tiff.Encode(f, img, nil)
	default:
		return png.Encode(f, img)
	}
}

func runEncode() error {
	if bitsPerChannel < 1 || bitsPerChannel > 8 {
		return fmt.Errorf("--bits-per-channel must be between 1 and 8, got %d", bitsPerChannel)
	}
	if channels < 1 || channels > 3 {
		return fmt.Errorf("--channels must be between 1 and 3, got %d", channels)
	}

	pass, err := resolvePassword(encoderFlags.key, true)
	if err != nil {
		return err
	}

	src, err := decodeImage(encoderFlags.inputImage)
	if err != nil {
		return err
	}

	cimg := toDrawImage(src)
	fmsg, err := os.Open(encoderFlags.inputMessage)
	if err != nil {
		return err
	}
	defer fmsg.Close()

	if parallel {
		err = steg.EncodeParallel(cimg, pass, bufio.NewReader(fmsg), bitsPerChannel, channels)
	} else {
		err = steg.Encode(cimg, pass, bufio.NewReader(fmsg), bitsPerChannel, channels)
	}
	if err != nil {
		return err
	}

	return encodeImage(encoderFlags.outputImage, cimg)
}

func runDecode() error {
	if bitsPerChannel < 1 || bitsPerChannel > 8 {
		return fmt.Errorf("--bits-per-channel must be between 1 and 8, got %d", bitsPerChannel)
	}
	if channels < 1 || channels > 3 {
		return fmt.Errorf("--channels must be between 1 and 3, got %d", channels)
	}

	pass, err := resolvePassword(decoderFlags.key, false)
	if err != nil {
		return err
	}

	src, err := decodeImage(decoderFlags.inputFile)
	if err != nil {
		return err
	}

	// Decode before touching the output path: a wrong password must not leave a
	// truncated or empty file behind.
	var b []byte
	if parallel {
		b, err = steg.DecodeParallel(toDrawImage(src), pass, bitsPerChannel, channels)
	} else {
		b, err = steg.Decode(toDrawImage(src), pass, bitsPerChannel, channels)
	}
	if err != nil {
		return err
	}

	out, err := os.Create(decoderFlags.outputFile)
	if err != nil {
		return fmt.Errorf("unable to create output file: %w", err)
	}
	defer out.Close()

	if _, err = out.Write(b); err != nil {
		return err
	}

	return nil
}

func runCapacity() error {
	src, err := decodeImage(capacityFlags.inputImage)
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
			cap := steg.CapacityForDims(w, h, ch, bpc)
			fmt.Printf("%*s", col, humanBytes(cap))
		}
		fmt.Println()
	}

	fmt.Printf("\nOverhead: %d B (16 plaintext-salt + 4 container-length + 4 real-length + 32 HMAC).\n",
		steg.Overhead)
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
	// Decode the carrier image once; we'll copy it for each run.
	src, err := decodeImage(testVisualFlags.inputImage)
	if err != nil {
		return err
	}

	if err = os.MkdirAll(testVisualFlags.outputDir, 0755); err != nil {
		return fmt.Errorf("cannot create output directory: %w", err)
	}

	b := src.Bounds()
	w, h := b.Max.X, b.Max.Y
	pass, err := resolvePassword(testVisualFlags.key, false)
	if err != nil {
		return err
	}

	bpcValues := []int{1, 2, 4, 8}
	total := 3 * len(bpcValues)
	written := 0

	fmt.Printf("Generating %d test images in %s ...\n", total, testVisualFlags.outputDir)

	for ch := 1; ch <= 3; ch++ {
		for _, bpc := range bpcValues {
			cap := steg.CapacityForDims(w, h, ch, bpc)
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

			if err = encodeImage(outPath, cimg); err != nil {
				return fmt.Errorf("encode %s: %w", outPath, err)
			}

			fmt.Printf("  ch=%d  bpc=%d  →  %-26s (%s bytes)\n", ch, bpc, name, formatBytes(cap))
			written++
		}
	}

	fmt.Printf("Done. %d images written.\n", written)
	return nil
}
