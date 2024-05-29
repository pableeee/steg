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
	decodeCmd.Flags().StringVarP(
		&decoderFlags.inputFile, "input_image", "i", "", "Image containing the coded message.",
	)
	decodeCmd.Flags().StringVarP(
		&decoderFlags.outputFile, "output_file", "o", "", "Path for the output file containing the coded data.",
	)
	decodeCmd.Flags().StringVarP(
		&decoderFlags.key, "password", "p", "YELLOW SUBMARINE", "passphrase to extract the contents.",
	)
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
