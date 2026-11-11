package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"io/fs"
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
			cmd.SilenceUsage = true // flags parsed: errors from here on are not usage errors
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
			cmd.SilenceUsage = true // flags parsed: errors from here on are not usage errors
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
			cmd.SilenceUsage = true // flags parsed: errors from here on are not usage errors
			return runCapacity()
		},
	}

	capacityFlags = struct{ inputImage string }{}

	testVisualCmd = &cobra.Command{
		Use:   "test-visual",
		Short: "Generate test images at every encoding intensity for visual comparison",
		Long:  "Encodes the input image filled to capacity at every (channels × bits-per-channel) combination and writes the results to an output directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true // flags parsed: errors from here on are not usage errors
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
	encodeCmd.MarkFlagRequired("input_image")
	encodeCmd.MarkFlagRequired("input_file")
	encodeCmd.MarkFlagRequired("output_image")
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
	decodeCmd.MarkFlagRequired("input_image")
	decodeCmd.MarkFlagRequired("output_file")

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

// supportedOutputExts lists the image extensions writeImage can produce.
var supportedOutputExts = []string{".png", ".bmp", ".tif", ".tiff"}

// checkOutputExt rejects an output path whose extension writeImage does not
// recognise, so a name like out.jpg is not silently filled with PNG bytes.
func checkOutputExt(path string) error {
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range supportedOutputExts {
		if ext == e {
			return nil
		}
	}
	return fmt.Errorf("unsupported output image extension %q for %s: use one of %s",
		filepath.Ext(path), path, strings.Join(supportedOutputExts, ", "))
}

// writeImage encodes img to w as PNG, BMP, or TIFF based on the extension of
// path. path only selects the format; nothing is opened.
func writeImage(w io.Writer, path string, img image.Image) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return png.Encode(w, img)
	case ".bmp":
		return bmp.Encode(w, img)
	case ".tif", ".tiff":
		return tiff.Encode(w, img, nil)
	default:
		return checkOutputExt(path)
	}
}

// encodeImage writes img to path as PNG, BMP, or TIFF based on extension.
func encodeImage(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("unable to create output file: %w", err)
	}
	if err = writeImage(f, path, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// isOpaque reports whether every pixel of img has full alpha.
func isOpaque(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a != 0xffff {
				return false
			}
		}
	}
	return true
}

// checkCarrierAlpha rejects carriers with any non-opaque pixel, whatever the
// output format. The cursor writes into toDrawImage's alpha-premultiplied
// bytes; PNG and BMP store straight alpha, so their encoders un-premultiply
// non-opaque pixels and destroy the hidden bits (alpha-0 pixels collapse to
// 0,0,0,0). TIFF happens to keep the bytes verbatim, but the result holds
// invalid premultiplied pixels (colour above alpha) that give the embedding
// away and do not survive a re-save, so it is rejected too for consistency.
func checkCarrierAlpha(carrier image.Image, carrierPath string) error {
	if isOpaque(carrier) {
		return nil
	}
	return fmt.Errorf(
		"carrier image %s has transparency (non-opaque pixels); hidden data cannot be "+
			"stored reliably in transparent pixels. Remove the alpha channel first "+
			"(flatten the image onto an opaque background)",
		carrierPath)
}

// decodeFromFile reads the image at path and extracts the hidden payload. It
// is the single decode path shared by `steg decode` and encode verification.
func decodeFromFile(path string, pass []byte, bpc, ch int, par bool) ([]byte, error) {
	src, err := decodeImage(path)
	if err != nil {
		return nil, err
	}
	if par {
		return steg.DecodeParallel(toDrawImage(src), pass, bpc, ch)
	}
	return steg.Decode(toDrawImage(src), pass, bpc, ch)
}

// createTempSibling creates a new file next to path whose name keeps path's
// extension, so writeImage/decodeImage pick the same format. The file is
// created with perm (subject to umask). Encode passes 0666, like os.Create, so
// an image's permissions match what a direct write would have produced; decode
// passes 0600 because the recovered payload is the secret.
func createTempSibling(path string, perm os.FileMode) (*os.File, error) {
	dir, base := filepath.Split(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 0; i < 100; i++ {
		var rnd [6]byte
		if _, err := rand.Read(rnd[:]); err != nil {
			return nil, err
		}
		name := filepath.Join(dir, fmt.Sprintf(".%s.steg-tmp-%s%s", stem, hex.EncodeToString(rnd[:]), ext))
		f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, perm)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("unable to create temporary file next to %s", path)
}

// writeVerified writes img to outPath only after proving that the written
// file decodes back to payload. The image goes to a temporary sibling first;
// it is re-read through decodeFromFile and compared byte for byte, and only
// then renamed onto outPath. On any failure the temporary file is removed and
// a pre-existing file at outPath is left untouched.
func writeVerified(outPath string, img image.Image, pass, payload []byte, bpc, ch int, par bool) (err error) {
	tmp, err := createTempSibling(outPath, 0666)
	if err != nil {
		return fmt.Errorf("unable to create output file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			os.Remove(tmpName)
		}
	}()

	if err = writeImage(tmp, outPath, img); err != nil {
		tmp.Close()
		return fmt.Errorf("unable to write output image: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("unable to write output image: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("unable to write output image: %w", err)
	}

	got, derr := decodeFromFile(tmpName, pass, bpc, ch, par)
	if derr != nil {
		err = fmt.Errorf("verification failed: the written image does not decode (%v); "+
			"output not written", derr)
		return err
	}
	if !bytes.Equal(got, payload) {
		err = fmt.Errorf("verification failed: the written image decodes to different data; " +
			"output not written")
		return err
	}

	if err = os.Rename(tmpName, outPath); err != nil {
		return fmt.Errorf("unable to move verified image into place: %w", err)
	}
	return nil
}

// writeSecretFile writes data to path with mode 0600 (subject to umask). It
// writes a temporary sibling, syncs and closes it, and only then renames it
// onto path, so a failure never leaves a partial file and never touches an
// existing file at path.
//
// An existing path that is a symlink, device or FIFO is written through in
// place instead, as os.Create would: renaming over it would replace the link
// itself, or fail, rather than reach what it points to. That keeps idioms
// like -o /dev/stdout working (a symlink, possibly to a regular file when
// stdout is redirected).
func writeSecretFile(path string, data []byte) (err error) {
	if fi, statErr := os.Lstat(path); statErr == nil && !fi.Mode().IsRegular() && !fi.IsDir() {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
		if err != nil {
			return fmt.Errorf("unable to open output file: %w", err)
		}
		if _, err = f.Write(data); err != nil {
			f.Close()
			return fmt.Errorf("unable to write output file: %w", err)
		}
		if err = f.Close(); err != nil {
			return fmt.Errorf("unable to write output file: %w", err)
		}
		return nil
	}

	tmp, err := createTempSibling(path, 0600)
	if err != nil {
		return fmt.Errorf("unable to create output file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			os.Remove(tmpName)
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("unable to write output file: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("unable to write output file: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("unable to write output file: %w", err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("unable to move output file into place: %w", err)
	}
	return nil
}

func runEncode() error {
	if bitsPerChannel < 1 || bitsPerChannel > 8 {
		return fmt.Errorf("--bits-per-channel must be between 1 and 8, got %d", bitsPerChannel)
	}
	if channels < 1 || channels > 3 {
		return fmt.Errorf("--channels must be between 1 and 3, got %d", channels)
	}
	if err := checkOutputExt(encoderFlags.outputImage); err != nil {
		return err
	}

	// Load and check the carrier before prompting for a password, so an
	// unusable carrier fails fast.
	src, err := decodeImage(encoderFlags.inputImage)
	if err != nil {
		return err
	}
	if err = checkCarrierAlpha(src, encoderFlags.inputImage); err != nil {
		return err
	}

	// Steg.Encode reads the whole payload into memory anyway; holding it here
	// lets us compare it against what the written file decodes to.
	payload, err := os.ReadFile(encoderFlags.inputMessage)
	if err != nil {
		return err
	}

	pass, err := resolvePassword(encoderFlags.key, true)
	if err != nil {
		return err
	}

	cimg := toDrawImage(src)
	if parallel {
		err = steg.EncodeParallel(cimg, pass, bytes.NewReader(payload), bitsPerChannel, channels)
	} else {
		err = steg.Encode(cimg, pass, bytes.NewReader(payload), bitsPerChannel, channels)
	}
	if err != nil {
		return err
	}

	return writeVerified(encoderFlags.outputImage, cimg, pass, payload, bitsPerChannel, channels, parallel)
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

	// Decode before touching the output path: a wrong password must not leave a
	// truncated or empty file behind.
	b, err := decodeFromFile(decoderFlags.inputFile, pass, bitsPerChannel, channels, parallel)
	if err != nil {
		return err
	}

	return writeSecretFile(decoderFlags.outputFile, b)
}

func runCapacity() error {
	src, err := decodeImage(capacityFlags.inputImage)
	if err != nil {
		return err
	}

	w, h := src.Bounds().Dx(), src.Bounds().Dy()

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
			cap := steg.CapacityForDims(w, h, bpc, ch)
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
	if err = checkCarrierAlpha(src, testVisualFlags.inputImage); err != nil {
		return err
	}

	if err = os.MkdirAll(testVisualFlags.outputDir, 0755); err != nil {
		return fmt.Errorf("cannot create output directory: %w", err)
	}

	w, h := src.Bounds().Dx(), src.Bounds().Dy()
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
			cap := steg.CapacityForDims(w, h, bpc, ch)
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
