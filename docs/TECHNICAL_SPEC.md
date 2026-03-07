# Steg — Technical Specification

**Version:** v2 (post security hardening)
**Date:** 2026-03-07

---

## Table of Contents

1. [Overview](#1-overview)
2. [Layer Architecture](#2-layer-architecture)
3. [Key Derivation](#3-key-derivation)
4. [Pixel Traversal — RNG Cursor](#4-pixel-traversal--rng-cursor)
5. [Bit-Level Stream Cipher](#5-bit-level-stream-cipher)
6. [Cursor Adapter](#6-cursor-adapter)
7. [Cipher Middleware](#7-cipher-middleware)
8. [Container Framing](#8-container-framing)
9. [On-Image Layout](#9-on-image-layout)
10. [Encode Flow](#10-encode-flow)
11. [Decode Flow](#11-decode-flow)
12. [Capacity](#12-capacity)
13. [Design Decisions](#13-design-decisions)
14. [Known Limitations](#14-known-limitations)

---

## 1. Overview

Steg hides an arbitrary file inside a lossless image by modifying the least-significant bit (LSB) of selected color channels of a pseudorandom subset of pixels. The data is encrypted and authenticated before being written, so an observer who recovers the bits cannot read the payload without the password, and any tampering is detected at decode time.

The tool operates entirely on the local filesystem. Input and output are PNG, BMP, or TIFF files. The carrier image is modified in-place in memory and re-encoded in lossless format, so no visual quality is lost and no pixel value changes by more than 1 in the modified channels.

---

## 2. Layer Architecture

The implementation is structured as a stack of composable layers. Each layer implements or wraps a narrow interface so that components can be tested and substituted independently.

```
┌─────────────────────────────────────────────────────┐
│  cmd/steg  (CLI — Cobra, PNG I/O)                   │
└────────────────────┬────────────────────────────────┘
                     │ draw.Image + password
┌────────────────────▼────────────────────────────────┐
│  steg.Encode / steg.Decode  (orchestration)         │
│    deriveKeys → seed, encKey, macKey  (Argon2id)    │
└───┬───────────────────────────────────┬─────────────┘
    │ seed                              │ encKey, macKey
┌───▼────────────────┐       ┌──────────▼──────────────┐
│  cursors.RNGCursor │       │  cipher.NewCipher        │
│  (pixel traversal) │       │  (AES-128-CTR, bit-level)│
└───┬────────────────┘       └──────────┬───────────────┘
    │ Cursor interface                  │ StreamCipherBlock
┌───▼────────────────────────────────── ▼───────────────┐
│  cursors.CipherMiddleware  (encrypts/decrypts each bit)│
└───┬────────────────────────────────────────────────────┘
    │ Cursor interface
┌───▼──────────────────────────────────────────────────┐
│  cursors.CursorAdapter  (bit-level → io.ReadWriteSeeker) │
└───┬──────────────────────────────────────────────────┘
    │ io.ReadWriteSeeker + hash.Hash (HMAC-SHA256)
┌───▼──────────────────────────────────────────────────┐
│  container.WritePayload / ReadPayload                │
│  [length][payload][HMAC-SHA256 tag]                  │
└──────────────────────────────────────────────────────┘
```

All layers below `steg.Encode/Decode` are unaware of each other's existence and interact only through `Cursor`, `io.ReadWriteSeeker`, or `hash.Hash`.

---

## 3. Key Derivation

**File:** `steg/steg.go`

A single Argon2id call produces all cryptographic material from the password:

```
derived = Argon2id(password, appSalt, time=1, memory=64 MiB, threads=4, length=60)

seed      = int64( BigEndian(derived[0:8])  )  // RNG cursor seed
encKey    = derived[8:24]                       // 16-byte AES-128 key
macKey    = derived[24:56]                      // 32-byte HMAC-SHA256 key
kdfNonce  = BigEndian.Uint32(derived[56:60])   // bootstrap cipher nonce
```

**Parameters:**

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| Algorithm | Argon2id | Resists GPU and side-channel attacks; OWASP 2023 recommendation |
| Time cost | 1 | OWASP interactive-login profile |
| Memory | 64 MiB | Memory-hardness as the primary brute-force deterrent |
| Parallelism | 4 | Matches common core counts; increases attacker cost |
| Salt | `"github.com/pableeee/steg/v1"` (fixed) | Domain separator; memory-hardness compensates for the lack of randomness |
| Output length | 60 bytes | 8 (seed) + 16 (AES key) + 32 (MAC key) + 4 (bootstrap nonce) |

All four values are derived from the same password in a single pass and are cryptographically independent — learning one gives no information about the others.

---

## 4. Pixel Traversal — RNG Cursor

**File:** `cursors/rng_cursor.go`
**Interface:** `cursors.Cursor`

```go
type Cursor interface {
    Seek(offset int64, whence int) (int64, error)
    WriteBit(bit uint8) (uint, error)
    ReadBit() (uint8, error)
}
```

The `RNGCursor` presents the image as a flat, seekable sequence of bits by mapping a linear bit position to a specific pixel and channel.

### Pixel sequence generation

All `width × height` pixel coordinates are enumerated in row-major order and then Fisher-Yates-shuffled using `math/rand` seeded with the Argon2id-derived `seed`:

```
positions[0..N-1] = shuffle( {(x,y) : 0 ≤ x < W, 0 ≤ y < H} )
```

This shuffle is deterministic: the same seed always produces the same pixel order, which is how decode reproduces the exact traversal used during encode.

### Channel selection

The cursor supports three channels per pixel: red (`R_Bit = 0x1`), green (`G_Bit = 0x2`), and blue (`B_Bit = 0x4`). The `channels` parameter on `Encode`/`Decode` controls which are active:

- `channels = 1` → R only (default `R_Bit`)
- `channels = 2` → R + G (`UseGreenBit()` added)
- `channels = 3` → R + G + B (`UseBlueBit()` added, the default CLI setting)

The `cursorOptions(seed, bitsPerChannel, channels)` helper in `steg/steg.go` constructs this option slice.

With `channels` active channels and `bitsPerChannel` bits per channel, the slot-to-image mapping is:

```
bitsPerPixel = channels × bitsPerChannel
pixelIndex   = cursorPosition / bitsPerPixel
slotInPixel  = cursorPosition % bitsPerPixel
channelIndex = slotInPixel / bitsPerChannel         (selects channel)
bitInChannel = (bitsPerChannel − 1) − (slotInPixel % bitsPerChannel)  (MSB-first)
```

### Bit read/write

`bitsPerChannel` LSBs (1–8) of each selected channel are read or written. Bits within a channel are ordered MSB-first. At the default of 1 bit/channel no pixel changes by more than ±1 per modified channel; higher settings increase the change magnitude and visual impact proportionally.

---

## 5. Bit-Level Stream Cipher

**File:** `cipher/cipher.go`
**Interface:** `cipher.StreamCipherBlock`

```go
type StreamCipherBlock interface {
    Seek(offset int64, whence int) (int64, error)
    EncryptBit(bit uint8) (uint8, error)
    DecryptBit(bit uint8) (uint8, error)
}
```

The cipher implements AES-128 in a custom counter mode that operates at the individual-bit level and supports seeking within the keystream.

### Counter block construction

Each 128-bit AES block is constructed as:

```
block_input = [ nonce (4 B, LE) | 0x00 0x00 0x00 0x00 | counter (4 B, LE) | 0x00 0x00 0x00 0x00 ]
keystream_block = AES_128(encKey, block_input)
```

Counter is a `uint32` incremented once per 128-bit (16-byte) keystream block consumed. The cipher maintains a window over the current block using `mixIndex` (start bit of block) and `maxIndex` (end bit of block), refreshing when the position falls outside the window.

### Bit extraction from keystream

Given bit position `index` within the keystream:

```
byte_in_block = (index / 8) % 16
bit_in_byte   = index % 8           // LSB = 0, MSB = 7
keystream_bit = (keystream_block[byte_in_block] >> bit_in_byte) & 1
output_bit    = input_bit XOR keystream_bit
```

Note: within each keystream byte, bits are consumed LSB-first.

### Seeking

`Seek` computes the target block counter from the requested bit position and refreshes the block if necessary. This allows the orchestration layer to position the cipher at bit 32 after the bootstrap write so that the payload cipher's keystream byte 0 aligns with on-image byte 4 (the first byte of the container length field).

---

## 6. Cursor Adapter

**File:** `cursors/adapter.go`

`CursorAdapter` bridges the bit-level `Cursor` interface to the standard `io.ReadWriteSeeker` interface expected by the container layer.

### Byte packing

Bytes are packed **MSB-first**: the first bit read/written corresponds to bit 7 of the byte.

```
Write(byte b):
    for i = 7 downto 0:
        WriteBit( (b >> i) & 1 )

Read() → byte b:
    for i = 7 downto 0:
        b |= ReadBit() << i
```

### Seek translation

The adapter multiplies incoming byte offsets by 8 before delegating to the cursor, and divides the returned bit position by 8 to satisfy the `io.ReadWriteSeeker` contract.

---

## 7. Cipher Middleware

**File:** `cursors/middleware.go`

`CipherMiddleware` is a `Cursor` decorator that transparently encrypts bits on write and decrypts bits on read, without either the layers above or below knowing about each other.

```
WriteBit(b):  → EncryptBit(b) → WriteBit(encrypted_b) to underlying Cursor
ReadBit():    → ReadBit() from underlying Cursor → DecryptBit(b) → return plaintext_b
Seek(n):      → Seek(n) on cipher AND Seek(n) on underlying Cursor (must stay in sync)
```

The middleware is inserted between the raw `RNGCursor` and the `CursorAdapter`, so all bytes that flow through the adapter are automatically encrypted or decrypted.

---

## 8. Container Framing

**File:** `steg/container/container.go`

The container layer adds a length prefix and an authentication tag around the payload. It is unaware of encryption — it writes to whatever `io.WriteSeeker` it receives, which may or may not be backed by a cipher.

### Write sequence

```
1. Note current stream position as basePos.
2. Seek to basePos + 4  (reserve 4 bytes for the length field).
3. Stream payload bytes from io.Reader to writer; feed each chunk to hashFn.
4. Write hashFn.Sum(nil)  (32 bytes for HMAC-SHA256).
5. Seek back to basePos.
6. Write uint32(payload_length) as 4 bytes, little-endian.
```

Because the payload length is not known in advance, it is written last by seeking back to the reserved slot.

### Read sequence

```
1. Read 4 bytes → uint32 length (little-endian).
2. Read length bytes → payload; feed to hashFn.
3. Read hashFn.Size() bytes → stored tag.
4. Compare hashFn.Sum(nil) with stored tag.
5. If equal, return payload; otherwise return error.
```

### Hash function

The `hashFn` parameter is `hash.Hash`. During encode and decode this is always `hmac.New(sha256.New, macKey)`, giving a 32-byte HMAC-SHA256 tag keyed with the Argon2id-derived MAC key.

---

## 9. On-Image Layout

Bits are stored in the order the `RNGCursor` produces them (pseudorandom pixel sequence, green channel before blue channel within each pixel).

```
Bit offset           Size        Cipher                  Content
────────────────────────────────────────────────────────────────────────────────
0                    32 bits     AES-128-CTR (bootstrap) Per-encode random nonce
32                   32 bits     AES-128-CTR (payload)   Container length (uint32, LE)
64                   32 bits     AES-128-CTR (payload)   Real payload length (uint32, LE)
96                   N×8 bits    AES-128-CTR (payload)   Real payload bytes
96 + N×8             P×8 bits    AES-128-CTR (payload)   Random padding (fills to capacity)
96 + (N+P)×8         256 bits    AES-128-CTR (payload)   HMAC-SHA256 tag (32 bytes)
```

**Total bits used:** always exactly `W × H × channels × bitsPerChannel` — the full image
capacity is written on every encode regardless of payload size.

Two AES-128-CTR cipher instances are used:

- **Bootstrap cipher** — `AES-CTR(encKey, kdfNonce)` encrypts only the 4-byte random
  nonce at bits 0–31. `kdfNonce` is fixed per password (KDF-derived). No plaintext
  appears at any position.
- **Payload cipher** — `AES-CTR(encKey, randomNonce)` encrypts everything from bit 32
  onwards. `randomNonce` is `crypto/rand`-generated and differs on every encode,
  giving a unique keystream even when the same password and carrier are reused.

The payload cipher is seeked to bit 32 before the container layer begins, so payload
cipher keystream byte 0 aligns with on-image byte 4. The cipher and cursor positions
remain synchronized throughout.

---

## 10. Encode Flow

```
steg.Encode(img draw.Image, pass []byte, payload io.Reader, bitsPerChannel, channels int)

1.  deriveKeys(pass) → seed, encKey, macKey, kdfNonce

2.  realPayload = io.ReadAll(payload)
    padded = buildPaddedPayload(img, realPayload, bitsPerChannel, channels)
        Layout: [4B real-length LE][real-payload][crypto/rand padding]
        Size:   imageCapacity + 4 bytes (fills the image to capacity)

3.  cur = NewRNGCursor(img, cursorOptions(seed, bitsPerChannel, channels)...)

4.  randomNonce = crypto/rand.Read(4)
    bootstrapCipher = cipher.NewCipher(kdfNonce, encKey)
    bootstrapAdapter = CursorAdapter(CipherMiddleware(cur, bootstrapCipher))
    bootstrapAdapter.Write(randomNonce)
        Encrypts randomNonce with the bootstrap cipher and writes the 4-byte
        ciphertext to image bytes 0–3. No plaintext appears on the image.

5.  payloadCipher = cipher.NewCipher(randomNonce, encKey)
    payloadCM = CipherMiddleware(cur, payloadCipher)
    payloadCM.Seek(32, SeekStart)
        Advances cursor to bit 32 (byte 4) and aligns payloadCipher
        keystream so byte 0 of the cipher corresponds to on-image byte 4.

6.  adapter = CursorAdapter(payloadCM)
    mac = hmac.New(sha256.New, macKey)
    container.WritePayload(adapter, bytes.NewReader(padded), mac)
        Writes: [encrypted container-length][encrypted padded block][encrypted HMAC]
        container-length field lands at on-image bytes 4–7.
        padded block starts at on-image byte 8.

7.  cur.Flush()
    png.Encode(outputFile, img)
```

---

## 11. Decode Flow

```
steg.Decode(img draw.Image, pass []byte, bitsPerChannel, channels int) → []byte

1.  deriveKeys(pass) → seed, encKey, macKey, kdfNonce

2.  cur = NewRNGCursor(img, cursorOptions(seed, bitsPerChannel, channels)...)
        Reproduces the identical shuffled pixel sequence with identical channel/bit
        configuration (must match the values used during encode).

3.  bootstrapCipher = cipher.NewCipher(kdfNonce, encKey)
    bootstrapAdapter = CursorAdapter(CipherMiddleware(cur, bootstrapCipher))
    io.ReadFull(bootstrapAdapter, rawNonce[0:4])
    randomNonce = BigEndian.Uint32(rawNonce)
        Decrypts on-image bytes 0–3 with the bootstrap cipher to recover
        the per-encode random nonce.

4.  payloadCipher = cipher.NewCipher(randomNonce, encKey)
    payloadCM = CipherMiddleware(cur, payloadCipher)
    payloadCM.Seek(32, SeekStart)

5.  adapter = CursorAdapter(payloadCM)
    mac = hmac.New(sha256.New, macKey)
    padded = container.ReadPayload(adapter, mac)
        Reads and decrypts container-length, padded block, and HMAC tag;
        verifies HMAC. Returns error if tag does not match (wrong password
        or any tampering).

6.  return extractRealPayload(padded)
        Reads the 4-byte real-length prefix from padded, returns
        padded[4 : 4+realLen]. Padding is discarded.
```

---

## 12. Capacity

| Quantity | Formula |
|----------|---------|
| Total carrier bits | `W × H × channels × bitsPerChannel` |
| Total carrier bytes | `W × H × channels × bitsPerChannel / 8` |
| Overhead | 44 bytes: 4 (enc nonce) + 4 (container length) + 4 (real-length prefix) + 32 (HMAC) |
| Maximum real payload | `max(0, floor(W × H × channels × bitsPerChannel / 8) − 44)` bytes |

Example capacities at the default CLI settings (3 channels, 1 bit/channel):

| Image | Pixels | Max payload |
|-------|--------|-------------|
| 100 × 100 | 10,000 | ~3,706 bytes (~3.6 KB) |
| 500 × 500 | 250,000 | ~93,706 bytes (~91.5 KB) |
| 1920 × 1080 | 2,073,600 | ~777,556 bytes (~759 KB) |
| 3840 × 2160 | 8,294,400 | ~3,110,356 bytes (~2.97 MB) |

The `steg capacity -i <image>` command prints a 3×4 table covering all channel and
bits-per-channel combinations for a given image.

---

## 13. Design Decisions

### Password-derived pixel order

Using the password-derived seed as the RNG seed means an attacker who does not know the password cannot locate which pixels carry data. This is security-through-obscurity and is not a cryptographic guarantee, but it does raise the practical bar: even if AES were broken, the attacker would need to brute-force the seed before they could read anything.

### Encrypted nonce — two-cipher bootstrap scheme

The per-encode nonce must be recoverable at decode time without out-of-band communication.
Writing it in plaintext (earlier design) created a fixed-position anchor detectable by
statistical analysis, and was replaced by the following scheme:

1. A KDF-derived `kdfNonce` (bytes 56–59 of Argon2id output) is fixed per password.
2. A `crypto/rand` `randomNonce` is generated on every encode.
3. `randomNonce` is encrypted with `AES-CTR(encKey, kdfNonce)` and written to image
   bytes 0–3. This looks identical to any other ciphertext on the image.
4. The payload cipher uses `AES-CTR(encKey, randomNonce)` — unique per encode.

On decode, the bootstrap cipher (`kdfNonce`) decrypts bytes 0–3 to recover `randomNonce`,
then the payload cipher is reconstructed from it.

This design provides:
- **No plaintext on the image** — even the nonce is encrypted.
- **Per-encode keystream uniqueness** — `randomNonce` differs every time, so two encodes
  with the same password never share a keystream.
- **No stego marker** — the 4 encrypted nonce bytes are indistinguishable from the rest
  of the ciphertext.

The bootstrap cipher's keystream bytes 0–3 are fixed per password, but they always
encrypt a different `crypto/rand` value, so the ciphertext changes on every encode.

A consequence is that encoding the same payload into the same carrier twice produces two
different output images (nonces differ), which is the correct behavior.

### Four-way key split from a single Argon2id call

Argon2id is memory-hard and slow by design, so calling it once per derived value would
multiply the key-derivation cost for the legitimate user. Producing 60 bytes in a single
call and slicing them into seed, encryption key, MAC key, and bootstrap nonce is standard
practice and does not weaken the derivation.

### HMAC-SHA256 as the authentication tag

HMAC-SHA256 is keyed with `macKey`, which is independent of `encKey`. Because the tag is keyed, an attacker who does not know the password cannot forge a valid tag. The tag is written through the cipher middleware, so it is encrypted on the carrier alongside the payload.

The tag is computed over the **plaintext** payload (MAC-then-Encrypt rather than Encrypt-then-MAC). This is unconventional: the standard recommendation is Encrypt-then-MAC because a MAC over ciphertext lets the verifier reject tampered messages before decryption. Here the tag is inside the encrypted channel, so an attacker cannot isolate and modify only the ciphertext without corrupting the tag position as well. The practical security is similar, but the ordering adds implementation complexity.

### Fixed application salt for Argon2id

A per-image random salt would marginally improve resistance to targeted precomputation (rainbow tables built for a specific candidate password). However, storing a random salt requires reading it before the cipher can be initialized — the same bootstrapping problem solved for the nonce. The current design places the 4-byte nonce first; adding a salt would require extending this plaintext header. The fixed salt `"github.com/pableeee/steg/v1"` serves as a domain separator: keys derived for this application cannot be reused against other Argon2id deployments using the same password.

### Configurable channels and bits-per-channel

The `--channels` flag (1=R, 2=R+G, 3=R+G+B, default 3) and `--bits-per-channel` flag (1–8, default 1) give the user direct control over the capacity vs. detectability trade-off. Higher values increase the payload capacity but also increase the statistical deviation from a natural image's channel distribution, making the steganogram easier to detect.

The `steg capacity` and `steg test-visual` commands let the user explore this trade-off empirically before committing to an encoding configuration.

### `math/rand` for the pixel shuffle

The Fisher-Yates shuffle uses Go's `math/rand` (a pseudo-random number generator), not `crypto/rand`. This is intentional: the shuffle must be deterministic and reproducible from the seed, which `crypto/rand` cannot provide. The security of the system does not depend on the shuffle being unpredictable to a computationally-unbounded adversary; it depends on the adversary not knowing the seed, which is protected by Argon2id.

---

## 14. Known Limitations

| Issue | Severity | Notes |
|-------|----------|-------|
| MAC-then-Encrypt ordering | Low | HMAC is computed over plaintext before encryption. Unconventional (Encrypt-then-MAC is preferred), but not exploitable in this threat model since the tag is inside the encrypted channel. |
| Fixed application salt | Low | A per-image random salt would be marginally stronger; not implemented to avoid header bootstrapping complexity. |
| No streaming decode | Medium | `ReadPayload` allocates the full payload in memory before returning it. Very large payloads may cause high memory usage. |
| Lossy formats unsupported | High | JPEG and other lossy formats destroy LSB data. Only lossless formats (PNG, BMP, TIFF) are supported. |
| Bootstrap cipher keystream reuse | Info | The bootstrap cipher uses `(encKey, kdfNonce)` — fixed per password — consuming keystream bytes 0–3 on every encode. Since the plaintext (crypto/rand) is different each time, the ciphertext differs; no information about the nonce or key is exposed. |
| Nonce tampering | Low | Flipping bits in the encrypted nonce region (bytes 0–3) corrupts the recovered `randomNonce`, causing the payload cipher to differ from encode, which cascades to an HMAC failure. The correct outcome — tampering is detected — but the error message does not distinguish nonce tampering from a wrong password. |
| Statistical steganalysis | Low (mitigated) | Every encode writes the full image capacity regardless of payload size, making payload-size estimation from statistical analysis infeasible. The built-in `steg detect` command implements chi-square and RS analysis to surface residual LSB signatures. Higher `--bits-per-channel` settings increase the statistical deviation. |
