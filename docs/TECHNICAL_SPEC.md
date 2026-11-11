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

Steg hides an arbitrary byte string in a lossless RGB image by setting the
`N` least-significant bits of up to three colour channels of a prefix of the
pixels, taken in a pixel order that is partly public and partly derived from the password. The
embedded data is a single **bit stream**:

```
[16-byte plaintext salt][AES-128-CTR encrypted: length | padded payload | HMAC-SHA256 tag]
```

The payload is followed by random padding up to a power-of-two bucket of at
least 4 KiB, or up to the full capacity of the carrier when that bucket does
not fit (section 8). Only the stream prefix up to the end of the tag is
written; every pixel after it in the order keeps its original value. At 1 bit
per channel the encoder embeds by LSB matching (±1), at 2–8 bits by
replacement (section 3.1); the decoder reads bits the same way in both cases.

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

Reading a stream bit reads that bit of the channel value. Inactive channels and
alpha are never written; how the active channel values change on write is
specified in section 3.1.

### 3.1 Write rule

The format defines only the value of the `bpc` low bits of each written channel
value. A conforming encoder may change a channel value in any way that leaves
those bits equal to the stream bits mapped to them; the decoder cannot tell the
difference. This encoder does one of two things:

* **`bpc = 1`: LSB matching** (`cursors.WithLSBMatching`, `writeByteMatching`).
  If bit 0 of the value `v` already equals the stream bit, `v` is left alone.
  Otherwise `v` becomes `v + 1` or `v − 1`, chosen at random; `v = 0` always
  becomes 1 and `v = 255` always becomes 254. Both candidates have the required
  bit 0, so every written value is within ±1 of the original and never wraps.
  The direction bits come from a ChaCha8 generator private to each cursor and
  seeded with 32 bytes from `crypto/rand` when the cursor is created; one
  generator bit is consumed per changed value. They are independent of the
  password and of every derived key, so they reveal nothing about either, and
  encoding the same input twice gives different images (the salt already made
  them differ in the encrypted region).
* **`bpc = 2..8`: LSB replacement.** Writing a bit sets or clears only that bit;
  the bits above the low `bpc` are left as they were. The `bpc` bits of a
  channel are written by separate slot steps, possibly separate `WriteByte`
  calls, so a minimum-distortion `bpc`-bit choice is out of scope; a cursor
  created with `WithLSBMatching` ignores the option at these settings and is
  byte-identical to one without it.

Matching is enabled on every write path (the salt write in `prepareEncode`, the
sequential container write, every parallel worker and the post-parallel length
and tag writes) and never on decode: `cursorOptions(..., write)` adds
`WithLSBMatching` exactly when `write` is true. Images from encoders that used
replacement at `bpc = 1` and images from this one are mutually decodable.

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

The counter is a uint32, so the keystream covers 2³² blocks (64 GiB). Seeking
past that returns an error rather than wrapping and reusing keystream; no
carrier the format accepts gets close (section 10).

---

## 7. Stream layout and container framing

**Files:** `steg/encode.go`, `steg/decode.go`, `steg/container/container.go`

Byte offsets are stream byte offsets (stream bit = 8 × byte).

| Stream bytes | Size | Cipher | Field |
|---|---|---|---|
| 0–15 | 16 B | none | `salt` |
| 16–19 | 4 B | AES-CTR | `L` = container length, LE32. The encoder writes `L = paddedSize(R, cap)` (section 8) |
| 20–23 | 4 B | AES-CTR | real payload length `R`, LE32 (first 4 bytes of the padded block) |
| 24 … 24+R−1 | R B | AES-CTR | real payload |
| 24+R … 20+L−1 | L−4−R B | AES-CTR | random padding |
| 20+L … 20+L+31 | 32 B | AES-CTR | HMAC-SHA256 tag |

Bytes `20 .. 20+L−1` are the **padded block**. The stream ends at byte
`20 + L + 32`; the encoder writes nothing after it, so the channel values of
every slot past stream bit `8 × (52 + L)` keep the carrier's original values
(the pixel holding the last tag bit may also contain later, untouched slots).
When the encoder falls back to `L = cap + 4` the stream ends at byte
`56 + cap = floor(n × bpp / 8)`, i.e. exactly at capacity.

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

**File:** `steg/steg.go` (`paddedSize`, `minPaddedBlock`, `buildPaddedPayload`,
`extractRealPayload`)

```
paddedBlock = LE32(R) ‖ payload ‖ crypto/rand bytes      // length paddedSize(R, cap)
```

The block length is the smallest power of two that is at least `4 + R` and at
least `minPaddedBlock = 4096`, unless that bucket exceeds `cap + 4`, in which
case it is `cap + 4` (the carrier is filled completely). As implemented:

```
paddedSize(R, cap):
    full   = cap + 4
    need   = R + 4
    bucket = 4096
    while bucket < need and bucket < full:
        bucket = bucket × 2
    if bucket > full: return full
    return bucket
```

Examples:

| `R` (bytes) | `4 + R` | `L` (with `cap + 4 ≥ L`) |
|---|---|---|
| 0 … 4092 | ≤ 4096 | 4096 |
| 4093 | 4097 | 8192 |
| 8188 | 8192 | 8192 |
| 8189 | 8193 | 16384 |

If the bucket does not fit, `L = cap + 4`: for example any payload on a
carrier with `cap + 4 < 4096` (such as 100 × 100 at the defaults, `cap = 3694`),
or a 600,000-byte payload when `cap + 4` lies between 600,004 and 1,048,575.
A bucket exactly equal to `cap + 4` is the same thing. Since `R ≤ cap`, the
result always satisfies `4 + R ≤ L ≤ cap + 4`.

If `R > cap` encode fails ("payload too large"). If `cap ≤ 0` encode fails
("image too small to hold any payload"). The padding is encrypted like
everything else, so on the image it is indistinguishable from payload.

The bucket is an encoder choice, not part of the format: the decoder accepts
any `L` with `4 + R ≤ L ≤ cap + 4` (section 7). Images written by the previous
encoder, which always used `L = cap + 4`, decode unchanged, and that encoder's
decoder reads bucketed images.

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

Valid settings are `bpc` in 1..8 and `ch` in 1..3. A carrier may have at most
2³²−1 pixels (the order stores 4-byte pixel indices), and `total − 56 + 4`
must fit the 32-bit container length field, so capacity is at most 4 GiB.
`CapacityBytes` / `CapacityForDims` return 0 for anything outside these limits,
and all four entry points reject such input before touching a pixel.

---

## 11. Encode and decode procedures

### Encode (`steg.Encode`)

```
1. payload = read all input; paddedBlock = buildPaddedPayload(...)      (section 8)
   L = len(paddedBlock) = paddedSize(R, cap)
2. order   = public prefix (section 4)
   salt    = 16 bytes from crypto/rand
   write salt, unencrypted, to stream bytes 0–15 via order[0..k−1]
3. keys    = Argon2id(password, salt)                                   (section 5)
   finish order with keys.orderKey
4. position the cipher and cursor at stream bit 128 (byte 16)
5. container write: LE32(L), paddedBlock, HMAC tag — all encrypted      (section 7)
   stream bytes 20+L+32 onward are not written
```

Every write in steps 2 and 5 follows the write rule of section 3.1 (LSB
matching at `bpc = 1`, replacement above).

### Decode (`steg.Decode`)

```
1. order = public prefix; read salt from stream bytes 0–15 (unencrypted)
2. keys  = Argon2id(password, salt); finish order with keys.orderKey
3. position at stream byte 16; container read with maxPayload = cap + 4  (section 7)
4. return the real payload
```

`Encode` and `Decode` share `prepareEncode` / `prepareDecode` in
`steg/steg.go` with the parallel paths, so all four obtain the identical order.

The carrier must be an `*image.RGBA` or `*image.NRGBA`; every other
`draw.Image` is rejected (error wrapping `cursors.ErrUnsupportedImage`) before
any pixel is touched, together with invalid settings (section 10). The cursor
reads and writes the R, G and B bytes of the image's `Pix` slice directly, so
values round-trip exactly with no color conversion; alpha is never touched, and
an `*image.NRGBA` with translucent pixels works. Bounds need not start at
(0,0): order index `i` is the pixel at `(i mod W, i div W)` relative to
`Bounds().Min`, addressed through the image's stride, so sub-images work.

For `*image.RGBA` this is bit-identical to the earlier implementation that went
through `At().RGBA()` and `Set(color.RGBA{…})` (each 8-bit channel `c` reads as
`c × 0x101`, whose low byte is `c`); `cursors/reference_test.go` checks it.

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
  pixels. The padded block is usually a bucket far smaller than the carrier
  (section 8) and may fit in one chunk: a 4 KiB block does whenever
  `alignment ≥ 4` (at the defaults, `alignment = 3`, it takes two jobs), and so
  does the full-fill block of a small carrier. The dispatch loop then sends a
  single job clipped to the block.
* Every encode worker, and the sequential stack that writes the length and
  tag, creates its own cursor with LSB matching at `bpc = 1` (section 3.1),
  so each has its own direction generator and workers share no random state.
* The payload starts at bit 160, which is not a pixel boundary when `bpp` is a
  multiple of 3 (including the default `bpp = 3`). `firstChunkShift(bpp)` is the
  smallest `r ≥ 0` with `(160 + 8r) mod bpp = 0`; the first chunk is `r` bytes
  longer, which puts every later boundary on a pixel boundary. Without it two
  encode workers would read-modify-write the same pixel and one would silently
  discard the other's bits. (A mutex around each pixel access would not
  prevent that; the whole read-modify-write of a pixel would have to be
  atomic.) With aligned chunks every worker owns whole pixels, so workers write
  disjoint bytes of `Pix` and need no lock.
* On encode the HMAC is computed over the whole padded block before dispatch.
  After all workers finish, a single sequential stack writes the length field
  (byte 16) and the tag (byte `20 + L`); both may share a pixel with a worker's
  region, which is safe because no worker is running any more.
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
        │ []uint32 (order)                      │ encKey, nonce
┌───────▼─────────────────────┐   ┌─────────────▼──────────────┐
│  cursors/order.go           │   │  cipher.NewCipher          │
│  NewPixelOrder (public)     │   │  AES-128-CTR, bit-indexed, │
│  FinishPixelOrder (keyed)   │   │  seekable                  │
│  cursors.RNGCursor          │   │                            │
│  bit↔pixel mapping on Pix   │   │                            │
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
* `RNGCursor` reads and writes the carrier's `Pix` bytes directly; there is no
  pixel cache and nothing to flush.

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
* `encode` requires `-i`, `-f` and `-o`; `decode` requires `-i` and `-o`. A
  missing flag prints usage; runtime errors print only the error.
* `encode` picks the output format from the extension (`.png`, `.bmp`,
  `.tif`, `.tiff`, case-insensitive) and rejects any other extension before
  doing anything else.
* `decode` decodes fully before creating the output file, so a wrong password
  leaves nothing behind. The payload is written to a temporary sibling with
  mode `0600`, synced and renamed into place. An output path that is a
  symlink, device or FIFO (e.g. `/dev/stdout`) is written through in place.

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

**What the secret order buys.** With bounded padding (section 8) only the
stream prefix up to the end of the tag is written, so the touched pixels are
the first `ceil(8 × (52 + L) / bpp)` entries of the order: the public salt
pixels plus a pseudorandom subset, spread over the whole image, that depends
on Argon2id output. Without the password an attacker cannot tell which pixels
carry data, and cannot test a candidate subset more cheaply than one KDF call
per password guess. When the payload fills the carrier (`L = cap + 4`) every
pixel carries data and the order hides nothing about *where* it is.

### ChaCha8 and a hand-written bounded draw

The order is part of the format, so it must be bit-for-bit reproducible
forever. `ChaCha8.Uint64` is fixed by the chacha8rand specification;
`rand.Rand.IntN` and `rand.Shuffle` are not guaranteed stable across Go
releases, so the shuffle uses its own rejection-sampling draw.
`TestPixelOrderGolden` pins the output.

### Bounded padding

Earlier builds padded every payload to the whole carrier, which hid the payload
size completely (a 1-byte and a 500 KB payload produced the same disturbance)
but made the disturbance always maximal: a 31-byte text changed 87.5% of the
pixels of a 1024×1536 photo. Padding to a power-of-two bucket of at least
4 KiB keeps most of the size-hiding and changes 0.62% of the pixels for the
same payload. Above the 4 KiB floor the bucket is less than twice the length
an unpadded block would need; below it, the floor makes every payload up to
4,092 bytes look the same, at the cost of writing 4 KiB even for one byte.

The trade-off is a size leak. The bucket hides the payload size only to within
a factor of two. `L` itself is encrypted, so reading it needs the password,
but the number of changed values is roughly proportional to `L`, so a
steganalyst who can estimate the embedding rate (or who has the original
carrier) learns roughly which bucket was used. A payload that falls back to
full fill hides its size as completely as before.

### LSB matching at 1 bit per channel

Replacement keeps every value inside its pair `(2k, 2k+1)`, so random data
evens out the counts within each pair; that is the signature chi-square and RS
analysis detect. Matching moves a value by ±1 at random, which crosses pair
boundaries half the time and leaves the pair structure intact. On a 1024×1536
photo filled to capacity, chi-square p went from 0.13–0.95 to 0.0000 on every
channel and RS asymmetry from about −0.043 to between −0.0015 and +0.0014;
`steg detect` changed from `LIKELY_STEGO` to `CLEAN`. This defeats those two
detectors only. Modern steganalysis (histogram characteristic-function
attacks, rich models such as SRM with a trained classifier) detects LSB
matching at high embedding rates, especially in smooth regions; adaptive
embedding is what would address that.

The directions are drawn from `crypto/rand`-seeded ChaCha8 rather than from
anything keyed, because a direction that depended on the password or keys
would leak information about them through the image. Matching only changes
which of two values with the right bit 0 is written, so it needed no format
change. At `bpc ≥ 2` a channel's bits are written in separate steps and the
minimum-distortion choice is not local to one step, so the encoder keeps
replacement there.

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
| Statistical steganalysis | Medium | At `bpc = 1` LSB matching defeats chi-square and RS analysis (`steg detect` reads `CLEAN` even at full capacity on a natural photo). It does not make the image undetectable: modern detectors (characteristic-function attacks, SRM-type rich models with a trained classifier) detect LSB matching at high embedding rates, especially in smooth regions. At `bpc ≥ 2` the encoder uses replacement, which chi-square and RS flag at high embedding rates. RS is noisier on small images (below roughly 512×512). |
| Payload size bucketed, not hidden | Low | `L` reveals the payload size to within a factor of two (section 8). It is encrypted, but the embedding rate, which an analyst can estimate, grows with it. Full-fill payloads hide their size completely. |
| Secret order ineffective at full fill | Info | When `L = cap + 4` every pixel carries data, so the keyed order hides nothing about where the data is. With a smaller bucket it selects which pixels are touched. |
| Output not reproducible | Info | The salt and the LSB-matching directions come from `crypto/rand`, so the same input encodes to a different image each time. |
| Public salt positions | Info | By design: the salt pixels are computable from the image size and settings, and the salt is plaintext. Anyone can read it; it does not help guess the password. |
| Dictionary attacks on weak passwords | Medium | Each guess costs one Argon2id (t=2, 64 MiB, p=4) call; the length bound rejects most wrong guesses right after it. Strength rests on the password. |
| No format version field | Low | A decoder of the wrong format version fails as if the password were wrong. Images from pre-v2 builds must be decoded with the build that wrote them. |
| Settings not stored | Low | `bpc` and `ch` must be supplied at decode time; a mismatch looks like a wrong password. |
| MAC-then-encrypt | Low | See section 15. |
| No streaming decode | Medium | The full padded block (`L` bytes, at most carrier capacity + 4) is held in memory. The length is bounded by capacity before allocation, so a wrong password cannot trigger an oversized allocation. |
| Transparent carriers rejected (CLI) | Low | Carriers with any non-opaque pixel are refused; flatten them first. |
| 8-bit carriers only | Low | The library accepts only `*image.RGBA` and `*image.NRGBA` and rejects other types. The CLI converts every carrier to 8-bit RGBA, so a 16-bit or grayscale input comes out as 8-bit RGB. |
| Size limits | Low | At most 2³²−1 pixels and 4 GiB of capacity per carrier (section 10). |
| Lossy formats unsupported | High | JPEG and other lossy formats destroy LSB data. Only PNG, BMP and TIFF are supported. |
