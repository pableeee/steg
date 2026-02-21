# Steg — Technical Specification

**Version:** v1 (post `fix/security`)
**Date:** 2026-02-21

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

Steg hides an arbitrary file inside a PNG image by modifying the least-significant bit (LSB) of selected color channels of a pseudorandom subset of pixels. The data is encrypted and authenticated before being written, so an observer who recovers the bits cannot read the payload without the password, and any tampering is detected at decode time.

The tool operates entirely on the local filesystem. Input and output are both PNG files. The carrier image is modified in-place in memory and re-encoded as a lossless PNG, so no visual quality is lost and no pixel value changes by more than 1 in the modified channels.

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
derived = Argon2id(password, appSalt, time=1, memory=64 MiB, threads=4, length=56)

seed   = int64( BigEndian(derived[0:8]) )   // RNG cursor seed
encKey = derived[8:24]                       // 16-byte AES-128 key
macKey = derived[24:56]                      // 32-byte HMAC-SHA256 key
```

**Parameters:**

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| Algorithm | Argon2id | Resists GPU and side-channel attacks; OWASP 2023 recommendation |
| Time cost | 1 | OWASP interactive-login profile |
| Memory | 64 MiB | Memory-hardness as the primary brute-force deterrent |
| Parallelism | 4 | Matches common core counts; increases attacker cost |
| Salt | `"github.com/pableeee/steg/v1"` (fixed) | Domain separator; memory-hardness compensates for the lack of randomness |
| Output length | 56 bytes | 8 (seed) + 16 (AES key) + 32 (MAC key) |

The seed, encryption key, and MAC key are derived from the same password in a single pass, so they are cryptographically independent — learning one gives no information about the others.

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

The cursor supports three 1-bit channels per pixel: red (bit 0x1), green (bit 0x2), and blue (bit 0x4). The encode/decode orchestration selects **green and blue** via `UseGreenBit()` and `UseBlueBit()`. Red is defined in the interface but not used.

With two channels per pixel, the bit cursor maps to image coordinates as:

```
pixelIndex   = cursorPosition / 2
channelIndex = cursorPosition % 2     (0 → green, 1 → blue)
```

### Bit read/write

Only the LSB (bit 0) of the selected channel's 8-bit value is read or written. The remaining 7 bits of each channel are untouched, limiting any single pixel's maximum observable change to ±1 per modified channel.

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

`Seek` computes the target block counter from the requested bit position and refreshes the block if necessary. This allows the orchestration layer to skip over the plaintext nonce bytes by seeking the cipher to bit 32 before beginning encryption, keeping the cipher position synchronized with the cursor position.

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
Bit offset    Size        Encryption   Content
──────────────────────────────────────────────────────────────────────
0             32 bits     Plaintext    Nonce (uint32, big-endian)
32            32 bits     AES-128-CTR  Payload length (uint32, little-endian)
64            N×8 bits    AES-128-CTR  Payload bytes
64 + N×8      256 bits    AES-128-CTR  HMAC-SHA256 tag (32 bytes)
```

**Total bits used:** `32 + 32 + N×8 + 256 = 320 + N×8`

The nonce region is written before the cipher is initialized and read back before the cipher is initialized during decode. The cipher is then seeked past bit 32, so that keystream bit 0 corresponds to on-image bit 32 (the first bit of the encrypted length field). The cipher and cursor positions remain synchronized throughout.

---

## 10. Encode Flow

```
steg.Encode(img draw.Image, pass []byte, payload io.Reader)

1.  deriveKeys(pass) → seed, encKey, macKey

2.  cur = NewRNGCursor(img, UseGreenBit(), UseBlueBit(), WithSeed(seed))
        Generates Fisher-Yates-shuffled pixel sequence.

3.  nonceBuf = crypto/rand.Read(4)
    nonce = BigEndian.Uint32(nonceBuf)

4.  rawAdapter = CursorAdapter(cur)
    rawAdapter.Write(nonceBuf)
        Writes 4 bytes (32 bits) of nonce into the first pixel positions
        of the shuffled sequence, in plaintext.

5.  c = cipher.NewCipher(nonce, encKey)
    cm = CipherMiddleware(cur, c)
    cm.Seek(32, SeekStart)
        Advances both cipher and cursor past the 32 nonce bits so
        keystream position 0 aligns with on-image bit 32.

6.  adapter = CursorAdapter(cm)
    mac = hmac.New(sha256.New, macKey)
    container.WritePayload(adapter, payload, mac)
        Writes: [encrypted length][encrypted payload][encrypted HMAC tag]

7.  png.Encode(outputFile, img)
```

---

## 11. Decode Flow

```
steg.Decode(img draw.Image, pass []byte) → []byte

1.  deriveKeys(pass) → seed, encKey, macKey

2.  cur = NewRNGCursor(img, UseGreenBit(), UseBlueBit(), WithSeed(seed))
        Reproduces the identical shuffled pixel sequence.

3.  rawAdapter = CursorAdapter(cur)
    io.ReadFull(rawAdapter, nonceBuf[0:4])
    nonce = BigEndian.Uint32(nonceBuf)
        Reads the 4 plaintext nonce bytes from the same first pixel positions.

4.  c = cipher.NewCipher(nonce, encKey)
    cm = CipherMiddleware(cur, c)
    cm.Seek(32, SeekStart)

5.  adapter = CursorAdapter(cm)
    mac = hmac.New(sha256.New, macKey)
    container.ReadPayload(adapter, mac)
        Reads and decrypts length, payload, and tag; verifies HMAC.
        Returns error if tag does not match (wrong password or tampering).
```

---

## 12. Capacity

| Quantity | Formula |
|----------|---------|
| Total carrier bits | `W × H × 2` |
| Overhead (nonce + length + tag) | `32 + 32 + 256 = 320 bits = 40 bytes` |
| Maximum payload | `floor((W × H × 2 − 320) / 8)` bytes |

Example capacities:

| Image | Pixels | Max payload |
|-------|--------|-------------|
| 100 × 100 | 10,000 | ~2,460 bytes (~2.4 KB) |
| 500 × 500 | 250,000 | ~62,460 bytes (~61 KB) |
| 1920 × 1080 | 2,073,600 | ~518,360 bytes (~506 KB) |
| 3840 × 2160 | 8,294,400 | ~2,073,520 bytes (~1.98 MB) |

---

## 13. Design Decisions

### Password-derived pixel order

Using the password-derived seed as the RNG seed means an attacker who does not know the password cannot locate which pixels carry data. This is security-through-obscurity and is not a cryptographic guarantee, but it does raise the practical bar: even if AES were broken, the attacker would need to brute-force the seed before they could read anything.

### Nonce from `crypto/rand`, stored in plaintext

The nonce must be recoverable at decode time without out-of-band communication. Storing it in plaintext in the first 32 bits of the pixel sequence is the simplest solution. Because `crypto/rand` is used, each encode operation produces a unique nonce even when the same carrier and password are reused, preventing keystream reuse.

A consequence is that encoding the same payload into the same carrier twice produces two different output images (nonces differ), which is the correct behavior.

### Three-way key split from a single Argon2id call

Argon2id is memory-hard and slow by design, so calling it three times (once per derived value) would triple the key-derivation cost for the legitimate user. Producing 56 bytes in a single call and slicing them into seed, encryption key, and MAC key is standard practice and does not weaken the derivation.

### HMAC-SHA256 as the authentication tag

HMAC-SHA256 is keyed with `macKey`, which is independent of `encKey`. Because the tag is keyed, an attacker who does not know the password cannot forge a valid tag. The tag is written through the cipher middleware, so it is encrypted on the carrier alongside the payload.

The tag is computed over the **plaintext** payload (MAC-then-Encrypt rather than Encrypt-then-MAC). This is unconventional: the standard recommendation is Encrypt-then-MAC because a MAC over ciphertext lets the verifier reject tampered messages before decryption. Here the tag is inside the encrypted channel, so an attacker cannot isolate and modify only the ciphertext without corrupting the tag position as well. The practical security is similar, but the ordering adds implementation complexity.

### Fixed application salt for Argon2id

A per-image random salt would marginally improve resistance to targeted precomputation (rainbow tables built for a specific candidate password). However, storing a random salt requires reading it before the cipher can be initialized — the same bootstrapping problem solved for the nonce. The current design places the 4-byte nonce first; adding a salt would require extending this plaintext header. The fixed salt `"github.com/pableeee/steg/v1"` serves as a domain separator: keys derived for this application cannot be reused against other Argon2id deployments using the same password.

### Green and blue channels only

Red, green, and blue channels are defined in the cursor interface, but the orchestration layer activates only green and blue. This is an arbitrary choice that can be changed by passing different `Option` values to `NewRNGCursor`. Using all three channels would increase capacity by 50% at the cost of making single-channel modifications visible in the red channel as well.

### `math/rand` for the pixel shuffle

The Fisher-Yates shuffle uses Go's `math/rand` (a pseudo-random number generator), not `crypto/rand`. This is intentional: the shuffle must be deterministic and reproducible from the seed, which `crypto/rand` cannot provide. The security of the system does not depend on the shuffle being unpredictable to a computationally-unbounded adversary; it depends on the adversary not knowing the seed, which is protected by Argon2id.

---

## 14. Known Limitations

| Issue | Severity | Notes |
|-------|----------|-------|
| Non-constant-time MAC comparison | Low | `container.go` uses a byte-loop comparison instead of `hmac.Equal`. The tag is inside an encrypted channel, so timing information is not directly observable by a network attacker, but this should be fixed. |
| MAC-then-Encrypt ordering | Low | See §13. Unconventional but not exploitable in this threat model. |
| Fixed application salt | Low | A per-image random salt would be marginally stronger; not implemented to avoid header bootstrapping complexity. |
| No streaming decode | Medium | `ReadPayload` allocates the full payload in memory before returning it. Very large payloads may cause high memory usage. |
| PNG only | Medium | Only lossless PNG is supported. JPEG is lossy and would destroy the LSB data; other lossless formats (BMP, PNG variants) could be added. |
| No integrity check on the nonce | Low | The 4-byte nonce is stored in plaintext without any MAC. An active attacker who can flip bits in the nonce region would change the keystream used for decryption, causing a MAC failure rather than silent data corruption — the correct outcome — but the failure message does not distinguish nonce tampering from key error. |
