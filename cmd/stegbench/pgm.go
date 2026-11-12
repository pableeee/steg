package main

import (
	"errors"
	"fmt"
)

// Limits on what the loaders accept. They bound every allocation made on the
// strength of a file's header, so a corrupt or hostile file cannot make the
// benchmark allocate more than about maxPixels bytes per plane.
const (
	maxDim    = 1 << 15 // 32768: width and height each at most this
	maxPixels = 1 << 26 // 64 Mi pixels per image
	// maxHeader bounds the PGM header (magic, numbers, comments). Real
	// headers are a few dozen bytes; this allows generous comments.
	maxHeader = 64 << 10
)

// pgm is a decoded 8-bit grayscale image: pix is row-major, width × height.
type pgm struct {
	width, height int
	pix           []uint8
}

// parsePGM decodes a binary PGM ("P5") file held entirely in data.
//
// Accepted:
//   - the magic "P5" followed by whitespace; then width, height and maxval as
//     unsigned decimal integers separated by whitespace (space, \t, \n, \v,
//     \f, \r) and/or comments ('#' to the end of the line);
//   - maxval exactly 255 (8-bit samples);
//   - exactly one whitespace character after maxval, then exactly
//     width × height raster bytes and nothing else.
//
// Rejected: any other magic (P2 plain PGM, P6 PPM, ...), maxval other than
// 255, zero or oversized dimensions (checked before anything is allocated),
// numbers with more than 9 digits or non-digit characters, a raster shorter
// or longer than width × height (so concatenated multi-image files too).
//
// The returned pix aliases data.
func parsePGM(data []byte) (*pgm, error) {
	if len(data) < 2 || data[0] != 'P' {
		return nil, errors.New("pgm: not a PGM file (missing 'P' magic)")
	}
	if data[1] != '5' {
		return nil, fmt.Errorf("pgm: unsupported format P%c: only binary 8-bit PGM (P5) is supported", printable(data[1]))
	}
	p := &pgmParser{data: data, pos: 2}

	var vals [3]int
	names := [3]string{"width", "height", "maxval"}
	for i := range vals {
		if err := p.separator(); err != nil {
			return nil, fmt.Errorf("pgm: before %s: %w", names[i], err)
		}
		v, err := p.number()
		if err != nil {
			return nil, fmt.Errorf("pgm: %s: %w", names[i], err)
		}
		vals[i] = v
	}
	w, h, maxval := vals[0], vals[1], vals[2]

	if maxval != 255 {
		return nil, fmt.Errorf("pgm: maxval %d not supported: only 8-bit images with maxval 255", maxval)
	}
	if w < 1 || h < 1 || w > maxDim || h > maxDim || int64(w)*int64(h) > maxPixels {
		return nil, fmt.Errorf("pgm: dimensions %dx%d out of range (each 1..%d, at most %d pixels)",
			w, h, maxDim, maxPixels)
	}
	if p.pos >= len(data) || !isSpace(data[p.pos]) {
		return nil, errors.New("pgm: maxval must be followed by a single whitespace character")
	}
	p.pos++
	if p.pos > maxHeader {
		return nil, fmt.Errorf("pgm: header longer than %d bytes", maxHeader)
	}

	raster := data[p.pos:]
	need := w * h
	switch {
	case len(raster) < need:
		return nil, fmt.Errorf("pgm: truncated raster: %d bytes, want %d for %dx%d", len(raster), need, w, h)
	case len(raster) > need:
		return nil, fmt.Errorf("pgm: %d trailing bytes after the %dx%d raster", len(raster)-need, w, h)
	}
	return &pgm{width: w, height: h, pix: raster}, nil
}

type pgmParser struct {
	data []byte
	pos  int
}

// separator consumes at least one whitespace character or comment, and any
// mix of them that follows.
func (p *pgmParser) separator() error {
	start := p.pos
	for p.pos < len(p.data) {
		if p.pos > maxHeader {
			return fmt.Errorf("header longer than %d bytes", maxHeader)
		}
		c := p.data[p.pos]
		switch {
		case isSpace(c):
			p.pos++
		case c == '#':
			for p.pos < len(p.data) && p.data[p.pos] != '\n' && p.data[p.pos] != '\r' {
				p.pos++
				if p.pos > maxHeader {
					return fmt.Errorf("header longer than %d bytes", maxHeader)
				}
			}
		default:
			if p.pos == start {
				return fmt.Errorf("expected whitespace, got %q", c)
			}
			return nil
		}
	}
	return errors.New("unexpected end of header")
}

// number reads an unsigned decimal integer of 1 to 9 digits.
func (p *pgmParser) number() (int, error) {
	const maxDigits = 9
	v, n := 0, 0
	for p.pos < len(p.data) && p.data[p.pos] >= '0' && p.data[p.pos] <= '9' {
		if n == maxDigits {
			return 0, fmt.Errorf("number longer than %d digits", maxDigits)
		}
		v = v*10 + int(p.data[p.pos]-'0')
		p.pos++
		n++
	}
	if n == 0 {
		if p.pos >= len(p.data) {
			return 0, errors.New("unexpected end of header")
		}
		return 0, fmt.Errorf("expected a decimal number, got %q", p.data[p.pos])
	}
	return v, nil
}

func isSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	}
	return false
}

func printable(c byte) rune {
	if c >= 0x21 && c < 0x7f {
		return rune(c)
	}
	return '?'
}
