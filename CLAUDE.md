# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Build the CLI binary
make build

# Run all tests
make test

# Run a single test
go test ./steg/ -run TestEncode

# Regenerate mocks (after changing cipher/cipher.go or cursors/cursor.go interfaces)
make mocks
```

## Architecture

**Steg** is a Go steganography tool that hides encrypted data in PNG images by modifying the least-significant bits (LSB) of the R, G, and B color channels in a pseudorandom pixel sequence.

### Core pipeline (encode direction)

```
Password → Argon2id → seed (8 B) + encKey (16 B) + macKey (32 B)
                ↓
        seed → RNGCursor (Fisher-Yates shuffled pixel order)
                ↓
        crypto/rand → 4-byte nonce written plaintext to first pixel positions
                ↓
        CipherMiddleware (AES-128 CTR, nonce + encKey, cursor seeked past nonce bytes)
                ↓
        CursorAdapter (bit-level cursor → io.ReadWriteSeeker)
                ↓
        container.WritePayload → [encrypted length][encrypted payload][encrypted HMAC-SHA256(macKey)]
```

On-image layout: `[4-byte nonce (plaintext)] [encrypted 4-byte length] [encrypted payload] [encrypted 32-byte HMAC-SHA256 tag]`

Decoding reverses the pipeline: reads the 4-byte nonce plaintext, reconstructs the same cursor/cipher stack, then reads and verifies the payload via `container.ReadPayload`.

### Package responsibilities

- **`steg/`** — Top-level encode/decode orchestration; `steg.go` derives a deterministic `int64` seed, 16-byte AES key, and 32-byte MAC key from the password via Argon2id (`deriveKeys`).
- **`steg/container/`** — Payload framing. Writes `[encrypted 4-byte length][encrypted payload][encrypted HMAC-SHA256 tag]` after the plaintext nonce. On read, verifies the HMAC-SHA256 tag keyed with `macKey`; a wrong password causes tag verification failure.
- **`cursors/`** — Three components that compose:
  - `rng_cursor.go`: Fisher-Yates shuffled pixel traversal using the seed; exposes bit-level `Read/WriteBit`.
  - `adapter.go`: Converts the bit-level `Cursor` interface into `io.ReadWriteSeeker` (bytes, MSB-first).
  - `middleware.go`: Decorator that transparently encrypts/decrypts bits passing through the cursor.
- **`cipher/`** — AES-128 counter-mode stream cipher operating at the bit level; supports `Seek()` for random access within the keystream. Accepts a pre-derived 16-byte `encKey` and a 4-byte nonce; returns an error if key setup fails.
- **`cmd/steg/`** — Cobra CLI with `encode`, `decode`, `capacity`, and `test-visual` subcommands; handles PNG, BMP, and TIFF I/O.
- **`mocks/`** — Generated mocks for `cipher.StreamCipherBlock` and `cursors.Cursor` interfaces.
- **`testutil/`** — `MemReadWriteSeeker`: in-memory `io.ReadWriteSeeker` used in tests.

### Capacity and channels

`NewRNGCursor` defaults to `R_Bit`. The `channels` parameter controls which channels are active: 1 = R only, 2 = R+G (adds `UseGreenBit()`), 3 = R+G+B (adds `UseBlueBit()`). The `bitsPerChannel` parameter (1–8) controls how many LSBs per channel are used. Image capacity in bits = `width × height × channels × bitsPerChannel`.

The `cursorOptions(seed, bitsPerChannel, channels)` helper in `steg/steg.go` builds the option slice used by `Encode`, `Decode`, `EncodeParallel`, and `DecodeParallel`.

Chunk alignment for parallel operation: `lcm(8 bits/byte, channels × bitsPerChannel bits/pixel) / 8` bytes per aligned chunk boundary. With defaults (3 channels, 1 bit/ch) this is 3 bytes = 8 pixels; values change with different settings.

## Pending security work

### 2. Plaintext nonce as a stego presence marker
The 4-byte nonce is written plaintext to the first 4 cursor positions (specific pixels determined by the Fisher-Yates shuffle seeded from the password). These bytes come from `crypto/rand` and are uncorrelated with image content. An attacker who knows the code and suspects steganography could test: "do the LSBs at these known-position pixels look uniformly random?" as a cheap presence test — even without the password.
**Mitigation**: derive the nonce from the Argon2id output (add a 4-byte nonce slice) instead of storing a plaintext random nonce. This removes the one fixed-position plaintext anchor entirely.

### 3. Full-image LSB disturbance for large payloads
For large payloads, the Fisher-Yates shuffle touches most or all pixels, globally disturbing the LSB distribution across the image. Statistical detectors (chi-square, RS analysis, SPA) become more effective as payload size approaches capacity.
**Mitigation**: pad the payload to full capacity so the LSB distribution is uniformly and consistently disturbed regardless of actual payload size (removes the payload-size signal).
