package main

import (
	"image"
	"image/draw"

	"github.com/spf13/cobra"
)

var (
	rootCmd = &cobra.Command{
		Use:   "steg",
		Short: "Service command-line interface",
	}
)

func init() {
	rootCmd.AddCommand(encodeCmd)
	rootCmd.AddCommand(decodeCmd)
	rootCmd.AddCommand(testCmd)
}

func toDrawImage(src image.Image) draw.Image {
	bounds := src.Bounds()
	cimg := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(cimg, cimg.Bounds(), src, bounds.Min, draw.Src)

	return cimg
}
