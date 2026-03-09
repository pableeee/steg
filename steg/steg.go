package steg

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"image/draw"

	"github.com/pableeee/steg/cursors"
	"golang.org/x/crypto/argon2"
)

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

// deriveSeed returns the Fisher-Yates pixel-traversal seed for the given password.
// SHA-256 is sufficient here: the seed only determines which pixels carry data,
// not any cryptographic secret. An attacker who knows the password can already
// decode; one who does not cannot determine the traversal order.
func deriveSeed(pass []byte) (int64, error) {
	if len(pass) == 0 {
		return 0, fmt.Errorf("password must not be empty")
	}
	h := sha256.Sum256(pass)
	return int64(binary.BigEndian.Uint64(h[:8])), nil
}

// deriveMainKeys stretches pass using Argon2id with a per-image random salt.
// Returns encKey (16-byte AES-128 key), macKey (32-byte HMAC-SHA256 key), payloadNonce (4-byte CTR nonce).
// 52 bytes: 16 (AES-128 enc key) + 32 (HMAC-SHA256 mac key) + 4 (cipher nonce).
func deriveMainKeys(pass, salt []byte) (encKey, macKey []byte, payloadNonce uint32, err error) {
	derived := argon2.IDKey(pass, salt, 2, 64*1024, 4, 52)
	encKey = derived[0:16]
	macKey = derived[16:48]
	payloadNonce = binary.BigEndian.Uint32(derived[48:52])
	return encKey, macKey, payloadNonce, nil
}

// imageCapacityBytes returns the maximum real payload size for the given image and
// encoding settings. Overhead is 56 bytes: 16 (plaintext salt) + 4 (container
// length) + 4 (embedded real-length prefix) + 32 (HMAC-SHA256 tag).
func imageCapacityBytes(m draw.Image, bitsPerChannel, channels int) int {
	b := m.Bounds()
	total := b.Dx() * b.Dy() * channels * bitsPerChannel / 8
	const overhead = 56
	if total <= overhead {
		return 0
	}
	return total - overhead
}

// buildPaddedPayload prepends a 4-byte LE real-length prefix and appends random
// padding so the full image capacity is always written. This removes the
// payload-size signal from LSB statistics regardless of actual payload size.
// The returned slice is passed directly to container.WritePayload.
func buildPaddedPayload(m draw.Image, payload []byte, bitsPerChannel, channels int) ([]byte, error) {
	cap := imageCapacityBytes(m, bitsPerChannel, channels)
	if cap <= 0 {
		return nil, fmt.Errorf("steg: image too small to hold any payload")
	}
	if len(payload) > cap {
		return nil, fmt.Errorf("steg: payload too large (%d bytes, capacity %d bytes)", len(payload), cap)
	}
	// Layout: [4B real-length][real-payload][random padding] = 4 + cap bytes total.
	// Container then writes: [4B container-length][data][32B HMAC] = imageTotal bytes.
	out := make([]byte, 4+cap)
	binary.LittleEndian.PutUint32(out[:4], uint32(len(payload)))
	copy(out[4:], payload)
	if _, err := rand.Read(out[4+len(payload):]); err != nil {
		return nil, err
	}
	return out, nil
}

// extractRealPayload recovers the original payload from the padded buffer returned
// by container.ReadPayload. The first 4 bytes are the LE-encoded real length.
func extractRealPayload(padded []byte) ([]byte, error) {
	if len(padded) < 4 {
		return nil, fmt.Errorf("steg: padded payload too short")
	}
	realLen := binary.LittleEndian.Uint32(padded[:4])
	if int(realLen) > len(padded)-4 {
		return nil, fmt.Errorf("steg: corrupt payload: real length %d exceeds data", realLen)
	}
	return padded[4 : 4+realLen], nil
}
