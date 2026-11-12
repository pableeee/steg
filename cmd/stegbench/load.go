package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// plane is one 8-bit image plane, row-major, width × height.
type plane struct {
	width, height int
	pix           []uint8
}

// listCovers returns the .pgm and .png files directly in dir (not
// recursively), sorted by name, then shuffled with a generator seeded from
// seed, truncated to the first n (n <= 0 means all). The result depends only
// on the set of file names, n and seed.
func listCovers(dir string, n int, seed int64) (selected []string, found int, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	var names []string
	for _, e := range entries {
		if !e.Type().IsRegular() && e.Type()&os.ModeSymlink == 0 {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".pgm", ".png":
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	rng := rand.New(rand.NewChaCha8(deriveSeed("select", seed)))
	rng.Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })
	found = len(names)
	if n > 0 && n < len(names) {
		names = names[:n]
	}
	paths := make([]string, len(names))
	for i, name := range names {
		paths[i] = filepath.Join(dir, name)
	}
	return paths, found, nil
}

// maxFileSize bounds how much of a cover file is read into memory.
const maxFileSize = maxPixels*8 + maxHeader

// loadPlane reads a cover file and returns the plane to embed in: the whole
// image for PGM; for PNG, the gray plane of a grayscale image or the given
// channel (0 = R, 1 = G, 2 = B) of a colour image. With crop > 0 the central
// crop × crop region is returned; images smaller than that are an error.
func loadPlane(path string, channel, crop int) (*plane, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	if st.Size() > maxFileSize {
		return nil, fmt.Errorf("file is %d bytes, more than the %d-byte limit", st.Size(), int64(maxFileSize))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p *plane
	switch strings.ToLower(filepath.Ext(path)) {
	case ".pgm":
		g, err := parsePGM(data)
		if err != nil {
			return nil, err
		}
		p = &plane{width: g.width, height: g.height, pix: g.pix}
	case ".png":
		p, err = decodePNGPlane(data, channel)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported extension %q", filepath.Ext(path))
	}
	if crop > 0 {
		return cropCenter(p, crop)
	}
	return p, nil
}

// decodePNGPlane decodes a PNG, checking its dimensions before the pixel
// buffer is allocated.
func decodePNGPlane(data []byte, channel int) (*plane, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("png: %w", err)
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > maxDim || cfg.Height > maxDim ||
		int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, fmt.Errorf("png: dimensions %dx%d out of range (each 1..%d, at most %d pixels)",
			cfg.Width, cfg.Height, maxDim, maxPixels)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("png: %w", err)
	}
	return imagePlane(img, channel), nil
}

// imagePlane extracts the plane of img described at loadPlane. Gray and
// Gray16 images give their gray level (the high byte for 16-bit). Any other
// image gives the selected channel of its non-premultiplied 8-bit colour,
// except *image.RGBA, whose stored (premultiplied) bytes are used as they are.
func imagePlane(img image.Image, channel int) *plane {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	p := &plane{width: w, height: h, pix: make([]uint8, w*h)}
	switch m := img.(type) {
	case *image.Gray:
		for y := 0; y < h; y++ {
			copy(p.pix[y*w:(y+1)*w], m.Pix[y*m.Stride:])
		}
		return p
	case *image.RGBA:
		for y := 0; y < h; y++ {
			row := m.Pix[y*m.Stride:]
			for x := 0; x < w; x++ {
				p.pix[y*w+x] = row[x*4+channel]
			}
		}
		return p
	case *image.NRGBA:
		for y := 0; y < h; y++ {
			row := m.Pix[y*m.Stride:]
			for x := 0; x < w; x++ {
				p.pix[y*w+x] = row[x*4+channel]
			}
		}
		return p
	}
	gray := img.ColorModel() == color.GrayModel || img.ColorModel() == color.Gray16Model
	i := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.At(x, y)
			if gray {
				p.pix[i] = uint8(color.Gray16Model.Convert(c).(color.Gray16).Y >> 8)
			} else {
				n := color.NRGBAModel.Convert(c).(color.NRGBA)
				p.pix[i] = [3]uint8{n.R, n.G, n.B}[channel]
			}
			i++
		}
	}
	return p
}

// cropCenter returns the central size × size region of p as a new plane.
func cropCenter(p *plane, size int) (*plane, error) {
	if p.width < size || p.height < size {
		return nil, fmt.Errorf("image %dx%d is smaller than the %dx%d crop", p.width, p.height, size, size)
	}
	x0, y0 := (p.width-size)/2, (p.height-size)/2
	out := &plane{width: size, height: size, pix: make([]uint8, size*size)}
	for y := 0; y < size; y++ {
		copy(out.pix[y*size:(y+1)*size], p.pix[(y0+y)*p.width+x0:])
	}
	return out, nil
}
