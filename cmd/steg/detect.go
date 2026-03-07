package main

import (
	"fmt"

	"github.com/pableeee/steg/steg/analysis"
	"github.com/spf13/cobra"
)

var detectFlags = struct{ inputImage string }{}

var detectCmd = &cobra.Command{
	Use:   "detect",
	Short: "Run steganalysis on an image to detect possible LSB steganography",
	Long: `Runs two complementary statistical tests on the image:

  Chi-square  Detects global LSB histogram uniformity. Natural images have
              unequal (2k)/(2k+1) pixel-value pair frequencies; LSB embedding
              equalises them. A high p-value (> 0.05) is suspicious.

  RS analysis Detects local pixel smoothness asymmetry. LSB embedding biases
              the response to the positive flipping mask (Rm) over the negative
              (Rnm). A positive asymmetry (Rm − Rnm > 0.01) is suspicious.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDetect()
	},
}

func init() {
	detectCmd.Flags().StringVarP(
		&detectFlags.inputImage, "input_image", "i", "", "Image to analyse (PNG, BMP, TIFF).",
	)
	detectCmd.MarkFlagRequired("input_image")
}

func runDetect() error {
	img, err := decodeImage(detectFlags.inputImage)
	if err != nil {
		return err
	}

	result := analysis.Analyze(img)

	fmt.Println("Chi-square analysis (high p-value = suspicious):")
	for _, r := range result.ChiSquare {
		label := "CLEAN"
		if r.Suspicious {
			label = "SUSPICIOUS"
		}
		fmt.Printf("  %s: χ²=%-10.2f  p=%.4f  [%s]\n", r.Channel, r.ChiSq, r.PValue, label)
	}

	fmt.Println("\nRS analysis (positive asymmetry = suspicious):")
	for _, r := range result.RS {
		label := "CLEAN"
		if r.Suspicious {
			label = "SUSPICIOUS"
		}
		fmt.Printf("  %s: Rm=%.4f  Sm=%.4f  R-m=%.4f  S-m=%.4f  asymmetry=%+.4f  [%s]\n",
			r.Channel, r.Rm, r.Sm, r.Rnm, r.Snm, r.Asymmetry, label)
	}

	fmt.Printf("\nVerdict: %s\n", result.Verdict)
	return nil
}
