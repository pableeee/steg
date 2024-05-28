package main

import (
	"bufio"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"log"
	"os"

	"github.com/pableeee/steg/steg"
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
	rootCmd.AddCommand(encodeCmd)
	rootCmd.AddCommand(decodeCmd)
}

func toDrawImage(src image.Image) draw.Image {
	bounds := src.Bounds()
	cimg := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(cimg, cimg.Bounds(), src, bounds.Min, draw.Src)

	return cimg
}

func runEncode() error {
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

	if err = steg.Encode(cimg, []byte(encoderFlags.key), bufio.NewReader(fmsg)); err != nil {
		return err
	}

	err = png.Encode(out, cimg)
	if err != nil {
		log.Fatal(err)
	}

	return nil
}

func runDecode() error {
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

	b, err := steg.Decode(toDrawImage(src), []byte(decoderFlags.key))
	if err != nil {
		return err
	}

	_, err = out.Write(b)
	if err != nil {
		return err
	}

	return nil
}
