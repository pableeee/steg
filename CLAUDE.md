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
(width, height, bitsPerPixel) → k = ceil(128 / bitsPerPixel)
                ↓
        NewPixelOrder: row-major pixel list, first k Fisher-Yates steps driven by
        ChaCha8(publicOrderKey) → public salt pixels points[:k]
                ↓
        crypto/rand → randomSalt (16 B) → written in plaintext to bits 0–127 (in points[:k])
                ↓
        Password + randomSalt → Argon2id(t=2, m=64MiB, 84 B) → encKey (16 B) + macKey (32 B) + nonce (4 B) + orderKey (32 B)
                ↓
        FinishPixelOrder: Fisher-Yates steps k..N−1 driven by ChaCha8(orderKey) → full order
                ↓
        RNGCursor over the order → CipherMiddleware (AES-128 CTR, encKey + nonce; starts at bit 128)
                ↓
        CursorAdapter (Cursor → io.ReadWriteSeeker)
                ↓
        buildPaddedPayload → [4B real-length][real-payload][random padding] (fills image capacity)
                ↓
        container.WritePayload → [encrypted container-length][encrypted padded block][encrypted HMAC-SHA256(macKey)]
```

On-image layout (stream bit b lives in pixel `order[b / bitsPerPixel]`):

| Bits | Size | Cipher | Field |
|------|------|--------|-------|
| 0–127 | 16 B | none (plaintext) | randomSalt |
| 128–159 | 4 B | AES-CTR payload | container length (LE uint32) |
| 160–191 | 4 B | AES-CTR payload | real payload length (LE uint32) |
| 192–… | N B | AES-CTR payload | real payload bytes |
| …–… | P B | AES-CTR payload | random padding (fills capacity) |
| …–(…+256) | 32 B | AES-CTR payload | HMAC-SHA256 tag |

Pixel order: `publicOrderKey = SHA-256("github.com/pableeee/steg/v2 salt positions")`. Fisher-Yates step i swaps `points[i]` with `points[i + uniform(N−i)]`, where `uniform` is rejection sampling over raw `ChaCha8.Uint64()` output (never `rand.Rand.IntN`/`Shuffle`, whose algorithms are not a format contract). The order is generated over `[0,Dx)×[0,Dy)` and offset by `Bounds().Min` on access. The salt pixels depend only on the image settings; everything after them depends on Argon2id output, so guessing a password costs a KDF call. The last salt pixel can also hold the first container bits (≥128). `TestPixelOrderGolden` pins the order: any change to `cursors/order.go` is a format break. Carriers with fewer than k pixels are rejected.

Decoding reverses the pipeline: builds the public prefix, reads the 16-byte plaintext salt from bits 0–127, runs Argon2id(pass, salt) to recover all keys and finish the order, then decrypts and verifies the payload via `container.ReadPayload`, and strips the real-length prefix via `extractRealPayload`.

### Package responsibilities

- **`steg/`** — Top-level encode/decode orchestration; `validateCarrier`/`validateSettings` in `steg.go` are the single check (carrier type, bitsPerChannel 1–8, channels 1–3, ≤ `cursors.MaxPixels` pixels, ≤ 4 GiB capacity) that all four entry points run before touching a pixel; `steg.go` derives all keys via `deriveMainKeys` (Argon2id) and builds the pixel order in `prepareEncode`/`prepareDecode` (salt write/read + KDF + order) — the only place either the sequential or the parallel path gets its order; `buildPaddedPayload` / `extractRealPayload` handle full-capacity padding. `CapacityBytes` / `CapacityForDims` and the `Overhead` constant are the single source of truth for capacity — never reimplement the arithmetic elsewhere, or callers will drift from the encoder and build payloads it rejects.
- **`steg/container/`** — Payload framing. Writes `[encrypted 4-byte length][encrypted data][encrypted HMAC-SHA256 tag]`. `ReadPayload(r, hashFn, maxPayload)` verifies the HMAC-SHA256 tag keyed with `macKey`; a wrong password causes tag verification failure. `maxPayload` bounds the length field before it sizes an allocation — that field is decrypted but not yet authenticated when read, so a wrong password otherwise yields an arbitrary uint32.
- **`cursors/`** — Pixel order plus three components that compose:
  - `order.go`: `NewPixelOrder` / `FinishPixelOrder` — the format-defining ChaCha8 Fisher-Yates order (see above).
  - `rng_cursor.go`: walks a `[]uint32` order of pixel indices (`WithSharedPoints`; row-major if none) with byte-level `ReadByte/WriteByte`, reading and writing the `Pix` bytes of `*image.RGBA` / `*image.NRGBA` directly (no `At`/`Set`, no cache, no flush). Any other image type fails with `ErrUnsupportedImage`, because color conversion would destroy hidden bits. Capacity = `len(order) × bitsPerPixel`.
  - `adapter.go`: Wraps the `Cursor` interface into `io.ReadWriteSeeker`.
  - `middleware.go`: Decorator that transparently encrypts/decrypts bytes passing through the cursor via `cipher.EncryptByte`/`DecryptByte`.
- **`cipher/`** — AES-128 counter-mode stream cipher (uint32 block counter: seeking past 64 GiB is an error, never a wrap); exposes `EncryptByte`/`DecryptByte` and `Seek()` for random access within the keystream. Bit-level XOR is an internal implementation detail of `processBit`, not exposed in the `StreamCipherBlock` interface. Accepts a pre-derived 16-byte `encKey` and a 4-byte nonce; returns an error if key setup fails.
- **`cmd/steg/`** — Cobra CLI with `encode`, `decode`, `capacity`, `detect` (steganalysis via `steg/analysis`), and `test-visual` subcommands; handles PNG, BMP, and TIFF I/O. Carriers with any non-opaque pixel are rejected. `encode` rejects unknown output extensions, writes to a temporary sibling, decodes that file with the same path `decode` uses, and only renames it onto the output if the payload round-trips. `decode` writes its output as a 0600 temp file renamed into place. `cmd.SilenceUsage` is set in every `RunE` so only flag errors print usage.
- **`steg/analysis/`** — Chi-square and RS steganalysis (`Analyze`, `ChiSquare`, `RSAnalysis`); the only implementation, shared by `steg detect` and `cmd/attack1`.
- **`cmd/attack1`, `cmd/attack4`** — Attack tooling for `docs/attack-analysis.md`: steganalysis report over `steg/analysis`, and a dictionary attack that calls `steg.Decode` per candidate. Both use library APIs only, so they cannot drift from the format.
- **`mocks/`** — Generated mocks for `cipher.StreamCipherBlock` and `cursors.Cursor` interfaces.
- **`testutil/`** — `MemReadWriteSeeker`: in-memory `io.ReadWriteSeeker` used in tests.

### Capacity and channels

`NewRNGCursor` defaults to `R_Bit`. The `channels` parameter controls which channels are active: 1 = R only, 2 = R+G (adds `UseGreenBit()`), 3 = R+G+B (adds `UseBlueBit()`). The `bitsPerChannel` parameter (1–8) controls how many LSBs per channel are used. Image capacity in bits = `width × height × channels × bitsPerChannel`.

The `cursorOptions(points, bitsPerChannel, channels)` helper in `steg/steg.go` builds the option slice used by `Encode`, `Decode`, `EncodeParallel`, and `DecodeParallel`.

Chunk alignment for parallel operation: `lcm(8 bits/byte, channels × bitsPerChannel bits/pixel) / 8` bytes per aligned chunk boundary. With defaults (3 channels, 1 bit/ch) this is 3 bytes = 8 pixels; values change with different settings.

That alignment only makes chunk *sizes* pixel-aligned. The payload also starts at bit 160 (16-byte salt + 4-byte length), and 160 is not a multiple of `bitsPerPixel` when `bitsPerPixel` is divisible by 3 — including the default of 3. `firstChunkShift` in `steg/parallel.go` shifts the first chunk by the remainder so every later boundary lands on a pixel boundary. Without it two workers read-modify-write the same boundary pixel and one silently discards the other's bits, producing an image that fails MAC verification. With aligned chunks each worker owns whole pixels and writes disjoint `Pix` bytes, which is why the parallel path needs no lock (a lock per access would not fix misalignment anyway: the read-modify-write *sequence* is not atomic). `TestChunkBoundariesArePixelAligned` guards the invariant arithmetically, because the race window is too narrow for a round-trip test to catch reliably.

