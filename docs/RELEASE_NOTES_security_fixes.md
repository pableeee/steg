# Release Notes — Security Fixes (`fix/security`)

**Date:** 2026-02-20
**Branch:** `fix/security`
**Base commit:** `cb42f1b`

---

## Overview

This release addresses five security issues identified during code review, plus one pre-existing bug in the cursor adapter that was blocking the new encoding pipeline. The container format has changed in a **breaking** way; images encoded with previous versions cannot be decoded with this release.

---

## Breaking Change — New Container Format

The on-image payload layout has changed:

| Region | Size | Encryption | Description |
|--------|------|------------|-------------|
| Nonce | 4 bytes | Plaintext | Read from carrier image pixel LSBs before cipher init |
| Length | 4 bytes | AES-128 CTR | Payload length, uint32 little-endian |
| Payload | N bytes | AES-128 CTR | Encrypted message bytes |
| HMAC tag | 32 bytes | AES-128 CTR | HMAC-SHA256 over plaintext payload, keyed with `aesKey` |

**Old format:** `[4-byte length][ciphertext][MD5 checksum]`
**New format:** `[4-byte nonce][encrypted length][encrypted payload][encrypted HMAC-SHA256 tag]`

Images encoded with the old version will fail to decode with the new version (checksum mismatch or garbled length).

---

## Security Fixes

### [Critical] Issue 1 — Weak key derivation (MD5, no salt, no stretching)

**File:** `steg/steg.go`

**Before:**
The password was hashed with a single raw MD5 call to produce a 64-bit seed. MD5 is not a key derivation function: it has no salt, no work factor, and can be brute-forced at billions of guesses per second on commodity hardware.

```go
// old
hashFn := md5.New()
hashFn.Write(pass)
seedBytes := hashFn.Sum(nil)
```

**After:**
Replaced with **Argon2id**, the winner of the Password Hashing Competition and the current OWASP recommendation. A single Argon2id call produces 24 bytes from which both the RNG seed (first 8 bytes) and the AES key (next 16 bytes) are derived, ensuring the two values are cryptographically independent.

```go
// new
derived := argon2.IDKey(pass, appSalt, 1, 64*1024, 4, 24)
seed   = int64(binary.BigEndian.Uint64(derived[0:8]))
aesKey = derived[8:24]
```

Parameters follow the OWASP 2023 interactive-login profile: time cost = 1, memory = 64 MiB, parallelism = 4. A fixed domain-separator salt (`"github.com/pableeee/steg/v1"`) is used; Argon2id's memory-hardness provides the necessary work factor against brute-force even with a fixed salt.

---

### [Critical] Issue 2 — AES-CTR nonce hardcoded to zero

**Files:** `steg/encode.go`, `steg/decode.go`

**Before:**
The nonce passed to `cipher.NewCipher` was always `0`. Reusing a (key, nonce) pair with CTR mode produces identical keystream bytes across every encoded image, enabling trivial plaintext recovery by XOR-ing two ciphertexts.

```go
// old
cm := cursors.CipherMiddleware(cur, cipher.NewCipher(0, pass))
```

**After:**
The nonce is derived from the carrier image itself: before the cipher is initialised, 4 bytes are read in plaintext from the first pixel positions in the pseudorandom traversal order. Because the pixel LSBs of the carrier vary per image, different carrier images produce different nonces under the same password. The same 4 bytes are re-read identically during decode to reconstruct the nonce.

```go
// new — encode & decode
rawAdapter := cursors.CursorAdapter(cur)
nonceBuf   := make([]byte, 4)
io.ReadFull(rawAdapter, nonceBuf)
nonce := binary.BigEndian.Uint32(nonceBuf)

c, _ := cipher.NewCipher(nonce, aesKey)
cm   := cursors.CipherMiddleware(cur, c)
cm.Seek(32, io.SeekStart)   // sync cipher and cursor past the nonce bytes
```

> **Note:** same password + same carrier image encodes to the same nonce. Embedding different payloads in the same image with the same password is therefore a theoretical (key, nonce) reuse risk. For the steganography use-case — where the carrier is typically replaced or varied — this is an acceptable trade-off.

---

### [Medium] Issue 3 — Broken PKCS#7 padding in cipher key setup

**File:** `cipher/cipher.go`

**Before:**
`NewCipher` attempted to PKCS#7-pad the raw password to 16 bytes to satisfy `aes.NewCipher`. This was broken in two ways: the padding implementation did not strip padding on the decryption path, and padding a raw password is not a substitute for a proper KDF.

```go
// old
func NewCipher(nonce uint32, pass []byte, ...) *streamCipherImpl {
    pass, _ = pkcs7Pad(pass, opts.blockSize)
    cb, _ := aes.NewCipher(pass)
    ...
}
```

**After:**
The `pkcs7Pad` function is deleted entirely. `NewCipher` now accepts a pre-derived 16-byte key (supplied by `deriveKeys`) and propagates the error from `aes.NewCipher` rather than discarding it. The function signature changes from returning `*streamCipherImpl` to `(*streamCipherImpl, error)`.

```go
// new
func NewCipher(nonce uint32, key []byte, ...) (*streamCipherImpl, error) {
    cb, err := aes.NewCipher(key)
    if err != nil {
        return nil, fmt.Errorf("cipher.NewCipher: %w", err)
    }
    ...
}
```

---

### [Medium] Issue 4 — Unauthenticated integrity check (unkeyed MD5)

**Files:** `steg/encode.go`, `steg/decode.go`, `steg/container/container.go`

**Before:**
The container used an unkeyed MD5 hash as its integrity tag. MD5 is not a MAC: an attacker who can modify the image can recompute a valid MD5 for any chosen ciphertext, making the check useless against active tampering. It also provided no authentication — the same tag is produced regardless of whether the correct password was used.

**After:**
Replaced with **HMAC-SHA256** keyed with `aesKey` (the Argon2id-derived AES key). Because the tag is keyed, an attacker without the password cannot forge a valid tag. The tag is 32 bytes. It is written through the cipher middleware so it is also encrypted on the carrier image.

```go
// new
mac := hmac.New(sha256.New, aesKey)
container.WritePayload(adapter, r, mac)   // encode
container.ReadPayload(adapter, mac)       // decode
```

The `container` package itself required no interface change — it already accepted a `hash.Hash`. Only the caller changed from `md5.New()` to `hmac.New(sha256.New, aesKey)`.

---

### [Low] Issue 5 — Default password `"YELLOW SUBMARINE"`

**File:** `cmd/steg/root.go`

**Before:**
Both `encode` and `decode` CLI flags registered `"YELLOW SUBMARINE"` as their default password, meaning a user who forgot to supply `-p` would silently encode with a publicly-known key.

```go
// old
encodeCmd.Flags().StringVarP(&encoderFlags.key, "password", "p", "YELLOW SUBMARINE", "...")
decodeCmd.Flags().StringVarP(&decoderFlags.key, "password", "p", "YELLOW SUBMARINE", "...")
```

**After:**
Default removed; both flags are now marked required via `MarkFlagRequired`. The CLI will exit with an error message if `-p` is omitted.

```go
// new
encodeCmd.Flags().StringVarP(&encoderFlags.key, "password", "p", "", "...")
encodeCmd.MarkFlagRequired("password")

decodeCmd.Flags().StringVarP(&decoderFlags.key, "password", "p", "", "...")
decodeCmd.MarkFlagRequired("password")
```

---

## Bug Fix — `cursors/adapter.go`: `Seek` returned bit position instead of byte position

**File:** `cursors/adapter.go`

**Before:**
`Seek` multiplied the incoming byte offset by 8 when delegating to the underlying bit-level cursor, but returned the raw bit position rather than dividing back to bytes. This violated the `io.ReadWriteSeeker` contract, which requires byte offsets, and caused `WritePayload`'s `Seek(0, SeekCurrent)` call to report a position 8× too large.

```go
// old
func (r *readWriteSeekerAdapter) Seek(offset int64, whence int) (int64, error) {
    return r.cur.Seek(offset*8, whence)
}
```

**After:**

```go
// new
func (r *readWriteSeekerAdapter) Seek(offset int64, whence int) (int64, error) {
    n, err := r.cur.Seek(offset*8, whence)
    return n / 8, err
}
```

---

## Dependency Changes

| Module | Before | After |
|--------|--------|-------|
| `golang.org/x/crypto` | indirect / `v0.0.0-20191011191535` | **direct** / `v0.48.0` |

`go.mod` and `go.sum` updated accordingly.

---

## Files Changed

| File | Change |
|------|--------|
| `go.mod` / `go.sum` | New direct dependency on `golang.org/x/crypto` |
| `cipher/cipher.go` | Removed `pkcs7Pad`; `NewCipher` now takes a key, returns error |
| `cipher/cipher_test.go` | Updated call sites for new `NewCipher` signature |
| `steg/steg.go` | Replaced MD5 seed derivation with Argon2id `deriveKeys` |
| `cursors/adapter.go` | Fixed `Seek` return value (`n / 8`) |
| `steg/container/container.go` | `WritePayload` uses `basePos`-relative seeks |
| `steg/encode.go` | New pipeline: carrier nonce, Argon2id key, HMAC-SHA256 |
| `steg/decode.go` | Mirror of new encode pipeline |
| `cmd/steg/root.go` | Removed default password; flags marked required |

---

## Known Limitations / Future Work

- **Fixed application salt:** A per-image random salt stored in plaintext on the carrier would be marginally stronger against targeted precomputation. This was not implemented to avoid a bootstrapping problem (the salt must be readable before cipher initialisation, similar to the nonce).
- **MAC-then-Encrypt:** The HMAC tag is computed over the plaintext and then written through the cipher. Encrypt-then-MAC would be more conventional but requires a more significant restructuring of the container format.
- **`bytesEqual` vs `hmac.Equal`:** The tag comparison in `container.go` uses a simple byte loop rather than `hmac.Equal` (constant-time). Given the tag is inside an encrypted channel this is not directly exploitable, but should be addressed in a follow-up.
