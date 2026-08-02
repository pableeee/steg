// Attack 4: Dictionary Attack with HMAC Oracle
//
// Uses HMAC-SHA256 verification as a password oracle. For each candidate:
//
//  1. Argon2id(candidate, fixedSalt)  → bootstrap keys
//  2. Decrypt 16-byte salt from image LSBs
//  3. Argon2id(candidate, recoveredSalt) → main keys
//  4. Decrypt container + verify HMAC
//
// HMAC pass = correct password. Two Argon2id calls per candidate (~100–200 ms
// each on CPU) make this expensive; Argon2id's 64 MiB memory requirement
// limits GPU parallelism.
//
// Usage:
//
//	go run ./cmd/attack4 <image.png> <wordlist.txt> [-c channels] [-b bits]
//
// Flags:
//
//	-c  number of channels used during encoding (default 1)
//	-b  bits per channel used during encoding (default 1)
package main

import (
	"bufio"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/draw"
	_ "image/png"
	"io"
	"os"
	"time"

	"crypto/hmac"
	"crypto/sha256"

	"github.com/pableeee/steg/cipher"
	"github.com/pableeee/steg/cursors"
	"golang.org/x/crypto/argon2"
)

var appSalt = []byte("github.com/pableeee/steg/v1")

func main() {
	channels := flag.Int("c", 3, "channels used during encoding (1–3)")
	bitsPerCh := flag.Int("b", 1, "bits per channel used during encoding (1,2,4,8)")
	flag.Parse()

	args := flag.Args()
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: attack4 [flags] <image.png> <wordlist.txt>\n")
		os.Exit(1)
	}

	img, err := loadDrawImage(args[0])
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
	fmt.Printf("Image:    %s  (%dx%d)\n", args[0], img.Bounds().Dx(), img.Bounds().Dy())
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

		payload, err := tryPassword(img, []byte(candidate), *bitsPerCh, *channels)
		if tried%100 == 0 {
			elapsed := time.Since(start)
			fmt.Printf("\r  tried %d  (%.1f/s)   ", tried, float64(tried)/elapsed.Seconds())
		}

		if err == nil {
			elapsed := time.Since(start)
			fmt.Printf("\r\n✓ PASSWORD FOUND: \"%s\"\n\n", candidate)
			fmt.Printf("  Tried:    %d candidates\n", tried)
			fmt.Printf("  Elapsed:  %s\n", elapsed.Round(time.Millisecond))
			fmt.Printf("  Rate:     %.2f candidates/s\n\n", float64(tried)/elapsed.Seconds())
			fmt.Printf("  Payload (%d bytes):\n%s\n", len(payload), string(payload))
			return
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "\nwordlist read error: %v\n", err)
		os.Exit(1)
	}

	elapsed := time.Since(start)
	fmt.Printf("\r\n✗ Password not found in wordlist.\n")
	fmt.Printf("  Tried: %d candidates in %s\n", tried, elapsed.Round(time.Millisecond))
}

// tryPassword attempts to decode the image with the given password.
// Returns the plaintext payload on success, or an error if the HMAC fails.
func tryPassword(img draw.Image, pass []byte, bitsPerChannel, channels int) ([]byte, error) {
	// Step 1: bootstrap keys.
	bsSeed, bsEncKey, bsNonce := deriveBootstrapKeys(pass)

	// Step 2: decrypt random salt from image LSB bits 0–127.
	cur := newCursor(img, bsSeed, bitsPerChannel, channels)
	bsCipher, err := cipher.NewCipher(bsNonce, bsEncKey)
	if err != nil {
		return nil, err
	}
	bsAdapter := cursors.CursorAdapter(cursors.CipherMiddleware(cur, bsCipher))
	var randomSalt [16]byte
	if _, err = io.ReadFull(bsAdapter, randomSalt[:]); err != nil {
		return nil, err
	}

	// Step 3: derive main keys from the recovered salt.
	encKey, macKey, payloadNonce := deriveMainKeys(pass, randomSalt[:])

	// Step 4: decrypt payload and verify HMAC (the oracle).
	cur2 := newCursor(img, bsSeed, bitsPerChannel, channels)
	pCipher, err := cipher.NewCipher(payloadNonce, encKey)
	if err != nil {
		return nil, err
	}
	payloadCM := cursors.CipherMiddleware(cur2, pCipher)
	if _, err = payloadCM.Seek(128, io.SeekStart); err != nil {
		return nil, err
	}
	adapter := cursors.CursorAdapter(payloadCM)

	// Read container length.
	lenBuf := make([]byte, 4)
	if _, err = io.ReadFull(adapter, lenBuf); err != nil {
		return nil, err
	}
	containerLen := binary.LittleEndian.Uint32(lenBuf)

	// Read padded payload.
	paddedBuf := make([]byte, containerLen)
	if _, err = io.ReadFull(adapter, paddedBuf); err != nil {
		return nil, err
	}

	// Read HMAC tag.
	tag := make([]byte, 32)
	if _, err = io.ReadFull(adapter, tag); err != nil {
		return nil, err
	}

	// Verify HMAC — this is the oracle.
	mac := hmac.New(sha256.New, macKey)
	mac.Write(paddedBuf)
	if !hmac.Equal(tag, mac.Sum(nil)) {
		return nil, fmt.Errorf("hmac mismatch")
	}

	// Extract real payload (first 4 bytes = LE real length).
	if len(paddedBuf) < 4 {
		return nil, fmt.Errorf("payload too short")
	}
	realLen := binary.LittleEndian.Uint32(paddedBuf[:4])
	if int(realLen) > len(paddedBuf)-4 {
		return nil, fmt.Errorf("corrupt length")
	}
	return paddedBuf[4 : 4+realLen], nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func newCursor(img draw.Image, bsSeed int64, bitsPerChannel, channels int) *cursors.RNGCursor {
	opts := []cursors.Option{
		cursors.WithSeed(bsSeed),
		cursors.WithBitsPerChannel(bitsPerChannel),
	}
	if channels >= 2 {
		opts = append(opts, cursors.UseGreenBit())
	}
	if channels >= 3 {
		opts = append(opts, cursors.UseBlueBit())
	}
	return cursors.NewRNGCursor(img, opts...)
}

func deriveBootstrapKeys(pass []byte) (bsSeed int64, bsEncKey []byte, bsNonce uint32) {
	derived := argon2.IDKey(pass, appSalt, 1, 64*1024, 4, 28)
	bsSeed = int64(binary.BigEndian.Uint64(derived[0:8]))
	bsEncKey = derived[8:24]
	bsNonce = binary.BigEndian.Uint32(derived[24:28])
	return
}

func deriveMainKeys(pass, salt []byte) (encKey, macKey []byte, payloadNonce uint32) {
	derived := argon2.IDKey(pass, salt, 1, 64*1024, 4, 52)
	encKey = derived[0:16]
	macKey = derived[16:48]
	payloadNonce = binary.BigEndian.Uint32(derived[48:52])
	return
}

func loadDrawImage(path string) (draw.Image, error) {
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
