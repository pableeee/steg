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
	Long: fmt.Sprintf(`Runs two complementary statistical tests on the image:

  Chi-square  Detects global LSB histogram uniformity. Natural images have
              unequal (2k)/(2k+1) pixel-value pair frequencies; LSB embedding
              equalises them. A high p-value (> %g) is suspicious. Degrees of
              freedom (df) are the number of value pairs present minus one; a
              channel with df < 1 has too few distinct values to test.

  RS analysis Detects local pixel smoothness asymmetry. In a clean image the
              regular-group fractions under the positive (Rm) and negative
              (R-m) flipping masks are about equal; LSB embedding lowers Rm
              and raises R-m. A negative asymmetry (Rm − R-m < %g) is
              suspicious. Small images (below roughly 512×512) are noisier and
              may be flagged by chance.`, analysis.ChiSquareSuspiciousP, analysis.RSSuspiciousAsymmetry),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true // flags parsed: errors from here on are not usage errors
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

	fmt.Printf("Chi-square analysis (p-value > %g = suspicious):\n", analysis.ChiSquareSuspiciousP)
	for _, r := range result.ChiSquare {
		if r.DF < 1 {
			fmt.Printf("  %s: too few distinct values to test  [CLEAN]\n", r.Channel)
			continue
		}
		label := "CLEAN"
		if r.Suspicious {
			label = "SUSPICIOUS"
		}
		fmt.Printf("  %s: χ²=%-10.2f  df=%-3d  p=%.4f  [%s]\n", r.Channel, r.ChiSq, r.DF, r.PValue, label)
	}

	fmt.Printf("\nRS analysis (asymmetry < %g = suspicious):\n", analysis.RSSuspiciousAsymmetry)
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
