package imageio

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"strings"

	"github.com/pableeee/steg/steg"
)

// ImageFormat represents supported image formats
type ImageFormat string

const (
	FormatPNG ImageFormat = "png"
	FormatJPEG ImageFormat = "jpeg"
	FormatUnknown ImageFormat = "unknown"
)

// DetectImageFormat detects the image format from file extension or magic bytes
func DetectImageFormat(filename string, file io.Reader) (ImageFormat, error) {
	// First try extension
	ext := strings.ToLower(filename)
	if strings.HasSuffix(ext, ".png") {
		return FormatPNG, nil
	}
	if strings.HasSuffix(ext, ".jpg") || strings.HasSuffix(ext, ".jpeg") {
		return FormatJPEG, nil
	}

	// Try magic bytes
	magicBytes := make([]byte, 8)
	if f, ok := file.(*os.File); ok {
		pos, _ := f.Seek(0, io.SeekCurrent)
		defer f.Seek(pos, io.SeekStart)
		n, _ := f.Read(magicBytes)
		f.Seek(pos, io.SeekStart)
		if n >= 8 {
			if bytes.HasPrefix(magicBytes, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}) {
				return FormatPNG, nil
			}
			if bytes.HasPrefix(magicBytes, []byte{0xFF, 0xD8, 0xFF}) {
				return FormatJPEG, nil
			}
		}
	} else {
		// For generic readers, try to read magic bytes
		peekBytes := make([]byte, 8)
		readSeeker, ok := file.(io.ReadSeeker)
		if ok {
			pos, _ := readSeeker.Seek(0, io.SeekCurrent)
			defer readSeeker.Seek(pos, io.SeekStart)
			n, _ := readSeeker.Read(peekBytes)
			readSeeker.Seek(pos, io.SeekStart)
			if n >= 3 {
				if bytes.HasPrefix(peekBytes, []byte{0x89, 0x50, 0x4E, 0x47}) {
					return FormatPNG, nil
				}
				if bytes.HasPrefix(peekBytes, []byte{0xFF, 0xD8, 0xFF}) {
					return FormatJPEG, nil
				}
			}
		}
	}

	return FormatUnknown, fmt.Errorf("unable to detect image format")
}

// DecodeImage decodes an image from a reader using format detection
func DecodeImage(filename string, file io.Reader) (image.Image, ImageFormat, error) {
	format, err := DetectImageFormat(filename, file)
	if err != nil {
		return nil, FormatUnknown, err
	}

	var img image.Image
	switch format {
	case FormatPNG:
		img, err = png.Decode(file)
	case FormatJPEG:
		img, err = jpeg.Decode(file)
	default:
		return nil, FormatUnknown, &steg.ErrInvalidFormat{
			Format: string(format),
			Reason: "format not supported for decoding",
		}
	}

	if err != nil {
		return nil, format, fmt.Errorf("failed to decode %s image: %w", format, err)
	}

	return img, format, nil
}

// EncodeImage encodes an image to a writer in the specified format
func EncodeImage(img image.Image, format ImageFormat, w io.Writer) error {
	switch format {
	case FormatPNG:
		return png.Encode(w, img)
	case FormatJPEG:
		// Use quality 95 for JPEG to minimize compression artifacts
		// Note: JPEG is lossy and may corrupt steganographic data
		return jpeg.Encode(w, img, &jpeg.Options{Quality: 95})
	default:
		return &steg.ErrInvalidFormat{
			Format: string(format),
			Reason: "format not supported for encoding",
		}
	}
}

// IsLossyFormat returns true if the format uses lossy compression
func IsLossyFormat(format ImageFormat) bool {
	return format == FormatJPEG
}
