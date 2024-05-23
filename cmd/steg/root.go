package main

import (
	"bufio"
	"fmt"
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
		outputImage string
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
		outputFile string
	}{}
)

func init() {
	encodeCmd.Flags().StringVar(
		&encoderFlags.inputImage, "input_image", "", "Input image used as medium.",
	)
	encodeCmd.Flags().StringVar(
		&encoderFlags.inputMessage, "input_message", "", "Message the will be encoded into the output image.",
	)
	encodeCmd.Flags().StringVar(
		&encoderFlags.outputImage, "output_image", "", "Image containing the coded message.",
	)

	decodeCmd.Flags().StringVar(
		&decoderFlags.inputFile, "input_image", "", "Image containing the coded message.",
	)
	decodeCmd.Flags().StringVar(
		&decoderFlags.outputFile, "output_file", "", "Path for the output file containing the coded data.",
	)
	rootCmd.AddCommand(encodeCmd)
	rootCmd.AddCommand(decodeCmd)
}

func runEncode() error {
	f, err := os.Open(encoderFlags.inputImage)
	if err != nil {
		return err
	}

	img, err := png.Decode(bufio.NewReader(f))
	if err != nil {
		return err
	}

	cimg, ok := img.(steg.ChangeableImage)
	if !ok {
		return fmt.Errorf("image its not changeable: %w", err)
	}

	fmsg, err := os.Open(encoderFlags.inputMessage)
	if err != nil {
		return err
	}

	out, err := os.Create(encoderFlags.outputImage)
	if err != nil {
		return fmt.Errorf("unable to create output file: %w", err)
	}
	defer out.Close()

	if err = steg.Encode(cimg, nil, bufio.NewReader(fmsg)); err != nil {
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

	img, err := png.Decode(bufio.NewReader(f))
	if err != nil {
		return err
	}

	cimg, ok := img.(steg.ChangeableImage)
	if !ok {
		return fmt.Errorf("image its not changeable: %w", err)
	}

	out, err := os.Create(decoderFlags.outputFile)
	if err != nil {
		return fmt.Errorf("unable to create output file: %w", err)
	}
	defer out.Close()

	b, err := steg.Decode(cimg, nil)
	if err != nil {
		return err
	}

	_, err = out.Write(b)
	if err != nil {
		return err
	}

	return nil
}
