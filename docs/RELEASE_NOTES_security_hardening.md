# Release Notes — Security Hardening

**Date:** 2026-03-07
**Tags:** `v20260307-security`, `v20260307-security2`
**Commits:** `6dfc391`, `a2779f9`

---

## Overview

Two security issues identified in the post-`fix/security` codebase are addressed in this
release. Together they eliminate the only remaining plaintext region on the encoded image,
ensure every encode uses a unique keystream, and remove the payload-size signal from
statistical analysis.

This is a **breaking format change** — images encoded with previous versions cannot be
decoded with this release.

---

## Fix 1 — Full-capacity padding (commit `6dfc391`)

### Problem

For small payloads the Fisher-Yates shuffle only modifies a fraction of the image's
pixels. Statistical detectors (chi-square, RS analysis) can observe a partial LSB
disturbance, giving an adversary a signal about how much data was embedded. Near-empty
images look clean; partially-filled images produce intermediate statistics.

### Fix

Every encode now writes the **full image capacity** regardless of payload size. The
steg layer prepends a 4-byte real-length prefix to the payload, then appends random
padding (`crypto/rand`) to fill the remaining capacity. The container sees a fixed-size
block every time.

```
padded_block = [4B real-length LE] [real-payload] [crypto/rand padding]
             = imageCapacity + 4 bytes total
```

On decode, `extractRealPayload` reads the 4-byte prefix and returns only the original
bytes. The padding is silently discarded.

**Effect:** every encoded image has a fully and uniformly disturbed LSB distribution
regardless of payload size. Payload-size estimation from statistical analysis is no
longer possible.

---

## Fix 2 — Plaintext nonce eliminated (commit `6dfc391`)

### Problem

The previous scheme wrote a `crypto/rand` 4-byte nonce as plaintext to the first 4 cursor
positions (pixel positions determined by the Fisher-Yates shuffle seeded from the
password). An attacker testing a candidate password could:

1. Derive the seed → find the 4 pixel positions
2. Check whether those LSBs look like `crypto/rand` output
3. Use that as a cheap confirmation oracle before HMAC verification

The nonce region was also the one fixed-position plaintext anchor distinguishable from
the rest of the encrypted payload.

### Fix (initial — later superseded by Fix 3)

The nonce was derived from the Argon2id output (bytes 56–59) instead of `crypto/rand`.
No bytes were written to the image before the cipher started. The cipher started at
bit 0 with no seek required.

**Trade-off introduced:** the nonce became deterministic per password. Two encodes with
the same password always produced the same keystream — a **critical two-time pad
vulnerability** (see Fix 3).

---

## Fix 3 — Two-time pad vulnerability fixed (commit `a2779f9`)

### Problem

With Fix 2 in place, the same password always produced `(encKey, kdfNonce)` — identical
inputs to the cipher for every encode. An adversary who obtained two carrier images
encoded with the same password could XOR their LSB planes to cancel the keystream:

```
C1 = P1 ⊕ keystream
C2 = P2 ⊕ keystream
C1 ⊕ C2 = P1 ⊕ P2
```

From `P1 ⊕ P2`, standard crib-dragging or known-plaintext attacks can recover both
messages. This is a critical vulnerability.

### Fix

A **two-cipher bootstrap scheme** restores per-encode randomness without writing any
plaintext to the image:

```
crypto/rand → randomNonce (4 bytes)
    │
    ▼  bootstrap cipher: AES-CTR(encKey, kdfNonce)
image bytes 0–3: encrypt(randomNonce)        ← ciphertext, not plaintext
    │
    ▼  payload cipher: AES-CTR(encKey, randomNonce)
image bytes 4+:  encrypt(container-length | real-length | real-payload | padding | HMAC)
```

- The **bootstrap cipher** uses `(encKey, kdfNonce)` — deterministic per password — to
  encrypt only the 4-byte random nonce. Its keystream bytes 0–3 are fixed per password,
  but since they always XOR a different `crypto/rand` value the ciphertext changes every
  encode.
- The **payload cipher** uses `(encKey, randomNonce)` — different on every encode —
  ensuring a unique keystream for all actual data.
- No plaintext bytes appear on the image at any position.

On decode, the bootstrap cipher decrypts bytes 0–3 to recover `randomNonce`, then the
payload cipher is initialized from it to decrypt the rest.

**Security properties restored:**
- Unique keystream per encode (even with the same password and carrier)
- No plaintext anchors on the image
- No stego presence marker from fixed-position uniform-random bytes

---

## New on-image format

```
Bit offset           Size        Cipher                  Field
────────────────────────────────────────────────────────────────────────────────
0                    32 bits     AES-128-CTR (bootstrap) Per-encode random nonce
32                   32 bits     AES-128-CTR (payload)   Container length (uint32, LE)
64                   32 bits     AES-128-CTR (payload)   Real payload length (uint32, LE)
96                   N×8 bits    AES-128-CTR (payload)   Real payload bytes
96 + N×8             P×8 bits    AES-128-CTR (payload)   Random padding
96 + (N+P)×8         256 bits    AES-128-CTR (payload)   HMAC-SHA256 tag
```

---

## Capacity change

Overhead increases from **40 bytes** (old) to **44 bytes** (new).

| Component | Old | New |
|---|---|---|
| Plaintext/encrypted nonce | 4 B (plaintext) | 4 B (encrypted, bootstrap cipher) |
| Container length field | 4 B | 4 B |
| Embedded real-length prefix | — | 4 B (new) |
| HMAC-SHA256 tag | 32 B | 32 B |
| **Total overhead** | **40 B** | **44 B** |

Maximum real payload = `floor(W × H × channels × bitsPerChannel / 8) − 44` bytes.

---

## KDF output length change

`deriveKeys` now produces **60 bytes** (was 56):

| Bytes | Field |
|---|---|
| 0–7 | RNG seed (int64) |
| 8–23 | AES-128 encryption key (encKey) |
| 24–55 | HMAC-SHA256 MAC key (macKey) |
| 56–59 | Bootstrap cipher nonce (kdfNonce) |

---

## Files changed

| File | Change |
|---|---|
| `steg/steg.go` | `deriveKeys` → 60 bytes, returns `kdfNonce`; new `imageCapacityBytes` (overhead=44), `buildPaddedPayload`, `extractRealPayload` |
| `steg/encode.go` | Two-cipher encode; full-capacity padding |
| `steg/decode.go` | Two-cipher decode; real-payload extraction |
| `steg/parallel.go` | Same scheme in parallel path; offsets +4 throughout |
| `steg/analysis/analysis_test.go` | Capacity helper updated to overhead=44 |
| `cmd/steg/root.go` | `imageCapacity` overhead 40→44; output text updated |
| `README.md` | On-image layout, security design table, known limitations |
| `CLAUDE.md` | On-image layout, pipeline diagram, removed pending-security section |
| `docs/TECHNICAL_SPEC.md` | Full update to reflect new KDF, layout, encode/decode flows, capacity |
