// Attack 3: Bootstrap CPA — Full Payload Recovery via Salt Recovery
//
// Extends Attack 2 to actually decrypt the target image's payload.
//
// Scenario: attacker knows the password, has encode access, and wants to
// recover a target image's payload without running the standard decode path.
// By recovering KS_bs from a self-made image, they can derive any target
// image's randomSalt and thus its main keys.
//
// Steps:
//  1. Encode a known file into imageA with password P → gives enc_salt_A.
//  2. Decode imageA to recover randomSalt_A (the bootstrap step of decode).
//  3. KS_bs = enc_salt_A ⊕ randomSalt_A.
//  4. Extract enc_salt_T from target imageT.
//  5. randomSalt_T = enc_salt_T ⊕ KS_bs.
//  6. Derive main keys: Argon2id(P, randomSalt_T).
//  7. Decrypt imageT payload and verify HMAC — without calling steg.Decode.
//
// Usage:
//
//	go run ./cmd/attack3 <imageA.png> <imageTarget.png> <password> [-c channels] [-b bits]
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"image"
	"image/draw"
	_ "image/png"
	"io"
	"os"

	"github.com/pableeee/steg/cipher"
	"github.com/pableeee/steg/cursors"
	"golang.org/x/crypto/argon2"
)

var appSalt = []byte("github.com/pableeee/steg/v1")

func main() {
	channels := flag.Int("c", 3, "channels used during encoding (1–3)")
	bitsPerCh := flag.Int("b", 1, "bits per channel used during encoding")
	flag.Parse()
	args := flag.Args()

	if len(args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: attack3 [flags] <imageA.png> <imageTarget.png> <password>\n")
		os.Exit(1)
	}

	imgA, err := loadDrawImage(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "load A: %v\n", err)
		os.Exit(1)
	}
	imgT, err := loadDrawImage(args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "load target: %v\n", err)
		os.Exit(1)
	}
	pass := []byte(args[2])

	fmt.Print("=== Attack 3: Bootstrap CPA — Salt Recovery ===\n\n")

	bsSeed, bsEncKey, bsNonce := deriveBootstrapKeys(pass)
	fmt.Printf("Bootstrap key:   %s\n", hex.EncodeToString(bsEncKey))
	fmt.Printf("Bootstrap nonce: %08x\n\n", bsNonce)

	// Step 1: extract raw encrypted salts from both images.
	encSaltA := extractRawBootstrapBytes(imgA, bsSeed, *bitsPerCh, *channels)
	encSaltT := extractRawBootstrapBytes(imgT, bsSeed, *bitsPerCh, *channels)

	// Step 2: decrypt imageA's salt (simulating "we created imageA ourselves").
	saltA := decryptSalt(encSaltA, bsEncKey, bsNonce)
	fmt.Printf("randomSalt_A (hex): %s  (recovered from our own image)\n", hex.EncodeToString(saltA[:]))

	// Step 3: recover bootstrap keystream from imageA.
	ksBs := xor16(encSaltA, saltA)
	fmt.Printf("KS_bs (hex):        %s\n\n", hex.EncodeToString(ksBs[:]))

	// Step 4: recover target's randomSalt without Argon2id.
	saltT := xor16(encSaltT, ksBs)
	fmt.Printf("enc_salt_T (hex):   %s\n", hex.EncodeToString(encSaltT[:]))
	fmt.Printf("randomSalt_T (hex): %s  (recovered via KS_bs)\n\n", hex.EncodeToString(saltT[:]))

	// Verify against directly decrypted salt (ground truth).
	saltTDirect := decryptSalt(encSaltT, bsEncKey, bsNonce)
	if saltT == saltTDirect {
		fmt.Print("✓ randomSalt_T matches direct decryption — CPA recovery confirmed.\n\n")
	} else {
		fmt.Print("✗ salt mismatch — something went wrong.\n\n")
		return
	}

	// Step 5: derive main keys for the target using the recovered salt.
	encKeyT, macKeyT, payloadNonceT := deriveMainKeys(pass, saltT[:])
	fmt.Printf("Main enc key (hex): %s\n", hex.EncodeToString(encKeyT))
	fmt.Printf("Main nonce:         %08x\n\n", payloadNonceT)

	// Step 6: decrypt the target image's payload and verify HMAC.
	payload, err := decryptPayload(imgT, bsSeed, encKeyT, macKeyT, payloadNonceT, *bitsPerCh, *channels)
	if err != nil {
		fmt.Printf("✗ Payload decryption failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Payload decrypted successfully — %d bytes\n", len(payload))
	fmt.Printf("  First 64 bytes (hex): %s\n", hex.EncodeToString(payload[:min(64, len(payload))]))
}

// ─── key derivation ───────────────────────────────────────────────────────────

func deriveBootstrapKeys(pass []byte) (bsSeed int64, bsEncKey []byte, bsNonce uint32) {
	derived := argon2.IDKey(pass, appSalt, 1, 64*1024, 4, 28)
	bsSeed = int64(binary.BigEndian.Uint64(derived[0:8]))
	bsEncKey = derived[8:24]
	bsNonce = binary.BigEndian.Uint32(derived[24:28])
	return
}

func deriveMainKeys(pass []byte, salt []byte) (encKey, macKey []byte, payloadNonce uint32) {
	derived := argon2.IDKey(pass, salt, 1, 64*1024, 4, 52)
	encKey = derived[0:16]
	macKey = derived[16:48]
	payloadNonce = binary.BigEndian.Uint32(derived[48:52])
	return
}

// ─── image helpers ────────────────────────────────────────────────────────────

func extractRawBootstrapBytes(img draw.Image, bsSeed int64, bitsPerChannel, channels int) [16]byte {
	cur := cursors.NewRNGCursor(img, cursorOpts(bsSeed, bitsPerChannel, channels)...)
	var enc [16]byte
	io.ReadFull(cursors.CursorAdapter(cur), enc[:])
	return enc
}

func cursorOpts(seed int64, bitsPerChannel, channels int) []cursors.Option {
	opts := []cursors.Option{
		cursors.WithSeed(seed),
		cursors.WithBitsPerChannel(bitsPerChannel),
	}
	if channels >= 2 {
		opts = append(opts, cursors.UseGreenBit())
	}
	if channels >= 3 {
		opts = append(opts, cursors.UseBlueBit())
	}
	return opts
}

func decryptSalt(enc [16]byte, bsEncKey []byte, bsNonce uint32) [16]byte {
	c, _ := cipher.NewCipher(bsNonce, bsEncKey)
	var plain [16]byte
	for i, b := range enc {
		plain[i], _ = c.DecryptByte(b)
	}
	return plain
}

// decryptPayload decrypts the full payload from the image and verifies HMAC.
// Mirrors the logic in steg/decode.go and steg/container/container.go.
func decryptPayload(img draw.Image, bsSeed int64, encKey, macKey []byte, payloadNonce uint32, bitsPerChannel, channels int) ([]byte, error) {
	cur := cursors.NewRNGCursor(img, cursorOpts(bsSeed, bitsPerChannel, channels)...)
	c, _ := cipher.NewCipher(payloadNonce, encKey)
	payloadCM := cursors.CipherMiddleware(cur, c)

	// Seek to bit 128 (past the 16-byte encrypted salt).
	if _, err := payloadCM.Seek(128, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek: %w", err)
	}
	adapter := cursors.CursorAdapter(payloadCM)

	// Read container length (4 bytes LE).
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(adapter, lenBuf); err != nil {
		return nil, fmt.Errorf("read length: %w", err)
	}
	containerLen := binary.LittleEndian.Uint32(lenBuf)

	// Read payload bytes.
	payloadBuf := make([]byte, containerLen)
	if _, err := io.ReadFull(adapter, payloadBuf); err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}

	// Read HMAC tag (32 bytes).
	tag := make([]byte, 32)
	if _, err := io.ReadFull(adapter, tag); err != nil {
		return nil, fmt.Errorf("read hmac: %w", err)
	}

	// Verify HMAC.
	mac := hmac.New(sha256.New, macKey)
	mac.Write(payloadBuf)
	if !hmac.Equal(tag, mac.Sum(nil)) {
		return nil, fmt.Errorf("HMAC verification failed — wrong password or corrupt image")
	}

	// Extract real payload from padded buffer: first 4 bytes = LE real length.
	if len(payloadBuf) < 4 {
		return nil, fmt.Errorf("payload too short")
	}
	realLen := binary.LittleEndian.Uint32(payloadBuf[:4])
	if int(realLen) > len(payloadBuf)-4 {
		return nil, fmt.Errorf("corrupt real length field")
	}
	return bytes.Clone(payloadBuf[4 : 4+realLen]), nil
}

func xor16(a, b [16]byte) [16]byte {
	var out [16]byte
	for i := range a {
		out[i] = a[i] ^ b[i]
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
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
