// Attack 2: Bootstrap Cipher Nonce Reuse — Two-Time Pad Demo
//
// The bootstrap cipher (AES-128-CTR) uses a key and nonce derived entirely from
// the password via a fixed-salt Argon2id. For the same password, that keystream
// KS_bs is identical across every encode.
//
// Each image stores enc_salt_i = randomSalt_i ⊕ KS_bs at LSB bits 0–127.
// Given two images A and B encoded with the same password:
//
//	enc_salt_A ⊕ enc_salt_B  =  randomSalt_A ⊕ randomSalt_B
//
// This is the classic two-time pad. Here we:
//  1. Recover both salts by decoding with the known password.
//  2. Extract the raw (encrypted) salt bytes from both images.
//  3. Verify the XOR identity holds.
//
// Usage:
//
//	go run ./cmd/attack2 <imageA.png> <imageB.png> <password> [-c channels] [-b bits]
package main

import (
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

// Fixed application salt — hardcoded in steg/steg.go.
var appSalt = []byte("github.com/pableeee/steg/v1")

func main() {
	channels := flag.Int("c", 3, "channels used during encoding (1–3)")
	bitsPerCh := flag.Int("b", 1, "bits per channel used during encoding")
	flag.Parse()
	args := flag.Args()

	if len(args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: attack2 [flags] <imageA.png> <imageB.png> <password>\n")
		os.Exit(1)
	}

	imgA, err := loadDrawImage(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "load A: %v\n", err)
		os.Exit(1)
	}
	imgB, err := loadDrawImage(args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "load B: %v\n", err)
		os.Exit(1)
	}
	pass := []byte(args[2])

	fmt.Print("=== Attack 2: Bootstrap Nonce Reuse (Two-Time Pad) ===\n\n")

	// Step 1: derive bootstrap keys — these are IDENTICAL for both images.
	bsSeed, bsEncKey, bsNonce := deriveBootstrapKeys(pass)
	fmt.Printf("Bootstrap key (hex): %s\n", hex.EncodeToString(bsEncKey))
	fmt.Printf("Bootstrap nonce:     %08x  (fixed for password \"%s\")\n\n", bsNonce, pass)

	// Step 2: extract raw (still-encrypted) salt bytes from each image.
	encSaltA := extractRawBootstrapBytes(imgA, bsSeed, *bitsPerCh, *channels)
	encSaltB := extractRawBootstrapBytes(imgB, bsSeed, *bitsPerCh, *channels)
	fmt.Printf("enc_salt_A (hex): %s\n", hex.EncodeToString(encSaltA[:]))
	fmt.Printf("enc_salt_B (hex): %s\n\n", hex.EncodeToString(encSaltB[:]))

	// Step 3: decrypt each salt using the bootstrap cipher.
	saltA := decryptBootstrapSalt(encSaltA, bsEncKey, bsNonce)
	saltB := decryptBootstrapSalt(encSaltB, bsEncKey, bsNonce)
	fmt.Printf("randomSalt_A (hex): %s\n", hex.EncodeToString(saltA[:]))
	fmt.Printf("randomSalt_B (hex): %s\n\n", hex.EncodeToString(saltB[:]))

	// Step 4: verify the XOR identity.
	xorEnc := xor16(encSaltA, encSaltB)
	xorSalt := xor16(saltA, saltB)
	fmt.Printf("enc_salt_A  ⊕ enc_salt_B  = %s\n", hex.EncodeToString(xorEnc[:]))
	fmt.Printf("randomSalt_A ⊕ randomSalt_B = %s\n\n", hex.EncodeToString(xorSalt[:]))

	if xorEnc == xorSalt {
		fmt.Println("✓ CONFIRMED: enc_salt_A ⊕ enc_salt_B  =  randomSalt_A ⊕ randomSalt_B")
		fmt.Println("  The bootstrap keystream KS_bs cancels out — two-time pad identity holds.")
	} else {
		fmt.Println("✗ MISMATCH — check that both images were encoded with the same password.")
	}

	// Step 5: recover KS_bs from image A and use it to re-derive salt B without
	//         the second Argon2id call (demonstrates the CPA shortcut).
	ksBs := xor16(encSaltA, saltA)
	recoveredSaltB := xor16(encSaltB, ksBs)
	fmt.Printf("\nRecovered KS_bs (hex):       %s\n", hex.EncodeToString(ksBs[:]))
	fmt.Printf("Re-derived randomSalt_B:     %s\n", hex.EncodeToString(recoveredSaltB[:]))
	if recoveredSaltB == saltB {
		fmt.Println("✓ randomSalt_B recovered correctly via KS_bs — bootstrap CPA works.")
	}
}

// ─── bootstrap key derivation (mirrors steg/steg.go:deriveBootstrapKeys) ─────

func deriveBootstrapKeys(pass []byte) (bsSeed int64, bsEncKey []byte, bsNonce uint32) {
	derived := argon2.IDKey(pass, appSalt, 1, 64*1024, 4, 28)
	bsSeed = int64(binary.BigEndian.Uint64(derived[0:8]))
	bsEncKey = derived[8:24]
	bsNonce = binary.BigEndian.Uint32(derived[24:28])
	return
}

// extractRawBootstrapBytes reads the first 16 bytes from the image LSBs in
// Fisher-Yates pixel order WITHOUT applying any cipher — the raw ciphertext.
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

// decryptBootstrapSalt decrypts a 16-byte encrypted salt using the bootstrap cipher.
func decryptBootstrapSalt(enc [16]byte, bsEncKey []byte, bsNonce uint32) [16]byte {
	c, err := cipher.NewCipher(bsNonce, bsEncKey)
	if err != nil {
		panic(err)
	}
	var plain [16]byte
	for i, b := range enc {
		plain[i], _ = c.DecryptByte(b)
	}
	return plain
}

func xor16(a, b [16]byte) [16]byte {
	var out [16]byte
	for i := range a {
		out[i] = a[i] ^ b[i]
	}
	return out
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
