# Steg — Technical Specification

**Format:** v2 (keyed pixel order)
**Date:** 2026-11-11

This document specifies the on-image format precisely enough to write a
compatible decoder without reading the Go source. Every statement here was
checked against the code; where they disagree, the code is authoritative and
this document is wrong.

> **Format break.** v2 changed how the pixel traversal order is derived. Images
> written by earlier builds (SHA-256 seed + `math/rand` shuffle, or the older
> bootstrap-cipher layouts) cannot be decoded by a v2 build, and v2 images cannot
> be decoded by earlier builds. There is no version field on the image: a
> mismatched decoder simply fails with a length or MAC error, exactly as for a
> wrong password.

---

## Table of Contents

1. [Overview](#1-overview)
2. [Parameters and notation](#2-parameters-and-notation)
3. [Bit-to-pixel mapping](#3-bit-to-pixel-mapping)
4. [Pixel order](#4-pixel-order)
5. [Key derivation](#5-key-derivation)
6. [Stream cipher](#6-stream-cipher)
7. [Stream layout and container framing](#7-stream-layout-and-container-framing)
8. [Padding](#8-padding)
9. [Authentication](#9-authentication)
10. [Capacity](#10-capacity)
11. [Encode and decode procedures](#11-encode-and-decode-procedures)
12. [Parallel encode/decode](#12-parallel-encodedecode)
13. [Implementation layers](#13-implementation-layers)
14. [CLI behaviour](#14-cli-behaviour)
15. [Design rationale](#15-design-rationale)
16. [Known limitations](#16-known-limitations)

---

## 1. Overview

Steg hides an arbitrary byte string in a lossless RGB image by overwriting the
`N` least-significant bits of up to three colour channels of every pixel, in a
pixel order that is partly public and partly derived from the password. The
embedded data is a single **bit stream**:

```
[16-byte plaintext salt][AES-128-CTR encrypted: length | padded payload | HMAC-SHA256 tag]
```

The encoder always writes the stream to the full capacity of the carrier: a
short payload is followed by random padding, so every encode touches every
usable bit of the image.

---

## 2. Parameters and notation

| Symbol | Meaning |
|---|---|
| `W`, `H` | Image width and height in pixels (`Bounds().Dx()`, `Bounds().Dy()`) |
| `n` | `W × H`, number of pixels |
| `bpc` | Bits per channel, 1–8 (CLI `--bits-per-channel`, default 1) |
| `ch` | Active channels, 1–3: 1 = R, 2 = R+G, 3 = R+G+B (CLI `--channels`, default 3) |
| `bpp` | Bits per pixel, `ch × bpc` |
| `k` | Salt pixels, `ceil(128 / bpp)` = `(128 + bpp − 1) / bpp` |
| `p` | Stream bit position, starting at 0 |
| `LE32` | 4-byte little-endian unsigned 32-bit integer |

`bpc` and `ch` are not stored on the image; the decoder must be given the values
used at encode time.

Pixel values are the 8-bit R, G and B components of an opaque pixel. Pixel
coordinates in this document are relative to the image's origin
(`Bounds().Min`); pixel index `i` in row-major order is the point
`(x, y) = (i mod W, i div W)`.

---

## 3. Bit-to-pixel mapping

**File:** `cursors/rng_cursor.go`

Given the pixel order `order[0..n−1]` (section 4), stream bit `p` lives at:

```
pixelIndex   = p div bpp                        → pixel order[pixelIndex]
slot         = p mod bpp
channel      = activeChannels[slot div bpc]     (activeChannels = [R], [R,G] or [R,G,B])
bitInChannel = (bpc − 1) − (slot mod bpc)       (0 = least-significant bit)
```

So inside one pixel the slots run through the channels in the order R, G, B
(only the active ones), and within each channel through its `bpc` low bits from
the most significant of them down to bit 0. Example with `ch = 3, bpc = 2`:
slots 0..5 of a pixel are R bit 1, R bit 0, G bit 1, G bit 0, B bit 1, B bit 0.

Bytes are packed into the stream **MSB first**: stream byte `i` occupies stream
bits `8i .. 8i+7`, and bit `8i + j` holds bit `7 − j` of the byte.

Writing a bit sets or clears only that bit of the channel value. Inactive
channels, the bits above the low `bpc`, and alpha are left as they were.

Only the first `8 × floor(n × bpp / 8)` stream bits are used; when `n × bpp` is
not a multiple of 8 the last few slots of the stream are never written.

---

## 4. Pixel order

**File:** `cursors/order.go` (`NewPixelOrder`, `FinishPixelOrder`), orchestrated
by `prepareOrder` in `steg/steg.go`.

The order is a permutation of all `n` pixel indices produced by **one**
Fisher-Yates shuffle whose steps are split between two pseudo-random streams:

* steps `0 .. k−1` (the **public prefix**) are driven by ChaCha8 under a fixed,
  published key, so `order[0..k−1]` depend only on `W`, `H` and `k`; the 128
  salt bits live in these pixels;
* steps `k .. n−2` (the **secret suffix**) are driven by ChaCha8 under
  `orderKey`, which comes from Argon2id over the password and the salt
  (section 5).

### 4.1 Random source

ChaCha8 is Go's `math/rand/v2.ChaCha8`, the generator specified by
[C2SP chacha8rand](https://c2sp.org/chacha8rand). Only its raw `Uint64()` output
is used. Each of the two streams is a fresh generator seeded with its 32-byte
key; the secret stream does not continue the public one.

```
publicOrderKey = SHA-256("github.com/pableeee/steg/v2 salt positions")   (ASCII, no terminator)
orderKey       = Argon2id output bytes [52:84]                           (section 5)
```

### 4.2 Bounded draw

`uniform(src, m)` returns an unbiased integer in `[0, m)` for `m > 0` by
rejection sampling over raw 64-bit outputs:

```
threshold = (2^64 − m) mod m          // in Go: -m % m on uint64
loop:
    x = src.Uint64()
    if x ≥ threshold: return x mod m
```

Draws below `threshold` are discarded and consume stream output. This is
deliberately not `rand.Rand.IntN` or `rand.Shuffle`, whose algorithms Go does
not promise to keep stable across releases.

### 4.3 Shuffle

```
shuffleRange(order, from, to, key):
    to  = min(to, n − 1)            // the last step (i = n−1) is the identity: skipped,
    src = ChaCha8(key)              // it consumes no stream output
    for i = from .. to−1:
        j = i + uniform(src, n − i)
        swap(order[i], order[j])

order = [0, 1, ..., n−1]            // row-major pixel indices
shuffleRange(order, 0, k, publicOrderKey)     // public prefix
-- read or write the 16-byte salt through order[0..k−1] (section 7) --
shuffleRange(order, k, n, orderKey)           // secret suffix
```

The second call does not change `order[0..k−1]`. The image must have at least
`k` pixels or encode/decode fails ("image too small").

The last salt pixel `order[k−1]` may also carry the first stream bits after the
salt (whenever `bpp` does not divide 128). That is consistent: its position is
fixed by the public prefix, so it is known before `orderKey` exists.

---

## 5. Key derivation

**File:** `steg/steg.go` (`deriveMainKeys`)

```
salt    = 16 bytes read from stream bits 0–127 (written from crypto/rand at encode time)
derived = Argon2id(password, salt, time = 2, memory = 64 MiB (65536 KiB),
                   threads = 4, keyLen = 84)            // golang.org/x/crypto/argon2.IDKey, v1.3

encKey   = derived[0:16]                      // AES-128 key
macKey   = derived[16:48]                     // HMAC-SHA256 key
nonce    = big-endian uint32(derived[48:52])  // CTR nonce
orderKey = derived[52:84]                     // ChaCha8 key for the secret order suffix
```

The password is the raw byte string (the CLI passes it unmodified, no
normalisation). An empty password is rejected.

---

## 6. Stream cipher

**File:** `cipher/cipher.go`

A custom AES-128 counter mode, addressed by **absolute stream bit position**.
The keystream bit used for stream bit `p` is:

```
counterBlock(c) = LE32(nonce) ‖ 00 00 00 00 ‖ LE32(c) ‖ 00 00 00 00      // 16 bytes
KS(c)           = AES-128-Encrypt(encKey, counterBlock(c))

keystreamBit(p) = (KS(p div 128)[(p div 8) mod 16] >> (p mod 8)) & 1
```

That is, keystream bytes are consumed **LSB first**, while data bytes are
packed MSB first (section 3). For stream bit `p`:
`storedBit(p) = dataBit(p) XOR keystreamBit(p)`.

Encryption applies to stream bits 128 onward. Keystream positions 0–127
(counter block 0) are never used, because the salt is stored in plaintext;
the first encrypted bit, `p = 128`, uses bit 0 of byte 0 of `KS(1)`.

Equivalent byte-level formulation: for stream byte `i ≥ 16`,

```
stored[i] = data[i] XOR bitReverse8( KS(i div 16)[i mod 16] )
```

---

## 7. Stream layout and container framing

**Files:** `steg/encode.go`, `steg/decode.go`, `steg/container/container.go`

Byte offsets are stream byte offsets (stream bit = 8 × byte).

| Stream bytes | Size | Cipher | Field |
|---|---|---|---|
| 0–15 | 16 B | none | `salt` |
| 16–19 | 4 B | AES-CTR | `L` = container length, LE32. The encoder always writes `L = cap + 4` |
| 20–23 | 4 B | AES-CTR | real payload length `R`, LE32 (first 4 bytes of the padded block) |
| 24 … 24+R−1 | R B | AES-CTR | real payload |
| 24+R … 20+L−1 | L−4−R B | AES-CTR | random padding |
| 20+L … 20+L+31 | 32 B | AES-CTR | HMAC-SHA256 tag |

Bytes `20 .. 20+L−1` are the **padded block**. With `L = cap + 4` the stream
ends at byte `56 + cap = floor(n × bpp / 8)`, i.e. exactly at capacity.

Container write (`container.WritePayload`, called with the stream positioned at
byte 16): skip 4 bytes, write the padded block while feeding it to the HMAC,
write the 32-byte tag, seek back and write `LE32(L)`.

Container read (`container.ReadPayload`, `maxPayload = cap + 4`):

1. Read `L`. If `L > cap + 4`, fail ("payload length … exceeds maximum …: wrong
   password or corrupt image") without allocating or reading further. With a
   wrong password `L` is effectively random, so this rejects almost every wrong
   candidate cheaply.
2. Read `L` bytes (the padded block) and the 32-byte tag.
3. Compare `HMAC-SHA256(macKey, paddedBlock)` with the tag in constant time
   (`hmac.Equal`). Mismatch → "checksum validation failed".
4. `R = LE32(paddedBlock[0:4])`. If `R > L − 4`, fail ("corrupt payload").
   Return `paddedBlock[4 : 4+R]`.

A decoder accepts any `L ≤ cap + 4`; it does not require `L = cap + 4`.

---

## 8. Padding

**File:** `steg/steg.go` (`buildPaddedPayload`, `extractRealPayload`)

```
paddedBlock = LE32(R) ‖ payload ‖ crypto/rand bytes      // length exactly cap + 4
```

If `R > cap` encode fails ("payload too large"). If `cap ≤ 0` encode fails
("image too small to hold any payload"). The padding is encrypted like
everything else, so on the image it is indistinguishable from payload.

---

## 9. Authentication

The tag is `HMAC-SHA256(macKey, paddedBlock)`: it covers the real-length
prefix, the payload and the padding. It does **not** directly cover the salt
or the container length field `L`, but both are bound indirectly:

* a changed salt changes every derived key and the secret order, so the tag no
  longer verifies;
* a changed `L` makes the decoder read a different range as the padded block
  and a different 32 bytes as the tag, which fails verification except with
  negligible probability.

The tag is computed over plaintext and then encrypted (MAC-then-encrypt); see
section 15.

---

## 10. Capacity

**File:** `steg/steg.go` (`Overhead`, `CapacityBytes`, `CapacityForDims`) —
the single source of truth; callers must not reimplement it.

```
total = floor(W × H × ch × bpc / 8)                 // bytes
cap   = total − 56   if total > 56, else 0          // maximum real payload, bytes
```

Overhead (56 B) = 16 salt + 4 container length + 4 real-length prefix + 32 tag.

Default settings (`ch = 3`, `bpc = 1`):

| Image | Pixels | Max payload |
|---|---|---|
| 100 × 100 | 10,000 | 3,694 B (3.61 KB) |
| 500 × 500 | 250,000 | 93,694 B (91.50 KB) |
| 1920 × 1080 | 2,073,600 | 777,544 B (759.32 KB) |
| 3840 × 2160 | 8,294,400 | 3,110,344 B (2.97 MB) |

`steg capacity -i <image>` prints the 3 × 4 table of every channel / bits-per-
channel combination.

---

## 11. Encode and decode procedures

### Encode (`steg.Encode`)

```
1. payload = read all input; paddedBlock = buildPaddedPayload(...)      (section 8)
2. order   = public prefix (section 4)
   salt    = 16 bytes from crypto/rand
   write salt, unencrypted, to stream bytes 0–15 via order[0..k−1]
3. keys    = Argon2id(password, salt)                                   (section 5)
   finish order with keys.orderKey
4. position the cipher and cursor at stream bit 128 (byte 16)
5. container write: LE32(L), paddedBlock, HMAC tag — all encrypted      (section 7)
```

### Decode (`steg.Decode`)

```
1. order = public prefix; read salt from stream bytes 0–15 (unencrypted)
2. keys  = Argon2id(password, salt); finish order with keys.orderKey
3. position at stream byte 16; container read with maxPayload = cap + 4  (section 7)
4. return the real payload
```

`Encode` and `Decode` share `prepareEncode` / `prepareDecode` in
`steg/steg.go` with the parallel paths, so all four obtain the identical order.

Library callers may pass a `draw.Image` whose bounds do not start at (0,0);
the cursor offsets every point by `Bounds().Min`. The cursor reads pixels with
`At().RGBA()` and writes them back as `color.RGBA` built from the low byte of
each 16-bit component, which is exact for 8-bit opaque images such as the
`*image.RGBA` the CLI always uses.

---

## 12. Parallel encode/decode

**File:** `steg/parallel.go`

`EncodeParallel` / `DecodeParallel` produce and accept exactly the same format
as `Encode` / `Decode`; the four are interchangeable.

* The salt is written (or read), keys derived and the order finished in
  `prepareEncode` / `prepareDecode` before any worker starts. Every worker
  shares the read-only order slice and builds its own cursor + cipher stack.
* The padded block (encode) or padded block + tag (decode) is split into jobs
  starting at stream byte 20. Each chunk is `alignment × 1024` bytes, where
  `alignment = lcm(8, bpp) / 8` bytes, so every chunk spans a whole number of
  pixels.
* The payload starts at bit 160, which is not a pixel boundary when `bpp` is a
  multiple of 3 (including the default `bpp = 3`). `firstChunkShift(bpp)` is the
  smallest `r ≥ 0` with `(160 + 8r) mod bpp = 0`; the first chunk is `r` bytes
  longer, which puts every later boundary on a pixel boundary. Without it two
  encode workers would read-modify-write the same pixel and one would silently
  discard the other's bits. (A mutex around `At`/`Set` does not prevent that;
  the whole load-modify-store sequence would have to be atomic.)
* On encode the HMAC is computed over the whole padded block before dispatch.
  After all workers finish, a single sequential stack writes the length field
  (byte 16) and the tag (byte `20 + L`); both may share a pixel with a worker's
  region, which is safe because no worker is running any more. Encode workers
  share one mutex around `img.At` / `img.Set` to avoid data races on the
  underlying pixel array.
* On decode the length is read and bounded first (`L ≤ cap + 4`), then workers
  fill the buffer and the tag is verified over the assembled block.

---

## 13. Implementation layers

```
┌──────────────────────────────────────────────────────────────┐
│  cmd/steg  (Cobra CLI; PNG/BMP/TIFF I/O; transparency check;  │
│             verify-after-write)                                │
└───────────────────────┬──────────────────────────────────────┘
                        │ draw.Image + password
┌───────────────────────▼──────────────────────────────────────┐
│  steg.Encode / Decode / EncodeParallel / DecodeParallel        │
│  prepareEncode/prepareDecode: public order → salt → Argon2id   │
│  → encKey, macKey, nonce, orderKey → finished order            │
└───────┬───────────────────────────────────────┬──────────────┘
        │ []image.Point (order)                 │ encKey, nonce
┌───────▼─────────────────────┐   ┌─────────────▼──────────────┐
│  cursors/order.go           │   │  cipher.NewCipher          │
│  NewPixelOrder (public)     │   │  AES-128-CTR, bit-indexed, │
│  FinishPixelOrder (keyed)   │   │  seekable                  │
│  cursors.RNGCursor          │   │                            │
│  bit↔pixel mapping, cache   │   │                            │
└───────┬─────────────────────┘   └─────────────┬──────────────┘
        │ Cursor                                │ StreamCipherBlock
┌───────▼───────────────────────────────────────▼──────────────┐
│  cursors.CipherMiddleware  (EncryptByte / DecryptByte)         │
└───────┬──────────────────────────────────────────────────────┘
        │ Cursor (ReadByte / WriteByte / Seek in bits)
┌───────▼──────────────────────────────────────────────────────┐
│  cursors.CursorAdapter  (Cursor → io.ReadWriteSeeker, bytes)   │
└───────┬──────────────────────────────────────────────────────┘
        │ io.ReadWriteSeeker + HMAC-SHA256(macKey)
┌───────▼──────────────────────────────────────────────────────┐
│  container.WritePayload / ReadPayload                          │
│  [LE32 length][padded block][32-byte tag]                      │
└──────────────────────────────────────────────────────────────┘
```

Notes on the layers:

* `Cursor` is byte-oriented (`ReadByte`, `WriteByte`, `Seek`) but `Seek`
  offsets are in **bits**. `CursorAdapter` multiplies byte offsets by 8.
* `CipherMiddleware.Seek` seeks the cipher and the cursor to the same bit
  position, which is what makes the keystream index equal the stream bit
  position (section 6).
* `RNGCursor` caches one pixel and writes it back once when the cursor leaves
  it, so each pixel costs one `At` and one `Set` regardless of `bpp`.

---

## 14. CLI behaviour

**File:** `cmd/steg/root.go`

These are properties of the `steg` command, not of the format.

* Every input image is copied into a fresh `*image.RGBA` with origin (0,0)
  before encoding or decoding.
* `encode` and `test-visual` reject a carrier that has any non-opaque pixel,
  whatever the output format. PNG and BMP store straight alpha, so writing the
  alpha-premultiplied buffer would destroy hidden bits in translucent pixels;
  TIFF would keep them but produce invalid premultiplied pixels. The user is
  told to flatten the image onto an opaque background.
* `encode` loads and checks the carrier and reads the payload file **before**
  asking for the password.
* `encode` writes the result to a temporary file next to the output (same
  extension, so the same format), decodes that file through the same function
  `steg decode` uses, compares the result byte for byte with the payload, and
  only then renames it onto the output path. On any failure the temporary file
  is removed and an existing output file is left untouched.
* `decode` decodes fully before creating the output file, so a wrong password
  leaves nothing behind.
* Output format is chosen by extension: `.bmp`, `.tif`/`.tiff`, otherwise PNG.

---

## 15. Design rationale

### Single Argon2id call over a per-image random salt

All key material comes from one Argon2id call over the password and a fresh
16-byte salt. The salt has to be readable before any key exists, so it is
stored in plaintext; that is harmless, since a salt is not a secret. A fresh
salt on every encode gives a unique `encKey`/`nonce` pair, and therefore a
unique keystream, even when the same password and carrier are reused. One call
producing 84 bytes is sliced into four independent values instead of paying
the KDF cost four times.

### Public salt positions, keyed order for everything else

The decoder must find the salt before it can derive any key, so the salt's
pixel positions cannot depend on the password. They are taken from a public
ChaCha8 stream: anyone can compute `order[0..k−1]` from the image size and
`bpp`, by design. Everything after the salt is placed by the shuffle under
`orderKey`, which is Argon2id output, so recomputing the rest of the order for
a guessed password costs one full KDF call; there is no cheaper shortcut.

The earlier design seeded `math/rand` with the first 8 bytes of
SHA-256(password): recomputing the order for a password guess cost one SHA-256
instead of a KDF call.

**What the secret order buys today: nothing.** The encoder fills the whole
carrier, so every pixel carries data and the order hides nothing about
*where* the data is. It starts to matter once padding is bounded (README, Next
steps, item 2) and only part of the image is touched; this change is that
item's prerequisite.

### ChaCha8 and a hand-written bounded draw

The order is part of the format, so it must be bit-for-bit reproducible
forever. `ChaCha8.Uint64` is fixed by the chacha8rand specification;
`rand.Rand.IntN` and `rand.Shuffle` are not guaranteed stable across Go
releases, so the shuffle uses its own rejection-sampling draw.
`TestPixelOrderGolden` pins the output.

### Full-capacity padding

Writing the whole carrier on every encode removes payload size as a
statistical signal: a 1-byte and a 500 KB payload produce the same LSB
disturbance. The cost is that the disturbance is always maximal; see the
limitations below.

### MAC-then-encrypt

The HMAC is computed over the plaintext padded block and then encrypted with
it. Encrypt-then-MAC is the usual recommendation; here the tag lives inside the
encrypted channel, decryption is a cheap XOR, and nothing is released before
the tag verifies, so the ordering is not exploitable in this threat model.

### Bit-level CTR

The cipher is indexed by stream bit so that any bit range can be encrypted or
decrypted independently. That is what lets parallel workers seek to their
chunk without coordinating cipher state.

---

## 16. Known limitations

| Issue | Severity | Notes |
|---|---|---|
| Statistical steganalysis | High | LSB replacement at full capacity is flagged by chi-square and RS analysis on every channel written to; `steg detect` does it without the password. RS is noisier on small images (below roughly 512×512). |
| Secret order currently ineffective | Info | With full-capacity padding every pixel carries data, so the keyed order hides nothing. It only matters with bounded padding. |
| Public salt positions | Info | By design: the salt pixels are computable from the image size and settings, and the salt is plaintext. Anyone can read it; it does not help guess the password. |
| Dictionary attacks on weak passwords | Medium | Each guess costs one Argon2id (t=2, 64 MiB, p=4) call; the length bound rejects most wrong guesses right after it. Strength rests on the password. |
| No format version field | Low | A decoder of the wrong format version fails as if the password were wrong. Images from pre-v2 builds must be decoded with the build that wrote them. |
| Settings not stored | Low | `bpc` and `ch` must be supplied at decode time; a mismatch looks like a wrong password. |
| MAC-then-encrypt | Low | See section 15. |
| No streaming decode | Medium | The full padded block (carrier capacity + 4 bytes) is held in memory. The length is bounded by capacity before allocation, so a wrong password cannot trigger an oversized allocation. |
| Transparent carriers rejected (CLI) | Low | Carriers with any non-opaque pixel are refused; flatten them first. |
| 8-bit pixels only | Low | The cursor writes `color.RGBA` built from the low byte of each component. The CLI converts every carrier to 8-bit RGBA, so a 16-bit input comes out as 8-bit; library callers passing a `draw.Image` with deeper components would get wrong pixels. |
| Lossy formats unsupported | High | JPEG and other lossy formats destroy LSB data. Only PNG, BMP and TIFF are supported. |
