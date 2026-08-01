# Attack Analysis: steg Cryptographic Security

This document catalogues known attack surfaces against the `steg` tool's
steganography and encryption scheme, framed as cryptopals-style exercises.
Each section describes the theoretical basis, the exploit path, expected
difficulty, and a results section to be filled in after implementation.

---

## Background: Cryptographic Design

```
Password
    │
    ▼  Argon2id(pass, FIXED_SALT="github.com/pableeee/steg/v1", t=1, m=64MiB)
    │    → bsSeed   (8B)  — Fisher-Yates pixel traversal seed
    │    → bsEncKey (16B) — bootstrap AES-128 key
    │    → bsNonce  (4B)  — bootstrap CTR nonce   ← CONSTANT PER PASSWORD
    │
    ▼  crypto/rand → randomSalt (16B)
    │    → encrypted with bootstrap cipher (key=bsEncKey, nonce=bsNonce)
    │    → written to image LSB bits 0–127
    │
    ▼  Argon2id(pass, randomSalt, t=1, m=64MiB)
         → encKey       (16B) — payload AES-128-CTR key
         → macKey       (32B) — HMAC-SHA256 key
         → payloadNonce (4B)  — unique per encode (randomSalt drives this)
```

On-image binary layout (in Fisher-Yates pixel bit order):

```
Bits        Size     Cipher              Field
──────────────────────────────────────────────────────────
0–127       16 B     AES-CTR bootstrap   enc(randomSalt)
128–159      4 B     AES-CTR payload     container length (LE uint32)
160–191      4 B     AES-CTR payload     real payload length (LE uint32)
192–…        N B     AES-CTR payload     real payload bytes
…–…          P B     AES-CTR payload     random padding (fills capacity)
…–(…+256)   32 B     AES-CTR payload     HMAC-SHA256 tag
```

---

## Attack 1: Steganalysis (Chi-Square Test)

### Theory

Natural images have uneven distributions of pixel LSB values: adjacent grey
levels `(2k, 2k+1)` appear with different frequencies because the image
content is correlated. LSB steganography replaces those bits with (pseudo-)
random cipher output, which equalises the pair frequencies.

The chi-square test measures this equalisation:

```
χ² = Σ (observed_i − expected_i)² / expected_i
```

A high p-value (> 0.05) suggests the LSBs have been homogenised — a
steganalysis signal detectable **without knowing the password**.

### Exploit Path

1. Load the suspect image.
2. For each colour channel (R, G, B), collect all pixel values.
3. Build a histogram of pair counts: `count[2k]` and `count[2k+1]` for k = 0..127.
4. Chi-square statistic over the 128 pairs.
5. Convert to a p-value; threshold at 0.05.

No password, no pixel-order knowledge required. Works on the whole image.

### Notes

- The Fisher-Yates pixel ordering does **not** prevent steganalysis because
  the statistical properties of the overall LSB set are unchanged regardless
  of traversal order.
- Random padding (fills the entire image capacity) amplifies the signal: even
  a 1-byte payload causes all remaining pixels to receive uniformly random
  LSBs.
- The `steg detect` command already implements this; re-implementing it
  independently confirms the design.

### Results

| Image | Channels | Bits/ch | Chi-square p (R/G/B) | RS asym (R/G/B) | Verdict |
|-------|----------|---------|---------------------|----------------|---------|
| `test/dude.png` (clean) | 3 | 1 | 0.00 / 0.00 / 0.00 | -0.0045 / -0.0017 / -0.0055 | CLEAN (0/6) |
| `steg_a.png` (19B payload) | 3 | 1 | 0.03 / 0.29 / 1.00 | -0.04 / -0.04 / -0.04 | SUSPICIOUS (2/6 chi) |

**Observations:**
- Clean image: all chi-square p ≈ 0, all RS asymmetries negative and small — correct CLEAN verdict.
- Stego image: G and B chi-square p become suspicious because filling the whole image with random
  padding homogenises those channels even though only R was not directly modified (format conversion
  during encode/decode changed G/B statistics).
- RS asymmetry is strongly *negative* for stego images (Rm < Rnm), the opposite sign from the
  theoretical expectation. This is because the random padding fills the entire image capacity,
  randomising all LSBs uniformly — the RS test catches this as suspicious but with inverted sign.
  The built-in `steg detect` command uses a `> 0.01` threshold so it misses this; the attack
  implementation could be extended to also flag large *negative* asymmetries.

---

## Attack 2: Bootstrap Cipher Nonce Reuse (Two-Time Pad)

### Theory

The bootstrap cipher uses key `bsEncKey` and nonce `bsNonce`, both derived
from `Argon2id(pass, FIXED_SALT)`. For a given password these values are
**constant**. The bootstrap keystream `KS_bs` is therefore identical for
every encode with that password.

Each image's first 128 bits of LSB data hold:

```
C_i = randomSalt_i ⊕ KS_bs
```

XOR-ing two images encoded with the same password:

```
C_A ⊕ C_B = randomSalt_A ⊕ randomSalt_B
```

This is the **two-time pad** problem from Set 3, Challenge 19/20. The
plaintexts here are uniformly random salts (not ASCII), so letter-frequency
attacks do not apply directly — but the structural weakness enables a
chosen-plaintext attack (see Attack 3).

### Exploit Path (demonstrating the leak)

1. Encode two files `A` and `B` with the **same password** into two carrier
   images.
2. Extract the first 128 bits of LSB data from each image in pixel order
   (requires knowing `bsSeed`, which requires the password — acceptable
   since we own both images in this demo).
3. XOR the two 16-byte values: `C_A ⊕ C_B`.
4. Verify that `C_A ⊕ C_B = randomSalt_A ⊕ randomSalt_B` by independently
   computing each salt (instrument the encoder to emit them).

### Expected Outcome

The XOR of two encoded images' bootstrap regions equals the XOR of their
random salts — a direct consequence of keystream reuse.

### Notes

- The payload bytes (bits 128+) do **not** have this problem: each image uses
  unique main keys derived from its unique `randomSalt`.
- The fix is to generate the bootstrap nonce randomly (stored in the image
  before the encrypted salt) rather than deriving it from the fixed-salt KDF.

### Results

| Image pair | `C_A ⊕ C_B` (hex) | `salt_A ⊕ salt_B` (hex) | Match? |
|------------|-------------------|------------------------|--------|
| steg_a + steg_b (password "hunter2") | `b6ec52e9594b6f123b61b539f9e0237e` | `b6ec52e9594b6f123b61b539f9e0237e` | ✓ |

**Observations:**
- XOR identity confirmed — bootstrap keystream `KS_bs` cancels out exactly.
- `KS_bs` = `e920916134b0b976fc3b4a2857a43a99` — same value for every encode with password "hunter2".
- Re-deriving `randomSalt_B` from `KS_bs` via XOR succeeds, demonstrating the CPA shortcut.

---

## Attack 3: Bootstrap CPA — Recovering a Target Salt

### Theory

Extends Attack 2. If the attacker:

- Knows the password `P`, AND
- Can create a stego image with `P` while observing the internal `randomSalt`
  (grey-box / instrumented encoder),

then they can recover the bootstrap keystream:

```
KS_bs = C_own ⊕ randomSalt_own
```

and use it to decrypt any target image's salt:

```
randomSalt_target = C_target ⊕ KS_bs
```

With `randomSalt_target` and `P` they run `Argon2id(P, randomSalt_target)` to
derive the target's main keys and decrypt its payload — without running the
standard `Decode` path.

### Practical Relevance

If you already know `P`, you can call `steg decode` directly. This attack
matters in a grey-box auditing scenario: it demonstrates that the bootstrap
region leaks the keystream to anyone with encode access + instrumentation,
bypassing the two-Argon2id decode path.

### Exploit Path

1. Instrument `steg/encode.go` to log `randomSalt` after `rand.Read`.
2. Encode a file with password `P` → record `randomSalt_own` and the
   resulting image `I_own`.
3. Extract `C_own` (first 16 LSB bytes) from `I_own`.
4. Compute `KS_bs = C_own ⊕ randomSalt_own`.
5. Extract `C_target` from the target image `I_target` (same password `P`).
6. Compute `randomSalt_target = C_target ⊕ KS_bs`.
7. Derive main keys: `Argon2id(P, randomSalt_target)`.
8. Decrypt payload from `I_target` and verify HMAC.

### Results

| Step | Value | Notes |
|------|-------|-------|
| `randomSalt_A` | `b805d1d8522bd105f5b650e37bb1ec0d` | recovered via bootstrap decrypt of image A |
| `C_A` (first 16 LSB bytes) | `512540b9669b6873098d1acb2c15d694` | extracted from image A |
| `KS_bs` | `e920916134b0b976fc3b4a2857a43a99` | `C_A ⊕ randomSalt_A` |
| `C_target` | `e7c912503fd0076132ecaff2d5f5f5ea` | extracted from target image B |
| `randomSalt_target` | `0ee983310b60be17ced7e5da8251cf73` | `C_target ⊕ KS_bs` |
| Payload decrypted? | ✓ "another secret message\n" (23 bytes) | HMAC passed |

**Observations:**
- Salt recovery via XOR is instantaneous — no second Argon2id call needed to recover the salt.
- Still requires Argon2id(P, randomSalt_target) to get the main keys, so attack cost is one
  Argon2id instead of two — a 2× speedup for the key derivation phase.
- The main practical value: demonstrates the bootstrap keystream is a shared secret derivable
  by any party with encode access and the ability to observe their own randomSalt.

---

## Attack 4: Dictionary Attack with HMAC Oracle

### Theory

The HMAC tag (last 32 bytes of the payload cipher stream) acts as a
password-verification oracle. For each candidate password `P_c`:

1. `Argon2id(P_c, fixedSalt)` → bootstrap keys
2. Decrypt first 16 LSB bytes → `randomSalt_c`
3. `Argon2id(P_c, randomSalt_c)` → main keys
4. Decrypt container → attempt HMAC verification

If HMAC passes, `P_c` is the correct password. The oracle is constant-time
(`hmac.Equal`) so timing attacks are not applicable.

### Cost per Candidate

Each attempt requires **two** Argon2id calls (64 MiB each, `t=1`):

- ~100–200 ms per candidate on modern CPU hardware
- A 100k-word dictionary takes ~3–6 hours
- A 1M-word dictionary takes ~30–60 hours

This is the primary practical attack against `steg`-encrypted images when
the password is weak.

### Exploit Path

1. Collect a wordlist (e.g. `rockyou.txt`).
2. For each candidate:
   a. Run bootstrap KDF.
   b. Extract and decrypt `randomSalt` from the image.
   c. Run payload KDF.
   d. Decrypt container length + padded payload.
   e. Verify HMAC.
   f. On success: extract real payload length and return plaintext.
3. Report elapsed time per attempt and total.

### Notes

- Running Argon2id twice per candidate (vs. once in a simpler design) is an
  accidental defence: the two-stage KDF roughly doubles attacker cost.
- Raising `time` from 1 to 2–3 would further harden against this at modest
  legitimate-use cost (~200–400 ms per decode instead of ~100 ms).
- GPU acceleration of Argon2id is limited by the 64 MiB memory-per-instance
  requirement — this is the core defence.

### Results

| Wordlist | Candidates tried | Time/candidate | Total time | Found? | Password |
|----------|-----------------|----------------|-----------|--------|---------|
| 10-word custom list | 7 | ~559 ms | 3.9 s | ✓ | "hunter2" |

**Observations:**
- ~1.79 candidates/s on this machine — this is purely Argon2id bottlenecked (2 calls × ~280 ms each).
- The 64 MiB memory requirement limits GPU parallelism significantly.
- Recovered payload: "top secret message\n" (19 bytes) — HMAC verified.
- A 1M-word rockyou dictionary at this rate would take ~155 hours single-threaded.

---

## Attack 5: MAC-then-Encrypt Analysis

### Theory

`container.WritePayload` computes HMAC over **plaintext** bytes, then
encrypts both the data and the tag together (MAC-then-Encrypt, MtE):

```go
hashFn.Write(buf[:n])   // HMAC over plaintext
w.Write(buf[:n])        // write ciphertext (encrypted plaintext)
// ...
w.Write(checksum)       // write encrypted HMAC tag
```

Modern best practice (TLS 1.3, AEAD) is Encrypt-then-MAC (EtM). MtE is
known to enable padding oracle attacks under CBC mode.

### Why This Is Safe Here (But Still a Design Smell)

AES-CTR has no padding. There is no padding oracle. The HMAC-SHA256 tag
still provides strong integrity once decrypted: a wrong password produces
a random-looking tag that will fail `hmac.Equal` with overwhelming probability.

The risk would materialise if the cipher were ever swapped to CBC mode
without revisiting the MAC ordering.

### Exercise

Confirm by experiment that a 1-bit flip in the ciphertext:
1. Produces a 1-bit flip in the corresponding plaintext byte (CTR stream
   property), AND
2. Causes HMAC verification failure (integrity holds).

### Results

| Flip position | Plaintext changed? | HMAC fail? | Notes |
|--------------|--------------------|-----------|-------|
| | | | |

---

## Summary Table

| # | Attack | Requires Password | Practical? | Severity |
|---|--------|------------------|-----------|---------|
| 1 | Chi-square steganalysis | No | Yes | Medium — detects presence |
| 2 | Two-time pad leak (`C_A⊕C_B`) | Yes (to extract bits) | Demo only | Low — leaks salt XOR |
| 3 | Bootstrap CPA salt recovery | Yes + grey-box encode | Grey-box only | Medium — bypasses decode path |
| 4 | Dictionary / brute-force | No (finding it) | Yes, for weak passwords | High |
| 5 | MtE bit-flip confirmation | Yes (to decrypt) | No direct exploit | Informational |
