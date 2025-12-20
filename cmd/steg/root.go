package main

import (
	"bufio"
	"fmt"
	"image"
	"image/draw"
	"os"

	"github.com/pableeee/steg/cursors"
	"github.com/pableeee/steg/steg"
	"github.com/pableeee/steg/steg/imageio"
	"github.com/spf13/cobra"
)

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
		Short: "Shows the encoding capacity of an image",
		Long:  "Shows the encoding capacity of an image in bytes. This indicates how much data can be encoded into the image.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCapacity()
		},
	}

	capacityFlags = struct {
		inputImage string
		bitsPerChannel int
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
		&encoderFlags.key, "password", "p", "YELLOW SUBMARINE", "passphrase to cipher the contents.",
	)

	decodeCmd.Flags().StringVarP(
		&decoderFlags.inputFile, "input_image", "i", "", "Image containing the coded message.",
	)
	decodeCmd.Flags().StringVarP(
		&decoderFlags.outputFile, "output_file", "o", "", "Path for the output file containing the coded data.",
	)
	decodeCmd.Flags().StringVarP(
		&decoderFlags.key, "password", "p", "YELLOW SUBMARINE", "passphrase to extract the contents.",
	)

	capacityCmd.Flags().StringVarP(
		&capacityFlags.inputImage, "input_image", "i", "", "Input image to check capacity.",
	)
	capacityCmd.Flags().IntVarP(
		&capacityFlags.bitsPerChannel, "bits_per_channel", "b", 1, "Number of bits per channel to use (1-3, default: 1).",
	)

	rootCmd.AddCommand(encodeCmd)
	rootCmd.AddCommand(decodeCmd)
	rootCmd.AddCommand(capacityCmd)
}

func toDrawImage(src image.Image) draw.Image {
	bounds := src.Bounds()
	cimg := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(cimg, cimg.Bounds(), src, bounds.Min, draw.Src)

	return cimg
}

func runEncode() error {
	// Open input image
	f, err := os.Open(encoderFlags.inputImage)
	if err != nil {
		return fmt.Errorf("failed to open input image: %w", err)
	}
	defer f.Close()

	// Detect and decode image format
	src, format, err := imageio.DecodeImage(encoderFlags.inputImage, bufio.NewReader(f))
	if err != nil {
		return fmt.Errorf("failed to decode image: %w", err)
	}

	// Warn about lossy formats
	if imageio.IsLossyFormat(format) {
		fmt.Fprintf(os.Stderr, "Warning: %s is a lossy format. Steganographic data may be corrupted.\n", format)
		fmt.Fprintf(os.Stderr, "Consider using PNG format for better reliability.\n")
	}

	cimg := toDrawImage(src)
	
	// Open message file
	fmsg, err := os.Open(encoderFlags.inputMessage)
	if err != nil {
		return fmt.Errorf("failed to open message file: %w", err)
	}
	defer fmsg.Close()

	// Encode the message
	if err = steg.Encode(cimg, []byte(encoderFlags.key), bufio.NewReader(fmsg)); err != nil {
		return fmt.Errorf("encoding failed: %w", err)
	}

	// Determine output format (use same as input, or detect from extension)
	outputFormat, err := imageio.DetectImageFormat(encoderFlags.outputImage, nil)
	if err != nil {
		// Default to input format if we can't detect from extension
		outputFormat = format
	}

	// Create output file
	out, err := os.Create(encoderFlags.outputImage)
	if err != nil {
		return fmt.Errorf("unable to create output file: %w", err)
	}
	defer out.Close()

	// Encode the image
	if err = imageio.EncodeImage(cimg, outputFormat, out); err != nil {
		return fmt.Errorf("failed to encode output image: %w", err)
	}

	return nil
}

func runDecode() error {
	// Open input image
	f, err := os.Open(decoderFlags.inputFile)
	if err != nil {
		return fmt.Errorf("failed to open input image: %w", err)
	}
	defer f.Close()

	// Detect and decode image format
	src, _, err := imageio.DecodeImage(decoderFlags.inputFile, bufio.NewReader(f))
	if err != nil {
		return fmt.Errorf("failed to decode image: %w", err)
	}

	// Decode the message
	b, err := steg.Decode(toDrawImage(src), []byte(decoderFlags.key))
	if err != nil {
		return fmt.Errorf("decoding failed: %w", err)
	}

	// Write output file
	out, err := os.Create(decoderFlags.outputFile)
	if err != nil {
		return fmt.Errorf("unable to create output file: %w", err)
	}
	defer out.Close()

	if _, err = out.Write(b); err != nil {
		return fmt.Errorf("failed to write output file: %w", err)
	}

	return nil
}

func runCapacity() error {
	// Open input image
	f, err := os.Open(capacityFlags.inputImage)
	if err != nil {
		return fmt.Errorf("failed to open input image: %w", err)
	}
	defer f.Close()

	// Detect and decode image format
	src, format, err := imageio.DecodeImage(capacityFlags.inputImage, bufio.NewReader(f))
	if err != nil {
		return fmt.Errorf("failed to decode image: %w", err)
	}

	cimg := toDrawImage(src)

	// Validate bits per channel
	if capacityFlags.bitsPerChannel < 1 || capacityFlags.bitsPerChannel > 3 {
		return fmt.Errorf("bits per channel must be between 1 and 3, got: %d", capacityFlags.bitsPerChannel)
	}

	// Create RNG cursor with same options as encoding (G and B channels)
	cur := cursors.NewRNGCursor(
		cimg,
		cursors.UseGreenBit(),
		cursors.UseBlueBit(),
		cursors.WithBitsPerChannel(capacityFlags.bitsPerChannel),
	)

	// Get capacity in bits
	capacityBits := cur.Capacity()
	capacityBytes := capacityBits / 8
	
	// Calculate usable capacity (accounting for container overhead)
	// Format overhead: 4 bytes (length) + 16 bytes (MD5 checksum) = 20 bytes
	containerOverhead := int64(20)
	usableBytes := capacityBytes - containerOverhead
	if usableBytes < 0 {
		usableBytes = 0
	}

	// Display results
	fmt.Printf("Image: %s\n", capacityFlags.inputImage)
	fmt.Printf("Format: %s\n", format)
	fmt.Printf("Dimensions: %d x %d pixels\n", cimg.Bounds().Dx(), cimg.Bounds().Dy())
	fmt.Printf("Channels used: Green, Blue (2 channels)\n")
	fmt.Printf("Bits per channel: %d\n", capacityFlags.bitsPerChannel)
	fmt.Printf("\n")
	fmt.Printf("Total capacity: %d bits (%d bytes)\n", capacityBits, capacityBytes)
	fmt.Printf("Container overhead: %d bytes (length + checksum)\n", containerOverhead)
	fmt.Printf("Usable capacity: %d bytes\n", usableBytes)
	
	// Display in human-readable format
	if usableBytes > 0 {
		fmt.Printf("\n")
		if usableBytes >= 1024*1024 {
			fmt.Printf("  ≈ %.2f MB\n", float64(usableBytes)/(1024*1024))
		} else if usableBytes >= 1024 {
			fmt.Printf("  ≈ %.2f KB\n", float64(usableBytes)/1024)
		}
	} else {
		fmt.Printf("\n⚠️  Image is too small to encode any data.\n")
	}

	return nil
}
