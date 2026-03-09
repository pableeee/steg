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
Password → SHA-256 → seed (8 B)
                ↓
        seed → RNGCursor (Fisher-Yates shuffled pixel order)
                ↓
        crypto/rand → randomSalt (16 B) → written in plaintext to bits 0–127
                ↓
        Password + randomSalt → Argon2id(t=2, m=64MiB) → encKey (16 B) + macKey (32 B) + nonce (4 B)
                ↓
        CipherMiddleware (AES-128 CTR, encKey + nonce; starts at bit 128)
                ↓
        CursorAdapter (bit-level cursor → io.ReadWriteSeeker)
                ↓
        buildPaddedPayload → [4B real-length][real-payload][random padding] (fills image capacity)
                ↓
        container.WritePayload → [encrypted container-length][encrypted padded block][encrypted HMAC-SHA256(macKey)]
```

On-image layout (in Fisher-Yates pixel bit order):

| Bits | Size | Cipher | Field |
|------|------|--------|-------|
| 0–127 | 16 B | none (plaintext) | randomSalt |
| 128–159 | 4 B | AES-CTR payload | container length (LE uint32) |
| 160–191 | 4 B | AES-CTR payload | real payload length (LE uint32) |
| 192–… | N B | AES-CTR payload | real payload bytes |
| …–… | P B | AES-CTR payload | random padding (fills capacity) |
| …–(…+256) | 32 B | AES-CTR payload | HMAC-SHA256 tag |

Decoding reverses the pipeline: derives seed via SHA-256(pass), reads the 16-byte plaintext salt from bits 0–127, runs Argon2id(pass, salt) to recover all keys, then decrypts and verifies the payload via `container.ReadPayload`, and strips the real-length prefix via `extractRealPayload`.

### Package responsibilities

- **`steg/`** — Top-level encode/decode orchestration; `steg.go` derives the pixel-traversal seed via `deriveSeed` (SHA-256) and all crypto keys via `deriveMainKeys` (Argon2id); `buildPaddedPayload` / `extractRealPayload` handle full-capacity padding.
- **`steg/container/`** — Payload framing. Writes `[encrypted 4-byte length][encrypted data][encrypted HMAC-SHA256 tag]`. On read, verifies the HMAC-SHA256 tag keyed with `macKey`; a wrong password causes tag verification failure.
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

