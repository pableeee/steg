# steg

**steg** is a command-line steganography tool written in Go. It hides an arbitrary file inside a PNG, BMP, or TIFF image by modifying the least-significant bits of selected color channels in a pseudorandom pixel sequence. The number of bits per channel (1–8) and the number of channels (R / R+G / R+G+B) are configurable, trading capacity for visual detectability. The hidden data is encrypted and authenticated, so the carrier image looks near-identical to the original while the payload is unreadable and tamper-evident without the correct password.

[![Go Reference](https://pkg.go.dev/badge/github.com/pableeee/steg.svg)](https://pkg.go.dev/github.com/pableeee/steg)
[![CI](https://github.com/pableeee/steg/actions/workflows/release.yml/badge.svg)](https://github.com/pableeee/steg/actions/workflows/release.yml)

---

## Table of contents

- [Features](#features)
- [Installation](#installation)
- [Usage](#usage)
- [Capacity](#capacity)
- [Performance](#performance)
- [Security design](#security-design)
- [On-image layout](#on-image-layout)
- [Architecture](#architecture)
- [Development](#development)
- [Known limitations](#known-limitations)
- [Roadmap](#roadmap)

---

## Features

- **Authenticated encryption** — AES-128-CTR encryption with HMAC-SHA256; a wrong password returns an error, never garbled data.
- **Strong key derivation** — Argon2id (OWASP 2023 interactive profile: time=1, mem=64 MiB, threads=4) derives independent encryption and MAC keys from the password in a single call.
- **Random nonce per encode** — `crypto/rand` generates a fresh 4-byte nonce on every encode, so encoding the same payload into the same image twice produces two different outputs.
- **Password-keyed pixel traversal** — pixels are visited in a Fisher-Yates-shuffled order derived from the password; an observer without the password cannot locate which pixels carry data.
- **Configurable capacity vs. detectability** — `--bits-per-channel` (1–8 LSBs per channel) and `--channels` (1=R, 2=R+G, 3=R+G+B) let you trade off payload capacity against visual impact. At 1 bit/channel no pixel changes by more than ±1.
- **`capacity` command** — prints a table of usable byte capacity for every (channels × bits-per-channel) combination for a given image.
- **`test-visual` command** — generates carrier images filled to capacity at every encoding intensity for side-by-side visual comparison.
- **Multiple image formats** — PNG, BMP, and TIFF are supported as both input and output.
- **Parallel mode** — a worker-pool implementation (`-P`) scales encode/decode across all available CPUs, giving up to ~2.5× speedup on large images.
- **Interoperable modes** — images encoded with the sequential path can be decoded with the parallel path and vice versa.

---

## Installation

### Pre-built binaries

Download the latest binary for your platform from the [Releases](https://github.com/pableeee/steg/releases) page. Releases are tagged `v<YYYYMMDD>-<commit>` and published automatically on every push to `master`.

| Platform | File |
|---|---|
| Linux amd64 | `steg-linux-amd64` |
| Linux arm64 | `steg-linux-arm64` |
| macOS amd64 | `steg-darwin-amd64` |
| macOS arm64 (Apple Silicon) | `steg-darwin-arm64` |
| Windows amd64 | `steg-windows-amd64.exe` |

After downloading, make the binary executable (Linux/macOS):

```bash
chmod +x steg-linux-amd64
sudo mv steg-linux-amd64 /usr/local/bin/steg
```

### Build from source

Requires Go 1.24+.

```bash
go install github.com/pableeee/steg/cmd/steg@latest
```

Or clone and build manually:

```bash
git clone https://github.com/pableeee/steg.git
cd steg
make build        # produces cmd/steg/steg
```

---

## Usage

### Encode

Hide a file inside a carrier image:

```bash
steg encode -i carrier.png -f secret.txt -o output.png -p "my passphrase"
```

| Flag | Short | Default | Description |
|---|---|---|---|
| `--input_image` | `-i` | — | Carrier image (PNG, BMP, or TIFF) |
| `--input_file` | `-f` | — | File to hide |
| `--output_image` | `-o` | — | Output image containing the hidden data |
| `--password` | `-p` | — | Passphrase (**required**) |
| `--bits-per-channel` | `-b` | `1` | Number of LSBs to use per color channel (1–8) |
| `--channels` | `-c` | `3` | Color channels to use: 1=R, 2=R+G, 3=R+G+B |
| `--parallel` | `-P` | off | Use parallel worker pool (faster on large images) |

### Decode

Recover a hidden file from a carrier image:

```bash
steg decode -i output.png -o recovered.txt -p "my passphrase"
```

| Flag | Short | Default | Description |
|---|---|---|---|
| `--input_image` | `-i` | — | Image containing the hidden data |
| `--output_file` | `-o` | — | Path for the recovered file |
| `--password` | `-p` | — | Passphrase (**required**) |
| `--bits-per-channel` | `-b` | `1` | Must match the value used during encode |
| `--channels` | `-c` | `3` | Must match the value used during encode |
| `--parallel` | `-P` | off | Use parallel worker pool (faster on large images) |

### Capacity

Show usable byte capacity for every (channels × bits-per-channel) combination:

```bash
steg capacity -i carrier.png
```

```
carrier.png — 1920 × 1080 px

                        1 bits/ch      2 bits/ch      4 bits/ch      8 bits/ch
  1 channel  (R)       253.11 KB     506.23 KB       1.01 MB       2.01 MB
  2 channels (R+G)     506.23 KB       1.01 MB       2.01 MB       4.01 MB
  3 channels (R+G+B)   759.34 KB       1.49 MB       3.02 MB       6.03 MB

Overhead: 40 B (4 nonce + 4 length + 32 HMAC).
```

### Test Visual

Generate carrier images filled to capacity at every intensity for side-by-side comparison:

```bash
steg test-visual -i carrier.png -o ./visual/ -p "mypass"
```

Writes up to 12 PNGs (`visual_ch{1-3}_b{1,2,4,8}.png`) into the output directory.

### Example

```bash
# Hide a document with default settings (3 channels, 1 bit/channel)
steg encode -i photo.png -f report.pdf -o photo_steg.png -p "hunter2"

# Recover it
steg decode -i photo_steg.png -o report_recovered.pdf -p "hunter2"

# Use 2 channels and 2 bits/channel for more capacity
steg encode -c 2 -b 2 -i photo.png -f archive.tar.gz -o out.png -p "hunter2"
steg decode -c 2 -b 2 -i out.png -o archive.tar.gz -p "hunter2"

# Use parallel mode for large images
steg encode -P -i 4k_photo.png -f big_archive.tar.gz -o out.png -p "hunter2"

# Encode into a BMP carrier; decode back
steg encode -i photo.bmp -f secret.txt -o out.bmp -p "hunter2"
steg decode -i out.bmp -o recovered.txt -p "hunter2"

# Check capacity before encoding
steg capacity -i photo.png
```

---

## Capacity

The usable payload capacity depends on the image dimensions and the chosen `--channels` / `--bits-per-channel` settings:

```
max_payload = max(0, floor( width × height × channels × bitsPerChannel / 8 ) − 40)  bytes
```

The 40-byte overhead covers: 4-byte nonce + 4-byte encrypted length + 32-byte encrypted HMAC tag.

Default settings (3 channels, 1 bit/channel):

| Image size | Pixels | Max payload |
|---|---|---|
| 100 × 100 | 10,000 | ~3.6 KB |
| 500 × 500 | 250,000 | ~91.5 KB |
| 1920 × 1080 (FHD) | 2,073,600 | ~759 KB |
| 3840 × 2160 (4K) | 8,294,400 | ~2.97 MB |

Use `steg capacity -i <image>` to print a full table for all (channels × bits-per-channel) combinations at once. If the payload exceeds the image capacity, encode returns an error.

---

## Performance

Benchmarks run on an AMD Ryzen 9 9950X3D (32 logical cores, Go 1.24, `GOMAXPROCS=32`). Key-derivation time (Argon2id, ~10 ms) dominates at small sizes.

| Image | Payload | Encode seq | Encode par | Decode seq | Decode par |
|---|---|---|---|---|---|
| 100 × 100 | 1 KB | 10.1 ms | 10.2 ms | 11.6 ms | 10.2 ms |
| 500 × 500 | 50 KB | 18.7 ms | 13.5 ms | 15.8 ms | 12.8 ms |
| 2000 × 2000 | 500 KB | 213 ms | 126 ms | 173 ms | 118 ms |
| 3840 × 2160 (4K) | 2 MB | 833 ms | 327 ms | 759 ms | 306 ms |

Run the benchmarks yourself:

```bash
go test ./steg/ -bench=BenchmarkEncodeBySize -benchtime=3s -benchmem
go test ./steg/ -bench=BenchmarkDecodeBySize -benchtime=3s -benchmem
```

---

## Security design

### Cryptographic components

| Component | Algorithm | Notes |
|---|---|---|
| Key derivation | Argon2id | time=1, mem=64 MiB, threads=4 (OWASP 2023 interactive) |
| RNG seed | First 8 bytes of KDF output | Drives pixel-order shuffle |
| Encryption key | Bytes 8–23 of KDF output | 16-byte AES-128 key |
| MAC key | Bytes 24–55 of KDF output | 32-byte HMAC-SHA256 key |
| Stream cipher | AES-128-CTR | Custom bit-addressable CTR; seekable keystream |
| Authentication | HMAC-SHA256 | Keyed with independent MAC key; constant-time comparison |
| Nonce | `crypto/rand` (4 bytes) | Stored in plaintext at the start of the pixel sequence |

### Threat model

- **Confidentiality** — AES-128-CTR with a strong KDF-derived key. An attacker without the password sees only pseudorandom bits across a pseudorandomly-ordered set of pixels.
- **Integrity / authentication** — HMAC-SHA256 over the plaintext payload, encrypted alongside it. A wrong password or any bit-flip in the encrypted region produces a MAC failure; no plaintext is returned.
- **Resistance to brute force** — Argon2id with 64 MiB memory requirement makes offline dictionary attacks expensive, even on GPU hardware.
- **Keystream uniqueness** — A fresh `crypto/rand` nonce per encode prevents keystream reuse even when the same password and carrier are reused.
- **Pixel deniability** — Without the password, an attacker cannot determine which pixels carry data (the traversal order is derived from the password via Argon2id).

### What steg does not protect against

- An attacker who can compare the carrier and the steg image pixel-by-pixel will observe that the LSBs of R, G, and B channels differ from a typical natural image distribution (statistical steganalysis).
- The Argon2id salt is a fixed domain separator (`"github.com/pableeee/steg/v1"`), not a per-image random value. This makes targeted precomputation marginally cheaper than with a random salt, though the memory-hardness of Argon2id still provides strong protection in practice.

---

## On-image layout

Bits are stored in the shuffled pixel sequence, green channel before blue within each pixel:

```
Bit offset        Size        Encryption     Field
──────────────────────────────────────────────────────────────────
0                 32 bits     Plaintext      Nonce (uint32, big-endian)
32                32 bits     AES-128-CTR    Payload length (uint32, little-endian)
64                N×8 bits    AES-128-CTR    Payload bytes
64 + N×8          256 bits    AES-128-CTR    HMAC-SHA256 tag
```

The cipher is seeked to bit offset 32 before encryption begins, keeping keystream position 0 aligned with the first encrypted byte. The nonce is written and read in plaintext without involving the cipher.

---

## Architecture

```
┌─────────────────────────────────────────────────┐
│  cmd/steg  (Cobra CLI, PNG I/O)                 │
└──────────────────┬──────────────────────────────┘
                   │ draw.Image + password
┌──────────────────▼──────────────────────────────┐
│  steg.Encode / Decode / EncodeParallel /        │
│  DecodeParallel  (orchestration, deriveKeys)    │
└──────┬────────────────────────────┬─────────────┘
       │ seed                       │ encKey, macKey
┌──────▼────────────┐   ┌───────────▼─────────────┐
│  RNGCursor        │   │  cipher.NewCipher       │
│  pixel traversal  │   │  AES-128-CTR, seekable  │
│  pixel cache      │   │  bit/byte-level XOR     │
└──────┬────────────┘   └───────────┬─────────────┘
       │ Cursor                     │ StreamCipherBlock
┌──────▼────────────────────────────▼─────────────┐
│  CipherMiddleware  (encrypt/decrypt per byte)   │
└──────┬──────────────────────────────────────────┘
       │ Cursor (ReadByte / WriteByte)
┌──────▼──────────────────────────────────────────┐
│  CursorAdapter  (Cursor → io.ReadWriteSeeker)   │
└──────┬──────────────────────────────────────────┘
       │ io.ReadWriteSeeker + hash.Hash
┌──────▼──────────────────────────────────────────┐
│  container.WritePayload / ReadPayload           │
│  [4B length][payload][32B HMAC tag]             │
└─────────────────────────────────────────────────┘
```

### Package responsibilities

| Package | Responsibility |
|---|---|
| `cmd/steg` | Cobra CLI; PNG/BMP/TIFF file I/O; `encode`, `decode`, `capacity`, and `test-visual` subcommands |
| `steg` | Encode/decode orchestration; Argon2id key derivation; parallel worker pool |
| `steg/container` | Payload framing (length prefix + HMAC tag); constant-time tag verification |
| `cursors` | `RNGCursor` (Fisher-Yates pixel traversal, write-back pixel cache), `CursorAdapter` (byte↔bit bridge), `CipherMiddleware` (transparent encrypt/decrypt) |
| `cipher` | AES-128 CTR stream cipher; bit- and byte-addressable keystream; seekable |
| `mocks` | Auto-generated gomock mocks for `Cursor` and `StreamCipherBlock` interfaces |
| `testutil` | `MemReadWriteSeeker` in-memory helper for tests |

---

## Development

### Prerequisites

- Go 1.24+
- `make`

### Commands

```bash
# Build the CLI binary
make build

# Install the binary to $GOPATH/bin (or $GOBIN)
make install

# Run all tests
make test

# Run tests with the race detector
go test -race ./steg/

# Run a single test
go test ./steg/ -run TestEncodeRoundTrip

# Regenerate mocks (after editing cipher/cipher.go or cursors/cursor.go interfaces)
make mocks

# Run benchmarks
go test ./steg/ -bench=BenchmarkEncodeBySize -benchtime=3s -benchmem
go test ./steg/ -bench=BenchmarkDecodeBySize -benchtime=3s -benchmem
```

### Project layout

```
.
├── cipher/          # AES-128-CTR stream cipher
├── cmd/steg/        # CLI entry point (Cobra)
├── cursors/         # RNGCursor, CursorAdapter, CipherMiddleware
├── docs/            # Technical spec, ADRs, release notes
├── mocks/           # Auto-generated gomock mocks
├── steg/            # Encode/decode orchestration, container framing
│   └── container/
└── testutil/        # Shared test helpers
```

### Continuous integration

Every push to `master` triggers a GitHub Actions workflow that:

1. Runs `go test ./...`
2. Cross-compiles binaries for Linux, macOS, and Windows (amd64 + arm64)
3. Publishes a GitHub Release tagged `v<YYYYMMDD>-<short-sha>` with all binaries attached

---

## Known limitations

| Issue | Severity | Notes |
|---|---|---|
| MAC-then-Encrypt ordering | Low | HMAC is computed over plaintext before encryption. Unconventional (Encrypt-then-MAC is preferred), but not exploitable in this threat model since the tag is inside the encrypted channel. |
| Fixed application salt | Low | Per-image random Argon2id salt would marginally harden against targeted precomputation; not implemented to avoid bootstrapping complexity. |
| No streaming decode | Medium | `ReadPayload` allocates the full payload in memory before returning. Very large payloads may cause high memory usage. |
| Lossy formats unsupported | High | JPEG and other lossy formats destroy LSB data. Only lossless formats (PNG, BMP, TIFF) are supported. |
| Nonce integrity | Low | The 4-byte plaintext nonce has no MAC. Flipping nonce bits changes the keystream, causing a MAC failure at decode — the correct outcome — but the error does not distinguish nonce tampering from a wrong password. |
| Statistical steganalysis | Medium | Modifying the LSBs of color channels across a pseudorandom pixel set produces a detectable statistical signature to an observer who analyses the carrier image's LSB distribution. Higher bits-per-channel settings make this more pronounced. |

---

## Roadmap

- **Per-image random Argon2id salt** — store a random salt in the plaintext header alongside the nonce to further harden against precomputation.
- **Lossless WebP support** — extend format support beyond PNG, BMP, and TIFF.
- **Streaming decode** — avoid loading the full payload into memory for large files.
- **16-bit image depth** — exploit the extra bits-per-channel available in 16-bit PNG/TIFF carriers.
