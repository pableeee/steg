# Attack Analysis: steg Cryptographic Security

This document catalogues known attack surfaces against the `steg` tool's
steganography and encryption scheme, framed as cryptopals-style exercises.
Each section gives the theory, the exploit path, and measured results. The
tools live in `cmd/attack1` (steganalysis) and `cmd/attack4` (dictionary
attack); both are built on the library's exported API, so they track the
current format instead of re-implementing it.

All measurements below were taken on 2026-11-11 with the current working tree,
on a 32-core Linux (WSL2) machine.

---

## Background: Cryptographic Design

```
(width, height, bitsPerPixel) → k = ceil(128 / bitsPerPixel)
    │
    ▼  NewPixelOrder: first k Fisher-Yates steps driven by
    │  ChaCha8(publicOrderKey)  → public salt pixels points[:k]
    │  publicOrderKey = SHA-256("github.com/pableeee/steg/v2 salt positions")
    │
    ▼  crypto/rand → randomSalt (16 B), written in PLAINTEXT to bits 0–127
    │
    ▼  Argon2id(pass, randomSalt, t=2, m=64 MiB, p=4, 84 B)
    │    → encKey   (16 B) — payload AES-128-CTR key
    │    → macKey   (32 B) — HMAC-SHA256 key
    │    → nonce    (4 B)  — payload CTR nonce
    │    → orderKey (32 B) — ChaCha8 key for the rest of the pixel order
    │
    ▼  FinishPixelOrder: Fisher-Yates steps k..N−1 driven by ChaCha8(orderKey)
    │
    ▼  AES-128-CTR(encKey, nonce) over stream bits 128..end
         container = [length][4 B real length | payload | random padding][HMAC]
```

On-image layout (stream bit `b` lives in pixel `order[b / bitsPerPixel]`):

```
Bits        Size     Cipher              Field
──────────────────────────────────────────────────────────
0–127       16 B     none (plaintext)    randomSalt
128–159      4 B     AES-CTR payload     container length (LE uint32)
160–191      4 B     AES-CTR payload     real payload length (LE uint32)
192–…        N B     AES-CTR payload     real payload bytes
…–…          P B     AES-CTR payload     random padding (to the bucket size)
…–(…+256)   32 B     AES-CTR payload     HMAC-SHA256 tag
                                         (stream ends; later pixels untouched)
```

The salt pixels depend only on the image size and encoding settings, so
anyone can read the salt. Everything after them depends on Argon2id output:
locating, decrypting or verifying any payload bit for a candidate password
costs one Argon2id call.

The padded block is `max(4096, nextPowerOfTwo(4 + payload))` bytes, or the
whole capacity when that bucket does not fit (`paddedSize` in `steg/steg.go`).
Only the stream up to the end of the tag is written, so a small payload
touches a keyed pseudorandom subset of the pixels and leaves the rest
untouched; a payload that falls back to full fill touches every used LSB. At
1 bit per channel every write is LSB matching (±1 in a random direction from a
`crypto/rand`-seeded generator); at 2–8 bits it is replacement. Neither change
affects the decoder.

Decoding reads the salt, runs Argon2id, finishes the order, and calls
`container.ReadPayload`, which rejects a container length larger than the
carrier can hold before reading the payload, then verifies the HMAC.

---

## Attack 1: Steganalysis (Chi-Square + RS)

### Theory

**Chi-square.** Natural images have uneven frequencies for adjacent grey
levels `(2k, 2k+1)` because image content is correlated. LSB replacement
overwrites those bits with cipher output, which equalises each pair. The test
computes

```
χ² = Σ (observed_i − expected_i)² / expected_i
```

over the 128 pairs of each channel. A high p-value (> 0.05) means the pairs are
suspiciously uniform.

**RS analysis** (Fridrich, Goljan, Du). Pixels are grouped in fours along a
row; each group is classified as regular (R) or singular (S) by whether the
positive flip (`x ^ 1`) or the negative flip (1↔2, 3↔4, …) on positions 0 and 2
raises its roughness. In a clean photograph `Rm ≈ Rnm`. LSB replacement pulls
`Rm` toward `Sm` while pushing `Rnm` up, so the asymmetry `Rm − Rnm` turns
**negative**. `steg/analysis` flags a channel when `Rm − Rnm < −0.02`.

An earlier version of `steg detect` flagged `Rm − Rnm > 0.01`, the wrong sign,
which never fired on images this tool produces; `cmd/attack1` carried its own
copy of that logic. Both now use `steg/analysis`, the single implementation.

### Exploit Path

1. Load the suspect image.
2. Run `analysis.Analyze`: chi-square and RS on R, G and B.
3. Report per-channel statistics and the verdict
   (`CLEAN` = 0/6 flags, `LIKELY_STEGO` = 6/6, otherwise `SUSPICIOUS`).

No password and no pixel-order knowledge required: both tests look at the
whole image. Against the previous encoder (replacement, full-capacity padding)
the pseudorandom order did not help, since every pixel's LSBs were randomised
regardless of payload size. Against the current one, both tests are built for
replacement and do not see LSB matching (see Results).

```sh
go run ./cmd/attack1 clean.png stego.png   # several images → comparison table
```

### Results

#### Previous encoder (LSB replacement, full-capacity padding)

Stego images encoded with `steg encode` defaults (3 channels, 1 bit/channel,
19-byte payload, password `hunter2`) before bounded padding and LSB matching.

| Image | Chi-square p (R/G/B) | RS asym (R/G/B) | Verdict |
|-------|---------------------|-----------------|---------|
| `dude.png` 1024×1536, clean | 0.0000 / 0.0000 / 0.0000 | −0.0045 / −0.0017 / −0.0055 | CLEAN (0/6) |
| `dude.png` stego | 0.6728 / 0.9193 / 1.0000 | −0.0424 / −0.0402 / −0.0416 | LIKELY_STEGO (6/6) |
| synthetic 512×512 gradient + noise, clean | 0.0000 / 0.0000 / 0.0000 | +0.0040 / +0.0076 / +0.0028 | CLEAN (0/6) |
| synthetic 512×512 stego | 0.7093 / 0.9901 / 0.9900 | −0.1816 / −0.1763 / −0.1753 | LIKELY_STEGO (6/6) |

**Observations:**
- RS separates clean from stego cleanly on `dude.png`: −0.002 to −0.006
  clean against −0.040 to −0.042 stego, either side of the −0.02 threshold.
- With randomised LSBs, each channel's chi-square p-value is roughly uniform
  on [0, 1], so a single channel can land below 0.05 by chance. A second
  encode of `dude.png` gave R p = 0.0760; a value below 0.05 would have dropped
  the verdict to SUSPICIOUS (5/6). RS does not have this problem.
- With that encoder detection was the primary weakness of the scheme, and not
  fixable by cryptography: the payload was safe, but its presence was evident.

#### Current encoder (bounded padding, LSB matching at 1 bit/channel)

`dude.png` (1024×1536, opaque RGB, capacity 589,768 bytes at 3 channels ×
1 bit). Changed counts are against the original; "channel bytes" counts R, G
and B values separately. The first two rows are the previous encoder on the
same image, for comparison.

| Encoder | Payload | Channel bytes changed | Pixels changed | Chi-square p (R/G/B) | RS asym (R/G/B) | Verdict |
|---------|---------|----------------------|----------------|---------------------|-----------------|---------|
| previous | 31 B text | 2,358,580 | 1,375,953 (87.5%) | 0.4973 / 0.2542 / 0.5285 | −0.0444 / −0.0445 / −0.0443 | LIKELY_STEGO |
| previous | 589,768 B (full) | 2,359,650 | 1,376,309 (87.5%) | 0.6226 / 0.1330 / 0.9523 | −0.0432 / −0.0426 / −0.0431 | LIKELY_STEGO |
| current | 31 B text | 16,652 | 9,724 (0.62%) | 0.0000 / 0.0000 / 0.0000 | −0.0047 / −0.0014 / −0.0057 | CLEAN |
| current | 100,000 B | 524,152 | 305,756 (19.4%) | 0.0000 / 0.0000 / 0.0000 | −0.0041 / −0.0017 / −0.0053 | CLEAN |
| current | 589,768 B (full) | 2,360,072 | 1,377,013 (87.6%) | 0.0000 / 0.0000 / 0.0000 | −0.0014 / +0.0014 / −0.0015 | CLEAN |
| current, `-P` | 589,768 B (full) | — | — | 0.0000 / 0.0000 / 0.0000 | −0.0003 / +0.0041 / −0.0012 | CLEAN |
| current, `-b 2` | 31 B text | 12,360 | 5,440 (0.35%) | 0.0000 / 0.0000 / 0.0000 | −0.0046 / −0.0020 / −0.0056 | CLEAN |

Clean original: chi-square p 0.0000 / 0.0000 / 0.0000, RS −0.0045 / −0.0017 /
−0.0055. On a synthetic 512×512 photo-like carrier with every slot filled,
replacement gave chi-square p 0.27 / 0.75 / 0.41 and RS ≈ −0.17 on every
channel (LIKELY_STEGO); matching gave p 0.0000 and RS ≈ 0.00 (CLEAN).

**Observations:**
- At 1 bit/channel no channel value changed by more than ±1. With `-b 2` some
  did, as expected from replacing 2 bits; that row is CLEAN only because
  bounded padding touched 0.35% of the pixels.
- Bounded padding is what shrinks the footprint of small payloads (87.5% →
  0.62% of pixels for 31 bytes). LSB matching is what keeps a full carrier
  CLEAN: the full-capacity row changes as many pixels as the previous encoder
  did, yet chi-square stays at p = 0.0000 and RS inside the clean range.
- RS asymmetry at full fill moves from the clean ≈ −0.004 toward 0 (and
  slightly positive on G). That is a measurable shift, just not in the
  direction or size the −0.02 threshold looks for; a detector calibrated on the
  clean image could notice it.
- These two detectors model LSB replacement. CLEAN means only that they found
  nothing. Modern steganalysis (histogram characteristic-function attacks,
  rich models such as SRM with a trained classifier) detects LSB matching at
  high embedding rates, especially in smooth regions; none of it is
  implemented here. Adaptive embedding (README "Next steps") is the planned
  answer, and `cmd/attack1` is meant to grow into the benchmark comparing
  embedding strategies.
- At 2–8 bits/channel the encoder still uses replacement, so a large payload
  there is flagged (`TestEncoderOutputReplacementAboveOneBit`).
  `TestEncoderOutputEvadesDetectors` guards the 1-bit result.
- Payload size is no longer fully hidden: the number of changed pixels grows
  with the power-of-two bucket, so an estimate of the embedding rate reveals
  the bucket roughly.

---

## Historical: Bootstrap Cipher (Attacks 2 and 3)

Before commit c69a626 the 16-byte salt was not stored in plaintext. It was
encrypted with a "bootstrap" AES-CTR cipher whose key and nonce came from
`Argon2id(pass, fixed application salt)`, so the bootstrap keystream was the
same for every image encoded with a given password. Two consequences:

- **Two-time pad (Attack 2):** XOR-ing the salt regions of two images with the
  same password gave `salt_A ⊕ salt_B`.
- **Salt recovery by CPA (Attack 3):** anyone who could encode with the
  password and observe their own salt recovered the keystream and could read
  any other image's salt without the first Argon2id call.

Neither leaked payload data (each image's payload keys came from its own
salt), and a decoder needed two Argon2id calls. Commit c69a626 removed the
bootstrap cipher: the salt is stored in plaintext (it is not secret) and a
single Argon2id over the per-image salt derives all keys. The `cmd/attack2`
and `cmd/attack3` tools attacked a component that no longer exists and were
removed; they remain in git history.

## Historical: Password-Derived Traversal Order

The pixel order used to come from `SHA-256(password)` seeding Go's `math/rand`
(v1). That generator reduces its seed modulo 2^31 − 1, so there were only about
2^31 possible orders, and computing the order for a candidate password cost one
SHA-256, not an Argon2id call.

This was harmless while the encoder filled the whole carrier: every pixel
carried data, so knowing the order told an attacker nothing about which
pixels to look at. It would have broken bounded padding, now in place, where
a cheap order would let an attacker test candidate orders against the
statistics of the pixels they select.

Fixed in commit 4fd29ba (format v2): only the salt pixels use
a public order; the order of all later pixels is a ChaCha8-driven Fisher-Yates
shuffle keyed by 32 bytes of Argon2id output (`cursors/order.go`). The legacy
`cursors.WithSeed` option and its `math/rand` code have been removed, and no
production code imports `math/rand` v1.

---

## Attack 4: Dictionary Attack with HMAC Oracle

### Theory

The HMAC tag is a password-verification oracle. For each candidate password
`P_c`, a full decode attempt succeeds only if `P_c` is correct. `cmd/attack4`
calls `steg.Decode` on a fresh copy of the carrier per candidate, so it does
exactly what the real decoder does and cannot drift from the format. The tag is
compared with `hmac.Equal`, so timing attacks do not apply.

### Cost per Candidate

1. Read the salt from the public salt pixels. This needs no password, so an
   attacker gains nothing by doing it once outside the loop: it is negligible
   next to step 2.
2. **One** `Argon2id(P_c, salt, t=2, m=64 MiB, p=4)`.
3. Build the secret part of the pixel order (Fisher-Yates over all remaining
   pixels).
4. Decrypt the 4-byte container length. For a wrong candidate this is an
   essentially random uint32; anything above the carrier's capacity is rejected
   before the payload is read or the HMAC computed. The chance that a random
   length passes is about capacity / 2^32 (≈ 1.4 × 10⁻⁴ for a 1024×1536
   carrier), so almost every wrong candidate stops here.

Measured on this machine (throwaway benchmark): Argon2id ≈ 16 ms per call;
building the order ≈ 2 ms for 512×512 and ≈ 20 ms for 1024×1536. In 200 wrong
passwords against a 128×128 stego image, all 200 were rejected by the length
bound and none reached the HMAC.

The attacker must also know the encoding settings (`-c`, `-b`): they decide
the salt pixels and the order. With the wrong settings the correct password
is not found (checked: `attack4 -c 1` on a 3-channel image reports not found).
There are at most 3 × 8 = 24 settings, so not knowing them multiplies the cost
by at most 24.

### Exploit Path

```sh
go run ./cmd/attack4 [-c 3] [-b 1] stego.png wordlist.txt
```

For each candidate: copy the carrier, call `steg.Decode`, report success on
the first candidate that decodes. Prints candidates/s, time per candidate and
total.

### Results

Carriers encoded with `steg encode -p hunter2` (defaults: 3 channels,
1 bit/channel), 19-byte payload. "hit" wordlist: 7 entries with `hunter2` sixth;
"miss" wordlist: 10 wrong entries, including `Hunter2` and `hunter3`.
`attack4` runs one candidate at a time.

| Image | Wordlist | Candidates tried | Time/candidate | Total | Found? |
|-------|----------|-----------------|----------------|-------|--------|
| synthetic 512×512 | hit | 6 | 32 ms | 191 ms | yes, `hunter2`, payload recovered |
| synthetic 512×512 | miss | 10 | 29 ms | 292 ms | no |
| `dude.png` 1024×1536 | hit | 6 | 96 ms | 574 ms | yes, `hunter2`, payload recovered |
| `dude.png` 1024×1536 | miss | 10 | 68 ms | 675 ms | no |

**Observations:**
- The previous version of `attack4` re-implemented the old two-KDF pipeline by
  hand and reported "not found" for images the current build decodes. The
  rewrite calls `steg.Decode` and finds the password.
- The cost of a wrong candidate is one Argon2id plus building the order; the
  order grows with image size and is about a third of the time on 1024×1536. The
  correct candidate costs more because it decrypts and MACs the whole padded
  block, which for these images (encoded before bounded padding) was the
  whole carrier (~590 KB for `dude.png`). With bounded padding a 19-byte
  payload has a 4 KiB block, so the correct candidate is barely more expensive
  than a wrong one; the cost of wrong candidates, which dominates, is
  unchanged.
- At ~68 ms per candidate, a 1M-word dictionary takes about 19 hours on one
  stream against a `dude.png`-sized image. Argon2id's 64 MiB per instance
  bounds how many candidates an attacker can run in parallel, particularly on
  GPUs, but a multi-core CPU can still run several streams at once.
- This remains the primary practical attack for weak passwords.

---

## Attack 5: MAC-then-Encrypt Analysis

### Theory

`container.WritePayload` computes the HMAC over the **plaintext**, then
encrypts the data and the tag together (MAC-then-Encrypt, MtE):

```go
hashFn.Write(buf[:n])   // HMAC over plaintext
w.Write(buf[:n])        // write ciphertext (encrypted plaintext)
// ...
w.Write(checksum)       // write encrypted HMAC tag
```

Modern practice (TLS 1.3, AEAD) is Encrypt-then-MAC. MtE is known to enable
padding-oracle attacks under CBC mode.

### Why This Is Safe Here (But Still a Design Smell)

AES-CTR has no padding, so there is no padding oracle. A ciphertext bit flip
in CTR flips exactly the same plaintext bit, and any change to the plaintext
or the encrypted tag makes HMAC verification fail. The decoder returns no
plaintext before the tag is verified, so a flipped bit yields only a generic
error. A flip in the length field is caught even earlier, by the length bound,
or makes the reader take the wrong bytes as the tag.

The risk would materialise if the cipher were ever swapped to CBC mode
without revisiting the MAC ordering.

### Exercise

Flip one ciphertext bit in an encoded image and check that:
1. exactly the corresponding plaintext bit changes (CTR property), and
2. decoding fails (integrity holds).

### Results

Throwaway test (deleted afterwards) using the library internals to locate
stream bits: 128×128 random opaque carrier, defaults (3 channels, 1 bit/ch,
49 152 stream bits), 19-byte payload. For each row the raw LSB holding that
stream bit was flipped, the stream decrypted with the original keys and
compared with the unmodified stream, and `steg.Decode` called.

| Stream bit | Region | Plaintext bits changed | Decode result |
|-----------|--------|------------------------|---------------|
| 5 | salt (plaintext) | n/a: different salt, different keys | rejected: length 2566749578 exceeds max 6092 |
| 130 | container length, low byte | 1 (bit 130) | rejected: length 6124 exceeds max 6092 |
| 158 | container length, high byte | 1 (bit 158) | rejected: length 33560524 exceeds max 6092 |
| 165 | real-length prefix | 1 (bit 165) | checksum validation failed |
| 192 | payload byte 0 | 1 (bit 192) | checksum validation failed |
| 275 | payload byte 10 | 1 (bit 275) | checksum validation failed |
| 20000 | padding | 1 (bit 20000) | checksum validation failed |
| 48896 | HMAC tag, first bit | 1 (bit 48896) | checksum validation failed |
| 49151 | HMAC tag, last bit | 1 (bit 49151) | checksum validation failed |

Black-box check on the same image: 300 trials, each flipping the LSB of one
random channel of one random pixel. **300/300 decodes failed** (297 at the
HMAC check, 3 at the length bound). That image was encoded before bounded
padding, so the stream filled the carrier and every LSB was part of it. With
bounded padding a flip in a pixel past the end of the stream changes no stream
bit and decoding still succeeds; that is expected, since those pixels carry no
data, and integrity of the stream itself is unaffected.

**Observations:** the CTR bit-flip property holds (one ciphertext bit changes
exactly one plaintext bit, at the same position), and integrity holds for
every region, including padding and the tag itself. No fix needed.

---

## Summary Table

| # | Attack | Requires password | Practical? | Status |
|---|--------|------------------|-----------|--------|
| 1 | Chi-square + RS steganalysis | No | Only at 2–8 bits/ch with large payloads | Mitigated at 1 bit/ch: LSB matching + bounded padding read CLEAN (0/6) on `dude.png` from 31 B to full capacity (previously 6/6). Stronger detectors for LSB matching (characteristic-function, SRM-type classifiers) are outside this repo's tooling and likely still detect high embedding rates |
| 2 | Two-time pad on bootstrap salt | Yes | — | Fixed in c69a626 (bootstrap removed); tool removed |
| 3 | Bootstrap CPA salt recovery | Yes + grey-box encode | — | Fixed in c69a626; tool removed |
| – | Cheap `math/rand` traversal order (~2^31 orders) | No | Would be, now that padding is bounded | Fixed in 4fd29ba: ChaCha8 keyed by Argon2id |
| 4 | Dictionary / brute force | No (finding it) | Yes, for weak passwords | Mitigated: one Argon2id (t=2, 64 MiB) per candidate |
| 5 | MtE bit flip | — | No | Integrity holds (300/300 flips rejected); informational |
