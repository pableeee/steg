# steg

**steg** is a command-line steganography tool written in Go. It hides an arbitrary file inside a PNG, BMP, or TIFF image by modifying the least-significant bits of selected color channels in a pseudorandom pixel sequence. The number of bits per channel (1–8) and the number of channels (R / R+G / R+G+B) are configurable, trading capacity for visual detectability. The hidden data is encrypted and authenticated, so the carrier image looks near-identical to the original while the payload is unreadable and tamper-evident without the correct password.

> **On-image format change.** The pixel traversal order is now derived from the per-image salt and the password through Argon2id (see [Security design](#security-design)). Images encoded by earlier builds cannot be decoded by this one, and vice versa: decode an older image with the binary that created it. A mismatch fails the same way a wrong password does.

[![Go Reference](https://pkg.go.dev/badge/github.com/pableeee/steg.svg)](https://pkg.go.dev/github.com/pableeee/steg)
[![Test](https://github.com/pableeee/steg/actions/workflows/test.yml/badge.svg)](https://github.com/pableeee/steg/actions/workflows/test.yml)
[![Release](https://github.com/pableeee/steg/actions/workflows/release.yml/badge.svg)](https://github.com/pableeee/steg/actions/workflows/release.yml)

---

## Table of contents

- [Features](#features)
- [Installation](#installation)
- [Usage](#usage)
  - [Supplying the password](#supplying-the-password)
- [Capacity](#capacity)
- [Performance](#performance)
- [Security design](#security-design)
- [On-image layout](#on-image-layout)
- [Architecture](#architecture)
- [Development](#development)
- [Known limitations](#known-limitations)
- [Steganalysis](#steganalysis)
  - [Trained detector: `stegbench`](#trained-detector-stegbench)
- [Next steps](#next-steps)
- [Roadmap](#roadmap)

---

## Features

- **Authenticated encryption** — AES-128-CTR encryption with HMAC-SHA256; a wrong password returns an error, never garbled data.
- **Strong key derivation** — Argon2id (time=2, mem=64 MiB, threads=4) derives independent encryption and MAC keys, the cipher nonce and the pixel-order key from the password and per-image random salt in a single call.
- **Random salt per encode** — `crypto/rand` generates a fresh 16-byte salt on every encode, stored in plaintext at publicly known pixel positions; Argon2id derives all keys and the cipher nonce from the password and this salt, so each encode produces a unique keystream even with the same password and carrier.
- **Keyed pixel traversal** — after the salt pixels, pixels are visited in a Fisher-Yates order driven by ChaCha8 under a key from Argon2id, so recomputing the order for a guessed password costs a full KDF call. With bounded padding only a prefix of this order is written, so the touched pixels are a keyed pseudorandom subset spread over the whole image (see [Security design](#security-design)).
- **Bounded padding** — the payload is padded to a power-of-two bucket of at least 4 KiB (`max(4096, nextPowerOfTwo(4 + payload))`), not to the whole carrier; pixels past the end of the stream keep their original values. A 31-byte text in a 1024×1536 photo changes 0.6% of the pixels instead of 87.5%. Payloads whose bucket does not fit fill the carrier as before. The bucket hides the payload size only to within a power of two.
- **LSB matching at 1 bit per channel** — a channel whose low bit must change moves ±1 in a random direction instead of to its pair partner, which removes the signature chi-square and RS analysis look for. The decoder reads the same bits, so images stay compatible both ways. With 2–8 bits per channel the encoder still uses plain replacement.
- **Safe CLI writes** — `encode` refuses transparent carriers, and verifies that the image it wrote decodes back to the payload before putting it at the output path.
- **Configurable capacity vs. detectability** — `--bits-per-channel` (1–8 LSBs per channel) and `--channels` (1=R, 2=R+G, 3=R+G+B) let you trade off payload capacity against visual impact. At 1 bit/channel no pixel changes by more than ±1.
- **`capacity` command** — prints a table of usable byte capacity for every (channels × bits-per-channel) combination for a given image.
- **`test-visual` command** — generates carrier images filled to capacity at every encoding intensity for side-by-side visual comparison.
- **`detect` command** — runs chi-square and RS steganalysis on any image and reports a per-channel verdict (`CLEAN` / `SUSPICIOUS` / `LIKELY_STEGO`).
- **`stegbench` benchmark** — measures how detectable LSB replacement, LSB matching and the real encoder are against a trained SPAM + FLD-ensemble detector, as well as chi-square and RS, over a corpus of cover images (see [Trained detector: `stegbench`](#trained-detector-stegbench)).
- **Multiple image formats** — PNG, BMP, and TIFF are supported as both input and output.
- **Parallel mode** — a worker-pool implementation (`-P`) spreads encode/decode across all available CPUs; on large images it is about 2.5–3.7× faster than the sequential path (see [Performance](#performance)).
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

Requires Go 1.25+.

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

### Supplying the password

Every command that needs a passphrase resolves it in this order:

1. **`--password` / `-p`** — convenient, but the value is visible to every other
   user on the machine via the process table (`ps aux`) and is written to your
   shell history. Prefer it only in scripts where neither matters.
2. **`STEG_PASSWORD` environment variable** — better for scripting and CI.
3. **Interactive prompt** — used when neither of the above is set and stdin is a
   terminal. `encode` asks for confirmation; `decode` does not.

If no password is available and stdin is not a terminal, the command fails
rather than proceeding with an empty passphrase.

```bash
steg encode -i carrier.png -f secret.txt -o output.png   # prompts
STEG_PASSWORD="my passphrase" steg decode -i output.png -o recovered.txt
```

### Encode

Hide a file inside a carrier image:

```bash
steg encode -i carrier.png -f secret.txt -o output.png -p "my passphrase"
```

| Flag | Short | Default | Description |
|---|---|---|---|
| `--input_image` | `-i` | required | Carrier image (PNG, BMP, or TIFF) |
| `--input_file` | `-f` | required | File to hide |
| `--output_image` | `-o` | required | Output image; the extension picks the format and must be `.png`, `.bmp`, `.tif` or `.tiff` |
| `--password` | `-p` | prompt | Passphrase; see [Supplying the password](#supplying-the-password) |
| `--bits-per-channel` | `-b` | `1` | Number of LSBs to use per color channel (1–8) |
| `--channels` | `-c` | `3` | Color channels to use: 1=R, 2=R+G, 3=R+G+B |
| `--parallel` | `-P` | off | Use parallel worker pool (see [Performance](#performance)) |

What `encode` does, in order:

1. **Checks the output extension.** Anything other than `.png`, `.bmp`, `.tif` or `.tiff` (case-insensitive) is rejected before any other work; a lossy format such as JPEG would destroy the hidden bits.
2. **Checks the carrier.** The carrier is loaded and rejected if any pixel is not fully opaque, whatever the output format:
   ```
   Error: carrier image transparent.png has transparency (non-opaque pixels); hidden data cannot be stored reliably in transparent pixels. Remove the alpha channel first (flatten the image onto an opaque background)
   ```
   Flatten the image onto an opaque background in an image editor (or remove its alpha channel) and try again.
3. **Reads the payload file.** These checks happen before the password prompt, so an unusable carrier or a missing file fails before you type anything.
4. **Asks for the password** (with confirmation) if it was not supplied another way.
5. **Embeds, then verifies.** A payload larger than the carrier's capacity is rejected here. The result is written to a temporary file next to the output, decoded through the same code path `steg decode` uses, and compared byte for byte with the payload. Only if they match is the temporary file renamed onto the output path. On any failure nothing is written, and an existing file at the output path is left untouched.

### Decode

Recover a hidden file from a carrier image:

```bash
steg decode -i output.png -o recovered.txt -p "my passphrase"
```

| Flag | Short | Default | Description |
|---|---|---|---|
| `--input_image` | `-i` | required | Image containing the hidden data |
| `--output_file` | `-o` | required | Path for the recovered file |
| `--password` | `-p` | prompt | Passphrase; see [Supplying the password](#supplying-the-password) |
| `--bits-per-channel` | `-b` | `1` | Must match the value used during encode |
| `--channels` | `-c` | `3` | Must match the value used during encode |
| `--parallel` | `-P` | off | Use parallel worker pool (faster on large images) |

The output file is created only after the payload has been decoded and authenticated, so a wrong password leaves nothing behind and an existing file at that path is left untouched. The recovered file is written to a temporary file with mode `0600` (owner read/write only, subject to your umask) and renamed into place, so it is never left half-written and never world-readable; an existing file it replaces also ends up `0600`. If the output path is a symlink, device or FIFO (for example `-o /dev/stdout`), the data is written through it in place instead.

A missing required flag prints the command's usage; any other error (wrong password, unreadable file) prints only the error.

### Capacity

Show usable byte capacity for every (channels × bits-per-channel) combination:

```bash
steg capacity -i carrier.png
```

```
carrier.png — 1920 × 1080 px

                            1 bits/ch      2 bits/ch      4 bits/ch      8 bits/ch
  1 channel  (R)            253.07 KB      506.20 KB     1012.45 KB        1.98 MB
  2 channels (R+G)          506.20 KB     1012.45 KB        1.98 MB        3.96 MB
  3 channels (R+G+B)        759.32 KB        1.48 MB        2.97 MB        5.93 MB

Overhead: 56 B (16 plaintext-salt + 4 container-length + 4 real-length + 32 HMAC).
```

### Test Visual

Generate carrier images filled to capacity at every intensity for side-by-side comparison:

```bash
steg test-visual -i carrier.png -o ./visual/ -p "mypass"
```

Writes up to 12 PNGs (`visual_ch{1-3}_b{1,2,4,8}.png`) into the output directory. Like `encode`, it rejects carriers with transparent pixels.

### Detect

Run steganalysis on an image to check for LSB steganography:

```bash
steg detect -i image.png
```

| Flag | Short | Description |
|---|---|---|
| `--input_image` | `-i` | Image to analyse (PNG, BMP, TIFF) |

Example output for a 1024×1536 photograph before encoding:

```
Chi-square analysis (p-value > 0.05 = suspicious):
  R: χ²=454.94      df=127  p=0.0000  [CLEAN]
  G: χ²=1583.51     df=115  p=0.0000  [CLEAN]
  B: χ²=2274.04     df=80   p=0.0000  [CLEAN]

RS analysis (asymmetry < -0.02 = suspicious):
  R: Rm=0.5196  Sm=0.4804  R-m=0.5241  S-m=0.4759  asymmetry=-0.0045  [CLEAN]
  G: Rm=0.5206  Sm=0.4794  R-m=0.5223  S-m=0.4777  asymmetry=-0.0017  [CLEAN]
  B: Rm=0.5191  Sm=0.4809  R-m=0.5246  S-m=0.4754  asymmetry=-0.0055  [CLEAN]

Verdict: CLEAN
```

The same photograph after a default encode (3 channels, 1 bit/channel) of a 31-byte text file. Bounded padding writes a 4 KiB block, so only about 0.6% of the pixels change, each channel by at most ±1:

```
Chi-square analysis (p-value > 0.05 = suspicious):
  R: χ²=453.74      df=127  p=0.0000  [CLEAN]
  G: χ²=1595.36     df=115  p=0.0000  [CLEAN]
  B: χ²=2263.58     df=80   p=0.0000  [CLEAN]

RS analysis (asymmetry < -0.02 = suspicious):
  R: Rm=0.5195  Sm=0.4805  R-m=0.5242  S-m=0.4758  asymmetry=-0.0046  [CLEAN]
  G: Rm=0.5205  Sm=0.4795  R-m=0.5222  S-m=0.4778  asymmetry=-0.0017  [CLEAN]
  B: Rm=0.5191  Sm=0.4809  R-m=0.5245  S-m=0.4755  asymmetry=-0.0054  [CLEAN]

Verdict: CLEAN
```

The ±1 directions are random, so the numbers vary slightly from one encode to the next. The previous encoder (LSB replacement, padding to the full carrier) and the current one on the same photograph:

| Encoder | Payload | Pixels changed | chi-square p (R/G/B) | RS asymmetry (R/G/B) | Verdict |
|---|---|---|---|---|---|
| previous | 31 B | 87.5% | 0.4973 / 0.2542 / 0.5285 | −0.0444 / −0.0445 / −0.0443 | `LIKELY_STEGO` |
| previous | 589,768 B (full) | 87.5% | 0.6226 / 0.1330 / 0.9523 | −0.0432 / −0.0426 / −0.0431 | `LIKELY_STEGO` |
| current | 31 B | 0.62% | 0.0000 / 0.0000 / 0.0000 | −0.0047 / −0.0014 / −0.0057 | `CLEAN` |
| current | 100,000 B | 19.4% | 0.0000 / 0.0000 / 0.0000 | −0.0041 / −0.0017 / −0.0053 | `CLEAN` |
| current | 589,768 B (full) | 87.6% | 0.0000 / 0.0000 / 0.0000 | −0.0014 / +0.0014 / −0.0015 | `CLEAN` |
| current, `-b 2` | 31 B | 0.35% | 0.0000 / 0.0000 / 0.0000 | −0.0046 / −0.0020 / −0.0056 | `CLEAN` |

A `CLEAN` verdict means these two tests found nothing, not that the image is undetectable; see [Steganalysis](#steganalysis). With 2 or more bits per channel the encoder still uses replacement, so a large payload at `-b 2` or above is flagged.

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

# Analyse an image for signs of LSB steganography
steg detect -i photo.png
steg detect -i suspected_steg.png
```

---

## Capacity

The usable payload capacity depends on the image dimensions and the chosen `--channels` / `--bits-per-channel` settings:

```
max_payload = max(0, floor( width × height × channels × bitsPerChannel / 8 ) − 56)  bytes
```

The 56-byte overhead covers: 16-byte plaintext salt + 4-byte encrypted container length + 4-byte encrypted real-length prefix + 32-byte encrypted HMAC tag.

Default settings (3 channels, 1 bit/channel):

| Image size | Pixels | Max payload |
|---|---|---|
| 100 × 100 | 10,000 | 3,694 B (~3.6 KB) |
| 500 × 500 | 250,000 | 93,694 B (~91.5 KB) |
| 1920 × 1080 (FHD) | 2,073,600 | 777,544 B (~759 KB) |
| 3840 × 2160 (4K) | 8,294,400 | 3,110,344 B (~2.97 MB) |

Use `steg capacity -i <image>` to print a full table for all (channels × bits-per-channel) combinations at once. If the payload exceeds the image capacity, encode returns an error.

---

## Performance

Benchmarks run on an AMD Ryzen 9 9950X3D (32 logical cores, Go 1.25, `GOMAXPROCS=32`). Key-derivation time (Argon2id, ~17 ms) dominates at small sizes.

| Image | Payload | Encode seq | Encode par | Decode seq | Decode par |
|---|---|---|---|---|---|
| 100 × 100 | 1 KB | 16.6 ms | 16.7 ms | 16.7 ms | 16.6 ms |
| 500 × 500 | 50 KB | 24.3 ms | 20.0 ms | 22.9 ms | 19.3 ms |
| 2000 × 2000 | 500 KB | 162 ms | 65 ms | 147 ms | 55 ms |
| 3840 × 2160 (4K) | 2 MB | 495 ms | 143 ms | 454 ms | 122 ms |

Both directions scale with `-P` on large images. What remains serial is Argon2id and the Fisher-Yates shuffle that builds the pixel order (about 90 ms at 4K); both are fixed by the format. The cursor reads and writes the image's pixel bytes directly, and parallel workers own disjoint, pixel-aligned ranges of the order, so they need no lock.

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
| Salt pixel positions | ChaCha8 under a fixed public key (SHA-256 of `"github.com/pableeee/steg/v2 salt positions"`) | Public by design: the first k = ⌈128 / bits-per-pixel⌉ pixels of the order depend only on image size and settings |
| Per-image salt | `crypto/rand` (16 bytes) | Stored in plaintext at stream bits 0–127, in the salt pixels; unique per encode |
| Key derivation | Argon2id | time=2, mem=64 MiB, threads=4, 84-byte output; keyed with password + salt |
| Encryption key | KDF output bytes 0–15 | 16-byte AES-128 key |
| MAC key | KDF output bytes 16–47 | 32-byte HMAC-SHA256 key |
| Payload nonce | KDF output bytes 48–51 | 4-byte AES-CTR nonce; unique per encode via random salt |
| Order key | KDF output bytes 52–83 | 32-byte ChaCha8 key that finishes the Fisher-Yates pixel order after the salt pixels |
| Stream cipher | AES-128-CTR | Custom bit-addressable CTR; seekable keystream |
| Authentication | HMAC-SHA256 | Keyed with independent MAC key; constant-time comparison |

### Threat model

- **Confidentiality** — AES-128-CTR with a strong KDF-derived key. An attacker without the password sees only pseudorandom bits across a pseudorandomly-ordered set of pixels.
- **Integrity / authentication** — HMAC-SHA256 over the plaintext padded block (real-length prefix, payload and padding), encrypted alongside it. A wrong password or any bit-flip in the encrypted region produces a MAC failure; no plaintext is returned.
- **Resistance to brute force** — Argon2id with 64 MiB memory requirement makes offline dictionary attacks expensive, even on GPU hardware.
- **Keystream uniqueness** — A fresh `crypto/rand` 16-byte salt is generated on every encode and stored in plaintext in the image header. Argon2id derives the cipher nonce from the password and this salt, so each encode produces a unique payload keystream even when the same password and carrier are reused.
- **Pixel order** — The salt has to be found before any key exists, so the salt pixel positions are public by design: anyone can compute them from the image size and settings. Everything after them is placed by a Fisher-Yates shuffle keyed with Argon2id output, so recomputing the order for a guessed password costs a full KDF call (previously the order came from SHA-256 of the password, which made guesses cheap). With bounded padding the encoder writes only the stream prefix up to the end of the tag, so the pixels it touches are a pseudorandom subset spread over the whole image, and which subset depends on Argon2id output. Without the password an attacker cannot single out the pixels that carry data, only estimate how many there are. When the payload fills the carrier every pixel carries data and the order hides nothing about *where* it is.
- **Payload size** — padding to a power-of-two bucket hides the size only to within a factor of two: a 31-byte and a 4,000-byte payload look the same, a 5,000-byte one does not. The bucket length is encrypted, so reading it needs the password, but the number of changed pixels grows with the bucket, so a steganalyst who can estimate the embedding rate learns roughly which bucket was used. The previous full-carrier padding hid the size completely, at the cost of maximal disturbance.

### What steg does not protect against

- An attacker who has the original carrier can compare it pixel by pixel with the steg image and see exactly which pixels changed, and from their number the approximate payload bucket.
- Steganalysis beyond chi-square and RS. At 1 bit per channel LSB matching defeats both tests in `steg detect`, but a trained detector does not need them: on never-compressed grayscale images the SPAM + FLD ensemble in `steg/analysis` tells LSB-matching stegos at 0.1 bits per pixel from covers with an average error of 16% (P_E ≈ 0.16; 50% would be guessing), and stronger modern detectors exist (see [Trained detector: `stegbench`](#trained-detector-stegbench)). At 2–8 bits per channel the encoder uses replacement, which the built-in tests catch at high embedding rates.

---

## On-image layout

The hidden data is one bit stream. Stream bit `b` lives in pixel `order[b / bitsPerPixel]`; within a pixel the bits run through the active channels in the order R, G, B, and within each channel through its N low bits from the most significant of them down. The stream layout is:

```
Bit offset           Size        Cipher                  Field
────────────────────────────────────────────────────────────────────────────────
0                    128 bits    none (plaintext)        Per-encode random salt
128                  32 bits     AES-128-CTR (payload)   Container length (uint32, LE)
160                  32 bits     AES-128-CTR (payload)   Real payload length (uint32, LE)
192                  N×8 bits    AES-128-CTR (payload)   Real payload bytes
192 + N×8            P×8 bits    AES-128-CTR (payload)   Random padding (to the bucket size)
192 + (N+P)×8        256 bits    AES-128-CTR (payload)   HMAC-SHA256 tag
                                                         (stream ends here; later pixels untouched)
```

The padded block (real length + payload + padding, `4 + N + P` bytes) is `max(4096, nextPowerOfTwo(4 + N))` bytes, or `capacity + 4` bytes when that bucket does not fit the carrier, in which case the stream fills the carrier exactly. A single AES-128-CTR payload cipher (`AES-CTR(encKey, payloadNonce)`) encrypts everything from bit 128 onward. The 16-byte `randomSalt` at bits 0–127 is stored in plaintext — an attacker who sees it still cannot derive any keys without the password. Argon2id takes the password and `randomSalt` and produces all key material (encKey, macKey, payloadNonce, orderKey) in a single call, so no bootstrap cipher is needed.

The decoder accepts any container length up to `capacity + 4`, so images written by the previous full-carrier encoder decode with this build and vice versa; bounded padding and LSB matching did not change the format.

The pixel order (`cursors/order.go`) is built in two parts of one Fisher-Yates shuffle over all pixels:

1. **Public prefix.** The first k = ⌈128 / bitsPerPixel⌉ steps are driven by ChaCha8 under a fixed public key. These k pixels hold the salt (the last of them may also hold the first encrypted bits).
2. **Secret suffix.** After the salt is written (encode) or read (decode) and Argon2id has run, the remaining steps are driven by ChaCha8 under `orderKey`.

Both the sequential and the parallel paths get the order from `prepareEncode` / `prepareDecode` in `steg/steg.go`. The exact algorithm, including the bounded random draw, is in [docs/TECHNICAL_SPEC.md](docs/TECHNICAL_SPEC.md).

---

## Architecture

```
┌─────────────────────────────────────────────────┐
│  cmd/steg  (Cobra CLI, PNG/BMP/TIFF I/O,        │
│  transparency check, verify-after-write)        │
└──────────────────┬──────────────────────────────┘
                   │ draw.Image + password
┌──────────────────▼──────────────────────────────┐
│  steg.Encode / Decode / EncodeParallel /        │
│  DecodeParallel  (orchestration)                │
│  prepareEncode/prepareDecode: public order →    │
│  salt → Argon2id → keys → finished order        │
└──────┬────────────────────────────┬─────────────┘
       │ pixel order                │ encKey, nonce
┌──────▼────────────┐   ┌───────────▼─────────────┐
│  order.go         │   │  cipher.NewCipher       │
│  ChaCha8 shuffle  │   │  AES-128-CTR, seekable  │
│  RNGCursor        │   │  bit/byte-level XOR     │
│  direct Pix I/O   │   │                         │
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
| `cmd/steg` | Cobra CLI; PNG/BMP/TIFF file I/O; transparency check; verify-after-write; `encode`, `decode`, `capacity`, `test-visual`, and `detect` subcommands |
| `steg` | Encode/decode orchestration; carrier and settings validation; salt handling and Argon2id key derivation (`prepareEncode` / `prepareDecode`); capacity; parallel worker pool |
| `steg/container` | Payload framing (length prefix + HMAC tag); constant-time tag verification |
| `cursors` | `order.go` (pixel order: public ChaCha8 prefix for the salt, keyed suffix — format-defining), `RNGCursor` (bit↔pixel mapping over a given order; reads and writes the `Pix` bytes of `*image.RGBA` / `*image.NRGBA` directly), `CursorAdapter` (byte↔bit bridge), `CipherMiddleware` (transparent encrypt/decrypt) |
| `cipher` | AES-128 CTR stream cipher; bit- and byte-addressable keystream; seekable |
| `steg/analysis` | Chi-square and RS steganalysis detectors (`Analyze()` returns the combined verdict `steg detect` prints); second-order SPAM features (`SPAM`) and a trained Fisher-linear-discriminant ensemble (`TrainEnsemble`, `Ensemble.Predict` / `Votes`), with a plain-Go Cholesky solver |
| `cmd/stegbench` | Steganalysis benchmark: embeds into a corpus of covers (simulated replacement and matching, or the real encoder), trains and tests the SPAM ensemble, and reports P_E with chi-square and RS flag rates |
| `mocks` | Auto-generated gomock mocks for `Cursor` and `StreamCipherBlock` interfaces |
| `testutil` | `MemReadWriteSeeker` in-memory helper for tests |

---

## Development

### Prerequisites

- Go 1.25+
- `make`

### Commands

```bash
# Build the CLI binary
make build

# Install the binary to $GOPATH/bin (or $GOBIN)
make install

# Run all tests
make test

# Vet hand-written packages (generated mocks are excluded)
make vet

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
├── cmd/
│   ├── steg/        # CLI entry point (Cobra)
│   ├── attack1/     # Steganalysis report over one or more images (built on steg/analysis)
│   ├── attack4/     # Dictionary attack using the real decode path as password oracle
│   └── stegbench/   # Steganalysis benchmark: SPAM + FLD ensemble, chi-square, RS over a cover corpus
├── cursors/         # Pixel order, RNGCursor, CursorAdapter, CipherMiddleware
├── docs/            # Technical spec, ADRs, release notes
├── mocks/           # Auto-generated gomock mocks
├── steg/            # Encode/decode orchestration, container framing
│   ├── analysis/    # Chi-square, RS, SPAM features + FLD ensemble (spam.go, ensemble.go, linalg.go)
│   └── container/
└── testutil/        # Shared test helpers
```

### Continuous integration

Two GitHub Actions workflows:

**`test.yml`** runs on every pull request and on pushes to non-`master` branches:

1. `make vet`
2. `go test ./...`
3. `go test -race ./...`
4. `go build ./...`

**`release.yml`** runs on every push to `master`:

1. Runs `go test ./...`
2. Cross-compiles binaries for Linux, macOS, and Windows (amd64 + arm64)
3. Publishes a GitHub Release tagged `v<YYYYMMDD>-<short-sha>` with all binaries attached

---

## Known limitations

| Issue | Severity | Notes |
|---|---|---|
| MAC-then-Encrypt ordering | Low | HMAC is computed over plaintext before encryption. Unconventional (Encrypt-then-MAC is preferred), but not exploitable in this threat model since the tag is inside the encrypted channel. |
| No streaming decode | Medium | `ReadPayload` allocates the full payload in memory before returning. Very large payloads may cause high memory usage. The declared length is clamped to the carrier's capacity first, so a wrong password cannot trigger an oversized allocation. |
| Lossy formats unsupported | High | JPEG and other lossy formats destroy LSB data. Only lossless formats (PNG, BMP, TIFF) are supported. |
| Transparent carriers rejected | Low | `encode` and `test-visual` refuse a carrier with any non-opaque pixel, for every output format: PNG and BMP store straight alpha, so saving would destroy bits hidden in translucent pixels. Flatten the image onto an opaque background first. |
| Library carriers: `*image.RGBA` / `*image.NRGBA` only | Low | `steg.Encode`, `Decode`, `EncodeParallel` and `DecodeParallel` reject every other `draw.Image` type (error wraps `cursors.ErrUnsupportedImage`) before touching a pixel, because color conversion (premultiplication, luma, 16-bit truncation) would destroy the hidden bits. Convert first with `draw.Draw` into an `*image.RGBA`. An `*image.NRGBA` with translucent pixels round-trips in memory; the CLI's transparency rule is about saving to files. The CLI converts every input itself. |
| Size limits | Low | At most 2³²−1 pixels per carrier (the pixel order uses 4-byte indices), and at most 4 GiB of capacity (the container length field is 32 bits). Invalid settings (bits per channel outside 1–8, channels outside 1–3) are rejected by the library as well as the CLI. |
| Images from earlier builds | Low | The pixel order changed and there is no format version field; an older image fails to decode exactly as with a wrong password. Decode it with the binary that created it. |
| Statistical steganalysis | Medium | At 1 bit per channel the encoder uses LSB matching, which chi-square and RS analysis (the tests in `steg detect`) do not see: a 1024×1536 photo reads `CLEAN` even when filled to capacity. A trained detector does see it. On BOSSbase (never-compressed grayscale) the SPAM + FLD ensemble reaches P_E ≈ 0.22 at 0.05 bits per pixel and ≈ 0.16 at 0.1, the same as for replacement; the 4 KiB padding floor alone is 0.127 bits per pixel in a 512×512 single-channel carrier, where the real encoder measures P_E ≈ 0.22 (see [`stegbench`](#trained-detector-stegbench)). Stronger detectors than SPAM exist, so these are upper bounds on how hard the output is to detect. Lowering the floor and adaptive embedding ([Next steps](#next-steps)) are the answers. With 2–8 bits per channel the encoder uses replacement, and a large payload is flagged by the built-in tests. |
| Payload size only bucketed | Low | The padded block is a power-of-two bucket of at least 4 KiB, so the payload size is hidden only to within a factor of two. The bucket is encrypted, but the number of changed pixels grows with it, so an embedding-rate estimate reveals it roughly. A payload that fills the carrier hides its size completely. |
| Output not reproducible | Info | The salt is fresh on every encode, and the ±1 directions of LSB matching come from `crypto/rand`, so encoding the same input twice gives different images. Both are independent of the password. |

---

## Steganalysis

`steg/analysis` has three detectors. Two are fixed statistical tests that need no training, chi-square and RS analysis; they are what the `detect` command runs, and they are built for LSB replacement. The third is a trained classifier, SPAM features with a Fisher-linear-discriminant ensemble, which also detects LSB matching; it needs a set of cover and stego images to train on, so `steg detect` does not run it. `cmd/stegbench` trains and evaluates it (see [Trained detector: `stegbench`](#trained-detector-stegbench)).

### Chi-square test

Compares the histogram of pixel value pairs `(2k, 2k+1)` per channel. In a natural image these pairs are unequal; LSB replacement equalises them (LSB matching does not; see below). Only pairs that occur in the image count: the degrees of freedom are the number of occupied pairs minus one (shown as `df`), and the p-value is the exact chi-square upper tail. A high p-value (> 0.05) for a channel is flagged as suspicious. A channel with fewer than two occupied pairs is reported as `too few distinct values to test` and not flagged.

**Performance against LSB replacement:** correctly identifies which channels were written to; untouched channels stay at p ≈ 0.0000. On a channel fully overwritten by replacement the p-value is roughly uniform between 0 and 1, so each written channel falls below 0.05 about 5% of the time by chance. A full-capacity R+G+B replacement encode therefore reads `LIKELY_STEGO` most of the time (about 86% in our estimate) and `SUSPICIOUS` otherwise; RS analysis does not have this variance.

**Against this tool's output:** at 1 bit per channel the encoder uses LSB matching, whose changes cross pair boundaries instead of equalising each pair, so the pairs stay as uneven as in the original and p stays at 0.0000 even with the carrier full. At 2–8 bits per channel the encoder uses replacement and the test works as above.

### RS analysis

Measures local pixel smoothness using regular (`R`) and singular (`S`) group fractions under a positive (`Rm`, `Sm`) and a negative (`R-m`, `S-m`) flipping mask. In a clean photograph `Rm ≈ R-m`. LSB replacement pulls `Rm` down toward `Sm` while pushing `R-m` up, so the reported asymmetry `Rm − R-m` turns negative (Fridrich, Goljan, Du). A channel with asymmetry below `−0.02` is flagged as suspicious.

**Performance against LSB replacement:** on a 1024×1536 photograph the clean channels measure −0.002 to −0.006, and the same photo after a full-capacity replacement encode (the previous encoder) measures −0.040 to −0.048. All three stego channels are flagged and none of the clean ones are.

**Against this tool's output:** RS models the flipping pattern of replacement, and LSB matching does not produce it. At 1 bit per channel the asymmetry stays near the clean value for small and medium payloads (−0.0047 / −0.0014 / −0.0057 for a 31-byte payload on the photo above) and moves toward zero, not below −0.02, when the carrier is full (−0.0014 / +0.0014 / −0.0015). That shift is real but far from the threshold; a detector calibrated on the clean image might notice it, `steg detect` does not. At 2–8 bits per channel the encoder uses replacement and large payloads are flagged.

Both tests remain useful as detectors of LSB replacement, which is what most simple steganography tools do. A `CLEAN` verdict on this tool's 1-bit output says only that these two tests found nothing; the trained detector below does see LSB matching.

The RS statistic is noisier on small images (below roughly 512×512), where a clean channel can cross the threshold by chance. Images whose LSB plane is already artificial (for example, synthetic images with all-even values) can have a large clean asymmetry and are not a meaningful baseline.

### Verdict thresholds

| Suspicious count | Verdict |
|---|---|
| 0 | `CLEAN` |
| 1 – (n−1) | `SUSPICIOUS` |
| all n | `LIKELY_STEGO` |

`n` = number of test×channel combinations (6 for a 3-channel image).

### Trained detector: `stegbench`

`cmd/stegbench` measures how detectable an embedding method is at a given embedding rate (bits per pixel, bpp) against a detector that has been trained for it, the setting modern steganalysis assumes.

**Detector.** Second-order SPAM features (Pevný, Bas, Fridrich, IEEE TIFS 2010): truncated differences between neighbouring pixels (T = 3) in 8 directions, modelled as a second-order Markov chain, 686 features per image plane. They feed a Fisher-linear-discriminant ensemble (Kodovský, Fridrich, Holub, IEEE TIFS 2012): 51 base learners, each trained on a random 300-feature subspace and a bootstrap sample, majority vote. The paper's out-of-bag search for the number of learners and the subspace size is not implemented; both are fixed (`-learners`, `-subspace` override them). Everything is plain Go, no new dependency. The sign convention of the differences follows the paper's equation; the reference MATLAB code may pair them the other way round, which amounts to analysing the intensity-inverted image: same detection power, but not numerically identical to published features.

**Method.** For each cover image, `stegbench` makes one stego image per (method, rate):

- `replacement` — simulated LSB replacement at `round(rate × N)` uniformly random pixels with random message bits;
- `matching` — the same with LSB matching (±1 at random when the bit must change), which is what `steg encode` does at 1 bit per channel;
- `tool` — the real `steg.Encode` (1 channel × 1 bit; the plane goes into R = G = B of an RGBA image), with the payload sized to the bounded-padding bucket whose rate is closest to the requested one.

It extracts SPAM features from every cover and stego, then over several random splits trains the ensemble on half the images and tests it on the other half. A cover and its stegos always land in the same half. It reports P_E = (P_FA + P_MD) / 2 on the test half, mean ± standard deviation over the splits (P_FA: covers called stego, P_MD: stegos called cover; 0.5 is guessing), plus the fraction of covers and of stegos that chi-square and RS flag. All randomness is derived from `-seed`, so runs are reproducible, except `tool` rows (the encoder's salt and ±1 directions come from `crypto/rand`). `tool` runs Argon2id for every image, about 64 MiB per worker.

**Running it.** Covers are `.pgm` (binary P5, 8-bit) or `.png` files directly in `-dir` (grayscale, or one colour channel chosen with `-channel`). The standard corpus is BOSSbase 1.01, 10,000 never-compressed 512×512 grayscale images:

```bash
mkdir -p ~/datasets/bossbase/extracted
(cd ~/datasets/bossbase &&
  curl -O https://dde.binghamton.edu/download/ImageDB/BOSSbase_1.01.zip &&
  unzip -q BOSSbase_1.01.zip -d extracted)

# from the repository root
go run ./cmd/stegbench -dir ~/datasets/bossbase/extracted/BOSSbase_1.01
go run ./cmd/stegbench -dir ~/datasets/bossbase/extracted/BOSSbase_1.01 -methods matching -rates 0.005,0.01,0.02
go run ./cmd/stegbench -dir ~/datasets/bossbase/extracted/BOSSbase_1.01 -n 500 -methods matching,tool -rates 0.127,0.25,0.5,1
go run ./cmd/stegbench -h   # all flags
```

The default run (replacement and matching at 0.05, 0.1, 0.2, 0.4 and 1 bpp, 5 splits of 5,000 / 5,000 images) takes 81 s and peaks at 1.8 GB of memory on a 32-thread Ryzen 9 9950X3D.

**Results** on BOSSbase 1.01, all 10,000 images, 5 splits. "Changed" is the fraction of pixels whose value changed; chi-square and RS give the fraction of covers / stegos flagged.

| Method | Rate (bpp) | Changed | P_E (mean ± std) | P_FA | P_MD | chi-square cover / stego | RS cover / stego |
|---|---|---|---|---|---|---|---|
| replacement | 0.05 | 0.025 | 0.2259 ± 0.0033 | 0.2564 | 0.1954 | 0.000 / 0.000 | 0.019 / 0.284 |
| replacement | 0.1 | 0.05 | 0.1581 ± 0.0043 | 0.1722 | 0.1441 | 0.000 / 0.000 | 0.019 / 0.755 |
| replacement | 0.2 | 0.1 | 0.0813 ± 0.0028 | 0.0922 | 0.0704 | 0.000 / 0.001 | 0.019 / 0.945 |
| replacement | 0.4 | 0.2 | 0.0216 ± 0.0005 | 0.0210 | 0.0223 | 0.000 / 0.007 | 0.019 / 0.991 |
| replacement | 1 | 0.5 | 0.0039 ± 0.0002 | 0.0055 | 0.0022 | 0.000 / 0.945 | 0.019 / 1.000 |
| matching | 0.005 | 0.0025 | 0.4342 ± 0.0014 | 0.4551 | 0.4132 | 0.000 / 0.000 | 0.019 / 0.020 |
| matching | 0.01 | 0.005 | 0.3831 ± 0.0048 | 0.4089 | 0.3573 | 0.000 / 0.000 | 0.019 / 0.020 |
| matching | 0.02 | 0.01 | 0.3201 ± 0.0053 | 0.3500 | 0.2902 | 0.000 / 0.000 | 0.019 / 0.021 |
| matching | 0.05 | 0.025 | 0.2246 ± 0.0033 | 0.2468 | 0.2023 | 0.000 / 0.000 | 0.019 / 0.027 |
| matching | 0.1 | 0.05 | 0.1601 ± 0.0063 | 0.1806 | 0.1396 | 0.000 / 0.000 | 0.019 / 0.040 |
| matching | 0.2 | 0.1 | 0.1025 ± 0.0014 | 0.1181 | 0.0870 | 0.000 / 0.000 | 0.019 / 0.052 |
| matching | 0.4 | 0.2 | 0.0587 ± 0.0025 | 0.0701 | 0.0473 | 0.000 / 0.000 | 0.019 / 0.068 |
| matching | 1 | 0.5 | 0.0162 ± 0.0010 | 0.0207 | 0.0117 | 0.000 / 0.000 | 0.019 / 0.075 |

The real encoder matches the simulation. On 500 images, at the rates bounded padding can reach in a 512×512 single-channel carrier (0.1266 bpp is the 4 KiB floor; then 0.2516, 0.5016 and 1.0), P_E for `tool` vs `matching` was 0.223 vs 0.208 at 0.127 bpp, 0.144 vs 0.136 at 0.25, 0.085 vs 0.079 at 0.5 and 0.031 vs 0.031 at 1.0, equal within split noise.

**What this shows:**

1. **Chi-square and RS are blind to LSB matching.** Chi-square flags no matching stego at any rate; RS flags 2–7.5% of them, against 1.9% of covers. RS does catch replacement from 0.1 bpp (76% of stegos, 95% at 0.2).
2. **Against the trained detector, matching is no better than replacement at low rates.** At 0.05 and 0.1 bpp P_E is about 0.22 and 0.16 for both. Matching helps only from 0.2 bpp up (0.10 vs 0.08 at 0.2, 0.016 vs 0.004 at 1). Its benefit is against the simple tests, not against a modern detector.
3. **The embedding rate dominates.** P_E for matching goes 0.43 at 0.005 bpp, 0.38 at 0.01, 0.32 at 0.02, 0.22 at 0.05.
4. **The bounded-padding floor matters.** A 4 KiB minimum block is 0.127 bpp in a 512×512 single-channel carrier, where P_E is about 0.15–0.2: caught most of the time. In a 1024×1536 colour image with 3 channels the same floor is about 0.007 bits per channel value, a rate at which a single grayscale plane gives P_E around 0.4. That comparison is only indicative: a detector that uses the correlation between colour channels, a different cover source and any JPEG history in the carrier all change the picture.

**Caveats.** BOSSbase is never-compressed, the easiest setting for the detector. The detector is trained on the same cover source and the same rate it is tested on, an idealised attacker. SPAM dates from 2010; stronger detectors (rich models such as SRM, convolutional networks) exist. The P_E values above are therefore an upper bound on how hard the output is to detect, not a guarantee.

---

## Next steps

The weakest point of the design is not the cryptography but the detectability
of the embedding itself. Until recently `steg detect` flagged every image the
tool produced, without needing the password. LSB matching and bounded padding
(below, done) put the tool's 1-bit output out of reach of chi-square and RS
analysis. `stegbench` (also done) shows that a trained detector still sees it,
and that the embedding rate is what decides how well; the remaining items
lower that rate or make each changed bit harder to see.

### Done: LSB matching instead of LSB replacement

Every colour value is a number between 0 and 255, and its lowest bit is whether
it is even or odd. Replacement forced that bit directly, so a pixel never left
its pair (100↔101, 102↔103, …): encrypted data, half zeros and half ones,
evened out the counts inside every pair, which is exactly what chi-square
measures. At 1 bit per channel the encoder now adds or subtracts 1 at random
when the bit must change (0 can only go up, 255 only down), so a value at 100
that must carry a 1 becomes 101 or 99. The decoder reads the same bit and was
not changed. The ±1 directions come from a per-cursor ChaCha8 generator seeded
from `crypto/rand`, independent of the password and keys. With 2–8 bits per
channel the encoder still uses replacement.

**Measured effect:** on `dude.png` filled to capacity, chi-square p went from
0.62 / 0.13 / 0.95 to 0.0000 on every channel, and RS asymmetry from about
−0.043 to between −0.0015 and +0.0014: `LIKELY_STEGO` became `CLEAN`.

### Done: Bounded padding

The padded block is now `max(4096, nextPowerOfTwo(4 + payload))` bytes, or
the whole carrier when that bucket does not fit, and pixels past the end of
the stream keep their original values. Because the pixel order after the salt
is keyed by Argon2id output, the touched pixels are a keyed pseudorandom
subset spread over the image.

**Measured effect:** a 31-byte text in `dude.png` changes 9,724 pixels (0.62%)
instead of 1,375,953 (87.5%). The cost is that the payload size is hidden only
to within a power of two (see [Security design](#security-design)).

### Done: Steganalysis benchmark

`cmd/stegbench` trains a SPAM + FLD-ensemble detector and measures P_E for
simulated replacement, simulated matching and the real encoder across
embedding rates (see [Trained detector: `stegbench`](#trained-detector-stegbench)).

**Measured effect:** on BOSSbase, matching is no harder to detect than
replacement at 0.05 and 0.1 bpp (P_E ≈ 0.22 and 0.16 for both), and the 4 KiB
padding floor in a 512×512 single-channel carrier is 0.127 bpp, where the
real encoder is caught most of the time. Both items below are to be judged
by `stegbench`.

### 1. Lower the padding floor

Small payloads are the common case, and for them the 4 KiB minimum block, not
the payload, sets the embedding rate. A 31-byte message is written as a
4,148-byte stream. Lowering the floor (for example to a few hundred bytes)
cuts the rate for small payloads by an order of magnitude, which `stegbench`
shows is the largest single lever on P_E (0.22 at 0.05 bpp, 0.38 at 0.01).
The cost is that small payload sizes become more distinguishable from each
other, since fewer of them share a bucket. It changes only the encoder's
`paddedSize`; the decoder already accepts any container length, so the format
stays compatible. This comes first because it is cheap and helps the case
that matters most.

### 2. Adaptive embedding

LSB matching defeats chi-square and RS, but not a trained detector: at the
same rate it is as detectable as replacement up to 0.1 bpp. Adaptive
embedding attacks the other factor, how visible each change is. Syndrome-trellis
codes (STC) carry the same message with fewer changes per bit, and a cost
function places those changes in noisy, textured regions instead of smooth
ones such as sky or walls. The decoder must reconstruct the same selection
without knowing the payload, so the costs have to be computed from bits that
are never modified (typically the upper seven bits of each channel), and the
on-image format changes. It is the way to improve P_E at medium rates, where
a lower floor does not help, but it is a much larger change than item 1.

### 3. Close the loop with `detect`

The benchmark part of this item is done (`stegbench`). What remains is having
`encode` run the steganalysis on its own output and warn when the result is
detectable. Chi-square and RS no longer flag 1-bit output, so with them the
warning is mainly useful at 2–8 bits per channel; the SPAM ensemble needs a
trained model, which the CLI does not ship. `TestEncoderOutputEvadesDetectors`
and `TestEncoderOutputReplacementAboveOneBit` in `steg/analysis` guard the
chi-square and RS behaviour.

### 4. Pipe mode

Read the payload from stdin and write to stdout so `steg` composes with
`tar`, `gpg`, or `age` in a pipeline.

---

## Roadmap

- **Lossless WebP support** — extend format support beyond PNG, BMP, and TIFF.
- **Streaming decode** — avoid loading the full payload into memory for large files.
- **16-bit image depth** — exploit the extra bits-per-channel available in 16-bit PNG/TIFF carriers.
