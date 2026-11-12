package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestParsePGMValid(t *testing.T) {
	raster := []byte{0, 1, 2, 3, 4, 5, 254, 255, 128, 7, 8, 9}
	cases := map[string]string{
		"plain":                   "P5\n4 3\n255\n",
		"comments":                "P5\n# created by test\n4 # width\n# a comment line\n3\n255\n",
		"odd whitespace":          "P5 \t\r\n\f\v4\t\t3\r\n  255\t",
		"comment right after P5":  "P5# comment\n4 3 255\r",
		"leading zeros":           "P5\n0004 003\n0255\n",
		"comment with CR ending":  "P5\n#c\r4 3\n255\n",
		"single space separators": "P5 4 3 255 ",
	}
	for name, hdr := range cases {
		t.Run(name, func(t *testing.T) {
			data := append([]byte(hdr), raster...)
			g, err := parsePGM(data)
			if err != nil {
				t.Fatalf("parsePGM: %v", err)
			}
			if g.width != 4 || g.height != 3 {
				t.Fatalf("dims %dx%d, want 4x3", g.width, g.height)
			}
			if !bytes.Equal(g.pix, raster) {
				t.Fatalf("pix %v, want %v", g.pix, raster)
			}
		})
	}
}

// A raster whose first byte is whitespace or '#' must not be eaten by the
// header parser: exactly one whitespace byte follows maxval.
func TestParsePGMRasterStartsWithWhitespace(t *testing.T) {
	raster := []byte{'\n', '#', ' ', 9}
	g, err := parsePGM(append([]byte("P5\n2 2\n255\n"), raster...))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(g.pix, raster) {
		t.Fatalf("pix %v, want %v", g.pix, raster)
	}
}

func TestParsePGMRejects(t *testing.T) {
	ok := func(hdr string, n int) []byte { return append([]byte(hdr), make([]byte, n)...) }
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"empty", nil, "not a PGM"},
		{"garbage", []byte("hello world"), "not a PGM"},
		{"binary garbage", []byte{0x89, 'P', 'N', 'G', 0, 0}, "not a PGM"},
		{"P2 plain", []byte("P2\n2 2\n255\n0 1 2 3\n"), "P2"},
		{"P6 ppm", ok("P6\n2 2\n255\n", 12), "P6"},
		{"P4", ok("P4\n8 1\n", 1), "P4"},
		{"no separator after magic", ok("P54 3\n255\n", 12), "whitespace"},
		{"maxval 65535", ok("P5\n2 2\n65535\n", 8), "maxval"},
		{"maxval 1", ok("P5\n2 2\n1\n", 4), "maxval"},
		{"maxval 254", ok("P5\n2 2\n254\n", 4), "maxval"},
		{"zero width", ok("P5\n0 2\n255\n", 0), "dimensions"},
		{"zero height", ok("P5\n2 0\n255\n", 0), "dimensions"},
		{"absurd dims", ok("P5\n999999999 999999999\n255\n", 4), "dimensions"},
		{"too many pixels", ok("P5\n32768 32768\n255\n", 4), "dimensions"},
		{"too wide", ok("P5\n40000 1\n255\n", 4), "dimensions"},
		{"overlong number", ok("P5\n0000000002 2\n255\n", 4), "digits"},
		{"negative", ok("P5\n-2 2\n255\n", 4), "decimal number"},
		{"letter in number", ok("P5\n2x 2\n255\n", 4), "whitespace"},
		{"truncated raster", ok("P5\n4 3\n255\n", 11), "truncated"},
		{"trailing bytes", ok("P5\n4 3\n255\n", 13), "trailing"},
		{"no byte after maxval", []byte("P5\n1 1\n255"), "single whitespace"},
		{"header ends early", []byte("P5\n4 "), "end of header"},
		{"comment to EOF", []byte("P5\n4 3 # no end"), "end of header"},
		{"maxval followed by comment", ok("P5\n1 1\n255#c\n", 1), "single whitespace"},
		{"huge comment", append([]byte("P5\n#"+strings.Repeat("x", maxHeader+10)+"\n1 1\n255\n"), 0), "header longer"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, err := parsePGM(c.data)
			if err == nil {
				t.Fatalf("parsePGM accepted %q as %dx%d", c.data[:min(len(c.data), 40)], g.width, g.height)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

func FuzzParsePGM(f *testing.F) {
	f.Add([]byte("P5\n2 2\n255\n\x00\x01\x02\x03"))
	f.Add([]byte("P5 # c\n1 1 255 x"))
	f.Fuzz(func(t *testing.T, data []byte) {
		g, err := parsePGM(data)
		if err != nil {
			return
		}
		if g.width < 1 || g.height < 1 || len(g.pix) != g.width*g.height {
			t.Fatalf("inconsistent result %dx%d with %d bytes", g.width, g.height, len(g.pix))
		}
	})
}
