package steg

import (
	"encoding/binary"
	"fmt"

	"github.com/pableeee/steg/cursors"
	"golang.org/x/crypto/argon2"
)

// appSalt is a fixed domain separator. Argon2id's memory-hardness
// provides key-stretching even with a fixed salt.
var appSalt = []byte("github.com/pableeee/steg/v1")

// cursorOptions builds the RNGCursor option slice for the given configuration.
// channels: 1 = R only, 2 = R+G, 3 = R+G+B.
func cursorOptions(seed int64, bitsPerChannel, channels int) []cursors.Option {
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

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func lcm(a, b int) int         { return a / gcd(a, b) * b }
func lcmBytes(bpb, bc int) int { return lcm(bpb, bc) / bpb }

// deriveKeys stretches pass using Argon2id and returns:
//   - seed: int64 to initialize the RNG cursor
//   - encKey: 16-byte AES-128 encryption key
//   - macKey: 32-byte HMAC-SHA256 authentication key
func deriveKeys(pass []byte) (seed int64, encKey []byte, macKey []byte, err error) {
	if len(pass) == 0 {
		return 0, nil, nil, fmt.Errorf("password must not be empty")
	}
	// OWASP 2023 interactive parameters: time=1, memory=64 MiB, threads=4
	// 56 bytes: 8 (RNG seed) + 16 (AES-128 enc key) + 32 (HMAC-SHA256 mac key)
	derived := argon2.IDKey(pass, appSalt, 1, 64*1024, 4, 56)
	seed = int64(binary.BigEndian.Uint64(derived[0:8]))
	encKey = derived[8:24]
	macKey = derived[24:56]
	return seed, encKey, macKey, nil
}
