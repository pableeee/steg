package analysis

import "image"

// extractChannel returns the 8-bit channel values for all pixels in img.
// ch: 0=R, 1=G, 2=B. Pixels are returned in row-major order.
func extractChannel(img image.Image, ch int) []uint8 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	vals := make([]uint8, w*h)
	i := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			switch ch {
			case 0:
				vals[i] = uint8(r >> 8)
			case 1:
				vals[i] = uint8(g >> 8)
			case 2:
				vals[i] = uint8(bl >> 8)
			}
			i++
		}
	}
	return vals
}
