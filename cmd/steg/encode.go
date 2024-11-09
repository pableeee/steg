package main

import (
	"bufio"
	"fmt"
	"image/png"
	"os"

	"github.com/pableeee/steg/steg"
	"github.com/spf13/cobra"
)

var (
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

	return nil
}
