package main

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"

	"github.com/pableeee/steg/cipher"
	"github.com/pableeee/steg/cursors"
	"github.com/pableeee/steg/steg"
	"github.com/pableeee/steg/steg/container"
	"github.com/pableeee/steg/steg/imageio"
)

// getChannelOptions returns the appropriate cursor options for the given number of channels
// Channels are selected in order: 1=G, 2=G+B, 3=R+G+B
func getChannelOptions(numChannels int) []cursors.Option {
	var opts []cursors.Option
	switch numChannels {
	case 1:
		// Use Green channel only
		opts = append(opts, cursors.UseGreenBit())
	case 2:
		// Use Green and Blue channels
		opts = append(opts, cursors.UseGreenBit(), cursors.UseBlueBit())
	case 3:
		// Use all three channels (R, G, B)
		// Since R_Bit is default and UseGreenBit/UseBlueBit use |=, 
		// calling both gives us R|G|B which is what we want
		opts = append(opts, cursors.UseGreenBit(), cursors.UseBlueBit())
	}
	return opts
}

// getChannelName returns a string representation of the channel configuration
func getChannelName(numChannels int) string {
	switch numChannels {
	case 1:
		return "G"
	case 2:
		return "GB"
	case 3:
		return "RGB"
	default:
		return fmt.Sprintf("%dch", numChannels)
	}
}

func runTestVisual() error {
	// Validate inputs
	if testVisualFlags.inputImage == "" {
		return fmt.Errorf("input_image is required")
	}
	if testVisualFlags.outputDir == "" {
		return fmt.Errorf("output_dir is required")
	}
	if testVisualFlags.maxChannels < 1 || testVisualFlags.maxChannels > 3 {
		return fmt.Errorf("max_channels must be between 1 and 3, got: %d", testVisualFlags.maxChannels)
	}
	if testVisualFlags.maxBits < 1 || testVisualFlags.maxBits > 3 {
		return fmt.Errorf("max_bits must be between 1 and 3, got: %d", testVisualFlags.maxBits)
	}

	// Create output directory if it doesn't exist
	if err := os.MkdirAll(testVisualFlags.outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Open and decode input image
	f, err := os.Open(testVisualFlags.inputImage)
	if err != nil {
		return fmt.Errorf("failed to open input image: %w", err)
	}
	defer f.Close()

	src, _, err := imageio.DecodeImage(testVisualFlags.inputImage, bufio.NewReader(f))
	if err != nil {
		return fmt.Errorf("failed to decode image: %w", err)
	}

	// Get base filename without extension
	baseName := strings.TrimSuffix(filepath.Base(testVisualFlags.inputImage), filepath.Ext(testVisualFlags.inputImage))

	fmt.Printf("Generating test images for: %s\n", testVisualFlags.inputImage)
	fmt.Printf("Output directory: %s\n", testVisualFlags.outputDir)
	fmt.Printf("Configurations: %d channels × %d bits = %d images\n\n",
		testVisualFlags.maxChannels, testVisualFlags.maxBits,
		testVisualFlags.maxChannels*testVisualFlags.maxBits)

	// Generate all combinations
	for numChannels := 1; numChannels <= testVisualFlags.maxChannels; numChannels++ {
		for bitsPerChannel := 1; bitsPerChannel <= testVisualFlags.maxBits; bitsPerChannel++ {
			// Create a fresh copy of the image for this test
			cimg := toDrawImage(src)

			// Create cursor with this configuration for capacity calculation
			opts := getChannelOptions(numChannels)
			opts = append(opts, cursors.WithBitsPerChannel(bitsPerChannel))
			opts = append(opts, cursors.WithSeed(testVisualFlags.seed))

			cur := cursors.NewRNGCursor(cimg, opts...)

			// Determine data size to encode
			capacityBits := cur.Capacity()
			capacityBytes := capacityBits / 8
			containerOverhead := int64(20) // 4 bytes length + 16 bytes MD5
			maxUsableBytes := capacityBytes - containerOverhead

			var dataSize int64
			if testVisualFlags.dataSize > 0 {
				dataSize = testVisualFlags.dataSize
				if dataSize > maxUsableBytes {
					fmt.Printf("⚠️  Warning: Requested data size (%d bytes) exceeds capacity (%d bytes) for %dch-%dbit, using maximum\n",
						dataSize, maxUsableBytes, numChannels, bitsPerChannel)
					dataSize = maxUsableBytes
				}
			} else {
				// Use 80% of capacity
				dataSize = (maxUsableBytes * 80) / 100
				if dataSize < 0 {
					dataSize = 0
				}
			}

			if dataSize <= 0 {
				fmt.Printf("⏭️  Skipping %dch-%dbit: insufficient capacity\n", numChannels, bitsPerChannel)
				continue
			}

			// Generate pseudo-random data with the specified seed
			rng := rand.New(rand.NewSource(testVisualFlags.seed))
			testData := make([]byte, dataSize)
			for i := range testData {
				testData[i] = byte(rng.Intn(256))
			}

			// Encode the data using the same seed derivation as regular encoding
			seedVal, err := steg.DeriveSeedFromPassword([]byte(testVisualFlags.key))
			if err != nil {
				return fmt.Errorf("failed to derive seed: %w", err)
			}

			// Create cursor for encoding with password seed
			encodeOpts := getChannelOptions(numChannels)
			encodeOpts = append(encodeOpts, cursors.WithBitsPerChannel(bitsPerChannel))
			encodeOpts = append(encodeOpts, cursors.WithSeed(seedVal))
			encodeCur := cursors.NewRNGCursor(cimg, encodeOpts...)

			// Validate capacity
			hashFn := md5.New()
			requiredBytes := container.CalculateRequiredCapacity(dataSize, hashFn.Size())
			availableBytes := encodeCur.Capacity() / 8

			if requiredBytes > availableBytes {
				fmt.Printf("⚠️  Warning: %dch-%dbit capacity (%d bytes) insufficient for data (%d bytes), skipping\n",
					numChannels, bitsPerChannel, availableBytes, requiredBytes)
				continue
			}

			// Encode
			cm := cursors.CipherMiddleware(encodeCur, cipher.NewCipher(0, []byte(testVisualFlags.key)))
			adapter := cursors.CursorAdapter(cm)
			if err := container.WritePayload(adapter, bytes.NewReader(testData), hashFn); err != nil {
				return fmt.Errorf("failed to encode data for %dch-%dbit: %w", numChannels, bitsPerChannel, err)
			}

			// Generate output filename
			channelName := getChannelName(numChannels)
			outputFilename := fmt.Sprintf("%s_%s_%dbit.png", baseName, channelName, bitsPerChannel)
			outputPath := filepath.Join(testVisualFlags.outputDir, outputFilename)

			// Save the encoded image (always as PNG for consistency)
			out, err := os.Create(outputPath)
			if err != nil {
				return fmt.Errorf("failed to create output file %s: %w", outputPath, err)
			}

			// Always save as PNG regardless of input format for consistency
			if err := imageio.EncodeImage(cimg, imageio.FormatPNG, out); err != nil {
				out.Close()
				return fmt.Errorf("failed to encode image %s: %w", outputPath, err)
			}
			out.Close()

			fmt.Printf("✓ Generated: %s (%d bytes encoded, %d bytes capacity)\n",
				outputFilename, dataSize, capacityBytes)
		}
	}

	fmt.Printf("\n✅ All test images generated successfully in: %s\n", testVisualFlags.outputDir)
	return nil
}
