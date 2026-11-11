// Attack 4: Dictionary Attack with the HMAC as Password Oracle
//
// For each candidate password the tool runs the library's own decoder,
// steg.Decode, on a fresh copy of the carrier. A decode that returns without
// error passed HMAC-SHA256 verification, so the candidate is the password.
// Calling the real decode path (instead of re-implementing it) keeps this tool
// in step with the on-image format: it cannot report "not found" for an image
// the current build can decode.
//
// Cost per candidate, as steg.Decode does it:
//
//  1. Read the 16-byte salt from the public salt pixels. Their positions depend
//     only on the image size and encoding settings, so anyone can read the salt
//     without the password; an attacker gains nothing by hoisting this step out
//     of the loop, since it is negligible next to step 2.
//  2. One Argon2id(candidate, salt, t=2, m=64 MiB, p=4). This dominates.
//  3. Build the secret part of the pixel order (ChaCha8 Fisher-Yates over all
//     pixels after the salt pixels), keyed by Argon2id output.
//  4. Decrypt the 4-byte container length. For a wrong candidate it is an
//     essentially random uint32, and anything above the carrier's capacity is
//     rejected before the payload is read or the HMAC computed; that rejects
//     almost every wrong candidate. The rest fail HMAC verification.
//
// Usage:
//
//	go run ./cmd/attack4 [-c channels] [-b bits] <image.png> <wordlist.txt>
//
// Flags:
//
//	-c  number of channels used during encoding (default 3, as steg encode)
//	-b  bits per channel used during encoding (default 1, as steg encode)
package main

import (
	"bufio"
	"flag"
	"fmt"
	"image"
	"image/draw"
	_ "image/png"
	"os"
	"time"

	"github.com/pableeee/steg/steg"
)

func main() {
	channels := flag.Int("c", 3, "channels used during encoding (1–3)")
	bitsPerCh := flag.Int("b", 1, "bits per channel used during encoding (1–8)")
	flag.Parse()

	args := flag.Args()
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: attack4 [-c channels] [-b bits] <image.png> <wordlist.txt>\n")
		os.Exit(1)
	}

	carrier, err := loadRGBA(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "load image: %v\n", err)
		os.Exit(1)
	}

	wl, err := os.Open(args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "open wordlist: %v\n", err)
		os.Exit(1)
	}
	defer wl.Close()

	fmt.Printf("=== Attack 4: Dictionary Attack ===\n\n")
	fmt.Printf("Image:    %s  (%dx%d)\n", args[0], carrier.Bounds().Dx(), carrier.Bounds().Dy())
	fmt.Printf("Wordlist: %s\n", args[1])
	fmt.Printf("Encoding: %d channel(s), %d bit(s)/channel\n\n", *channels, *bitsPerCh)

	var tried int
	start := time.Now()
	scanner := bufio.NewScanner(wl)

	for scanner.Scan() {
		candidate := scanner.Text()
		if candidate == "" {
			continue
		}
		tried++

		payload, err := steg.Decode(clone(carrier), []byte(candidate), *bitsPerCh, *channels)
		elapsed := time.Since(start)
		fmt.Printf("\r  tried %d  (%.2f/s, %s/candidate)   ",
			tried, float64(tried)/elapsed.Seconds(), perCandidate(elapsed, tried))

		if err == nil {
			fmt.Printf("\n\nPASSWORD FOUND: %q\n\n", candidate)
			printStats(tried, elapsed)
			fmt.Printf("\n  Payload (%d bytes):\n%s\n", len(payload), string(payload))
			return
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "\nwordlist read error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\n\nPassword not found in wordlist.\n\n")
	printStats(tried, time.Since(start))
}

func printStats(tried int, elapsed time.Duration) {
	fmt.Printf("  Tried:          %d candidates\n", tried)
	fmt.Printf("  Total:          %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("  Per candidate:  %s\n", perCandidate(elapsed, tried))
	fmt.Printf("  Rate:           %.2f candidates/s\n", float64(tried)/elapsed.Seconds())
}

func perCandidate(elapsed time.Duration, tried int) time.Duration {
	if tried == 0 {
		return 0
	}
	return (elapsed / time.Duration(tried)).Round(time.Millisecond)
}

// clone returns an independent copy of m, so that no candidate can observe
// state left in the carrier by a previous one.
func clone(m *image.RGBA) *image.RGBA {
	c := *m
	c.Pix = append([]uint8(nil), m.Pix...)
	return &c
}

// loadRGBA decodes path into an *image.RGBA at origin (0,0), the same
// conversion steg decode applies before decoding.
func loadRGBA(path string) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst, nil
}
