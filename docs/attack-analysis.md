# Attack Analysis: steg Cryptographic Security

This document catalogues known attack surfaces against the `steg` tool's
steganography and encryption scheme, framed as cryptopals-style exercises.
Each section gives the theory, the exploit path, and measured results. The
tools live in `cmd/attack1` (steganalysis report), `cmd/stegbench` (trained
steganalysis benchmark) and `cmd/attack4` (dictionary attack); all are built
on the library's exported API, so they track the current format instead of
re-implementing it.

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

The padded block is `max(64, nextPowerOfTwo(4 + payload))` bytes
(`MinPaddedBlock` = 64; it was 4096 before), or the whole capacity when that
bucket does not fit (`paddedSize` in `steg/steg.go`).
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

## Attack 1: Steganalysis (Chi-Square, RS, trained SPAM detector)

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
same image, for comparison. The chi-square and RS values of the `current`
31 B row, and the whole `-b 2` row, were measured with the earlier 4 KiB
padding floor (31 B then changed 16,652 channel bytes, 9,724 pixels, 0.62%);
with the 64-byte floor only the change counts of the `current` 31 B row were
re-measured, and `steg detect` still reads CLEAN.

| Encoder | Payload | Channel bytes changed | Pixels changed | Chi-square p (R/G/B) | RS asym (R/G/B) | Verdict |
|---------|---------|----------------------|----------------|---------------------|-----------------|---------|
| previous | 31 B text | 2,358,580 | 1,375,953 (87.5%) | 0.4973 / 0.2542 / 0.5285 | −0.0444 / −0.0445 / −0.0443 | LIKELY_STEGO |
| previous | 589,768 B (full) | 2,359,650 | 1,376,309 (87.5%) | 0.6226 / 0.1330 / 0.9523 | −0.0432 / −0.0426 / −0.0431 | LIKELY_STEGO |
| current | 31 B text | 432 | 260 (0.0165%) | 0.0000 / 0.0000 / 0.0000 | −0.0047 / −0.0014 / −0.0057 | CLEAN |
| current | 100,000 B | 524,152 | 305,756 (19.4%) | 0.0000 / 0.0000 / 0.0000 | −0.0041 / −0.0017 / −0.0053 | CLEAN |
| current | 589,768 B (full) | 2,360,072 | 1,377,013 (87.6%) | 0.0000 / 0.0000 / 0.0000 | −0.0014 / +0.0014 / −0.0015 | CLEAN |
| current, `-P` | 589,768 B (full) | — | — | 0.0000 / 0.0000 / 0.0000 | −0.0003 / +0.0041 / −0.0012 | CLEAN |
| current, `-b 2` (4 KiB floor) | 31 B text | 12,360 | 5,440 (0.35%) | 0.0000 / 0.0000 / 0.0000 | −0.0046 / −0.0020 / −0.0056 | CLEAN |

Clean original: chi-square p 0.0000 / 0.0000 / 0.0000, RS −0.0045 / −0.0017 /
−0.0055. On a synthetic 512×512 photo-like carrier with every slot filled,
replacement gave chi-square p 0.27 / 0.75 / 0.41 and RS ≈ −0.17 on every
channel (LIKELY_STEGO); matching gave p 0.0000 and RS ≈ 0.00 (CLEAN).

**Observations:**
- At 1 bit/channel no channel value changed by more than ±1. With `-b 2` some
  did, as expected from replacing 2 bits; that row is CLEAN only because
  bounded padding touched 0.35% of the pixels (with the 4 KiB floor; fewer
  now).
- Bounded padding is what shrinks the footprint of small payloads (87.5% →
  0.62% of pixels for 31 bytes with the 4 KiB floor, 0.0165% with the 64-byte
  floor; a 1,000-byte payload went from 0.62% to 0.16%). LSB matching is
  what keeps a full carrier
  CLEAN: the full-capacity row changes as many pixels as the previous encoder
  did, yet chi-square stays at p = 0.0000 and RS inside the clean range.
- RS asymmetry at full fill moves from the clean ≈ −0.004 toward 0 (and
  slightly positive on G). That is a measurable shift, just not in the
  direction or size the −0.02 threshold looks for; a detector calibrated on the
  clean image could notice it.
- These two detectors model LSB replacement. CLEAN means only that they found
  nothing. A trained detector does see LSB matching, at low rates as well as
  high ones; see [Trained detector](#trained-detector-spam--fld-ensemble)
  below.
- At 2–8 bits/channel the encoder still uses replacement, so a large payload
  there is flagged (`TestEncoderOutputReplacementAboveOneBit`).
  `TestEncoderOutputEvadesDetectors` guards the 1-bit result.
- Payload size is no longer fully hidden: the number of changed pixels grows
  with the power-of-two bucket, so an estimate of the embedding rate reveals
  the bucket roughly. With the 64-byte floor the buckets below 4 KiB are 64,
  128, 256, … bytes, so small sizes are distinguishable in finer steps than
  when everything up to 4,092 bytes shared one block, although at these rates
  the amount of change is hard to estimate.

### Trained detector (SPAM + FLD ensemble)

#### Theory

Chi-square and RS test for a specific artefact of replacement. A trained
detector instead learns what the embedding does to the local statistics of
the image, whatever the embedding rule.

**Features.** Second-order SPAM (Pevný, Bas, Fridrich, "Steganalysis by
Subtractive Pixel Adjacency Matrix", IEEE TIFS 5(2), 2010), `analysis.SPAM`.
For each of 8 directions the difference between a pixel and its neighbour,
truncated to [−3, 3], is modelled as a second-order Markov chain; the
transition probabilities are averaged over the 4 horizontal/vertical and the
4 diagonal directions, giving 2 × 7³ = 686 features per 8-bit plane. ±1
noise, whichever rule produced it, disturbs these neighbour differences in
smooth regions. The sign convention of the differences follows the paper's
equation; the reference MATLAB code may pair them the other way round, which
is equivalent to analysing the intensity-inverted image (same detection power,
not numerically identical to published features). The paper text and
reference code were not available when this was written.

**Classifier.** Fisher-linear-discriminant ensemble (Kodovský, Fridrich,
Holub, "Ensemble Classifiers for Steganalysis of Digital Media", IEEE TIFS
7(2), 2012), `analysis.TrainEnsemble`. L = 51 base learners, each trained on a
random subspace of d_sub = 300 features and a per-class bootstrap sample; the
FLD direction solves (S_W + λI)·w = μ_s − μ_c with λ = 1e-4 · tr(S_W) / d_sub
(Cholesky, plain Go in `linalg.go`, no new dependency); each learner's
threshold minimises its weighted training error; the ensemble takes a
majority vote. The paper's out-of-bag search for L and d_sub is not
implemented.

#### Exploit Path

1. Collect cover images from the same source as the suspect images, and make
   stego versions at the rate to be detected (the attacker knows the method
   and roughly the rate).
2. Extract SPAM features from covers and stegos and train the ensemble.
3. Classify the suspect image's plane with `Ensemble.Predict`.

No password needed. `cmd/stegbench` runs this as an experiment: for each
cover it makes one stego per (method, rate), with simulated LSB replacement,
simulated LSB matching (`round(rate · N)` uniformly random pixels, random
message bits) or the real `steg.Encode` (`-methods tool`, 1 channel × 1 bit,
payload sized to the reachable bounded-padding bucket closest to the requested
rate). It trains on half the images and tests on the other half (a cover and
its stegos always in the same half), repeated over several random splits, and
also records how often chi-square and RS flag covers and stegos. Everything
is seeded from `-seed` except `tool` rows, whose salt is random.

```sh
go run ./cmd/stegbench -dir ~/datasets/bossbase/extracted/BOSSbase_1.01
```

#### Results

BOSSbase 1.01 (https://dde.binghamton.edu/download/ImageDB/BOSSbase_1.01.zip):
10,000 never-compressed 512×512 grayscale images. Defaults: 5 splits of
5,000 / 5,000 images, L = 51, d_sub = 300. The default run took 81 s and
peaked at 1.8 GB RSS on a 32-thread Ryzen 9 9950X3D.

P_E = (P_FA + P_MD) / 2 on the test half, mean ± std over the 5 splits;
0.5 = guessing. `eff` = bits embedded per pixel, `chg` = fraction of pixels
changed; chi and RS columns are the fraction of covers / stegos flagged.

```
method            rate     eff     chg  P_E               P_FA   P_MD  chi cov/stg   RS cov/stg
replacement       0.05  0.0500  0.0250  0.2259 ± 0.0033 0.2564 0.1954  0.000/0.000   0.019/0.284
replacement        0.1  0.1000  0.0500  0.1581 ± 0.0043 0.1722 0.1441  0.000/0.000   0.019/0.755
replacement        0.2  0.2000  0.1000  0.0813 ± 0.0028 0.0922 0.0704  0.000/0.001   0.019/0.945
replacement        0.4  0.4000  0.2000  0.0216 ± 0.0005 0.0210 0.0223  0.000/0.007   0.019/0.991
replacement          1  1.0000  0.5000  0.0039 ± 0.0002 0.0055 0.0022  0.000/0.945   0.019/1.000
matching          0.05  0.0500  0.0250  0.2246 ± 0.0033 0.2468 0.2023  0.000/0.000   0.019/0.027
matching           0.1  0.1000  0.0500  0.1601 ± 0.0063 0.1806 0.1396  0.000/0.000   0.019/0.040
matching           0.2  0.2000  0.1000  0.1025 ± 0.0014 0.1181 0.0870  0.000/0.000   0.019/0.052
matching           0.4  0.4000  0.2000  0.0587 ± 0.0025 0.0701 0.0473  0.000/0.000   0.019/0.068
matching             1  1.0000  0.5000  0.0162 ± 0.0010 0.0207 0.0117  0.000/0.000   0.019/0.075
matching         0.005  0.0050  0.0025  0.4342 ± 0.0014 0.4551 0.4132  0.000/0.000   0.019/0.020
matching          0.01  0.0100  0.0050  0.3831 ± 0.0048 0.4089 0.3573  0.000/0.000   0.019/0.020
matching          0.02  0.0200  0.0100  0.3201 ± 0.0053 0.3500 0.2902  0.000/0.000   0.019/0.021
```

**Cross-check with the real encoder.** On 500 images, with the earlier 4 KiB
floor, `tool` against the matching simulation at the rates bounded padding
could then reach in a 512×512 single-channel carrier (0.1266 bpp for the
4 KiB floor bucket, then 0.2516, 0.5016 and 1.0):

| Rate (bpp) | P_E `tool` | P_E `matching` |
|---|---|---|
| 0.127 | 0.223 | 0.208 |
| 0.25 | 0.144 | 0.136 |
| 0.50 | 0.085 | 0.079 |
| 1.0 | 0.031 | 0.031 |

Equal within split noise: the simulation is a faithful model of the encoder.
With the 64-byte floor the reachable rates start at (52 + 64) · 8 / 262,144 =
0.0035 bpp. On 2,000 images (`-methods matching,tool -rates
0.00354,0.0094,0.1266`):

| Block (bytes) | Rate (bpp) | P_E `tool` | P_E `matching` |
|---|---|---|---|
| 64 | 0.0035 | 0.4597 ± 0.0038 | 0.4603 ± 0.0016 |
| 256 | 0.0094 | 0.3952 ± 0.0051 | 0.3989 ± 0.0037 |
| 4096 | 0.1266 | 0.1488 ± 0.0079 | 0.1500 ± 0.0063 |

The 4096 row is lower than in the 500-image run, most likely because the
detector trains on four times as many images.

**Choice of floor.** Matching simulation on all 10,000 images at the rate each
candidate floor gives for a minimal message (52 bytes of framing plus the
block):

| Floor (bytes) | Rate (bpp) | P_E |
|---|---|---|
| 16 | 0.0021 | 0.4716 ± 0.0014 |
| 32 | 0.0026 | 0.4643 ± 0.0012 |
| 64 | 0.0035 | 0.4507 ± 0.0015 |
| 128 | 0.0055 | 0.4278 ± 0.0031 |
| 256 | 0.0094 | 0.3894 ± 0.0045 |
| 512 | 0.0172 | 0.3333 ± 0.0035 |
| 1024 | 0.0328 | 0.2680 ± 0.0040 |
| 2048 | 0.0641 | 0.1997 ± 0.0052 |
| 4096 | 0.1266 | ≈ 0.15 (2,000-image run above) |

**Observations:**
- Chi-square and RS are blind to LSB matching. Chi-square flags no matching
  stego; RS flags 2–7.5% of them against a 1.9% false-positive rate on covers.
  RS catches replacement from 0.1 bpp (76% of stegos; 95% at 0.2).
- Against the trained detector, matching is no better than replacement at low
  rates: P_E ≈ 0.22 at 0.05 bpp and ≈ 0.16 at 0.1 for both. It helps only from
  0.2 bpp up (0.10 vs 0.08 at 0.2; 0.016 vs 0.004 at 1). The benefit of
  matching is against the simple detectors, not a modern one.
- Detectability is dominated by the embedding rate: P_E 0.43 at 0.005 bpp,
  0.38 at 0.01, 0.32 at 0.02.
- The bounded-padding floor mattered. The original 4 KiB minimum block was
  0.127 bpp in a 512×512 single-channel carrier, where the detector was right
  about 85% of the time (P_E ≈ 0.15). The floor is now 64 bytes: a short
  message is 0.0035 bpp there and P_E ≈ 0.45–0.46, right about 54% of the
  time. Below 64 the 52 bytes of framing dominate and a smaller floor buys
  little (0.46 at 32, 0.47 at 16). The price is finer size buckets below
  4 KiB. Medium and large payloads are still detected: P_E 0.27 at 1 KiB and
  0.15 at 4 KiB in the same carrier. In a 1024×1536 colour image with 3
  channels a short message is now about 0.0002 bits per channel value (about
  0.007 with the 4 KiB floor). Extrapolate from the grayscale results
  only cautiously: a detector that exploits colour-channel correlation, a
  different cover source and possible JPEG history in the carrier all change
  the picture.
- Caveats: BOSSbase is never-compressed, the easiest setting for the
  detector; the detector is trained on the same cover source and rate it is
  tested on (an idealised attacker); SPAM dates from 2010 and stronger
  detectors (SRM, CNNs) exist. These P_E values are an upper bound on how hard
  the stego is to detect.
- Next steps (README): the padding floor has been lowered (above);
  adaptive embedding with syndrome-trellis codes (fewer changes per bit,
  placed in noisy regions) is next, to improve P_E at medium rates.
  `stegbench` is the yardstick.

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
  payload has a 64-byte block, so the correct candidate is barely more expensive
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
| 1 | Steganalysis: chi-square + RS | No | Only at 2–8 bits/ch with large payloads | Mitigated at 1 bit/ch: LSB matching + bounded padding read CLEAN (0/6) on `dude.png` from 31 B to full capacity (previously 6/6) |
| 1 | Steganalysis: trained SPAM + FLD ensemble | No (needs covers from a similar source) | Yes, at the rates the tool writes | Open. On BOSSbase, P_E ≈ 0.22 at 0.05 bpp and ≈ 0.16 at 0.1 for matching and replacement alike; a short message is 0.0035 bpp in a 512×512 single-channel carrier with the 64-byte padding floor (real encoder P_E ≈ 0.46; it was 0.127 bpp and P_E ≈ 0.15 with the earlier 4 KiB floor), but 1 KiB is P_E ≈ 0.27 and 4 KiB ≈ 0.15. Stronger detectors exist. Planned: adaptive embedding (STC) |
| 2 | Two-time pad on bootstrap salt | Yes | — | Fixed in c69a626 (bootstrap removed); tool removed |
| 3 | Bootstrap CPA salt recovery | Yes + grey-box encode | — | Fixed in c69a626; tool removed |
| – | Cheap `math/rand` traversal order (~2^31 orders) | No | Would be, now that padding is bounded | Fixed in 4fd29ba: ChaCha8 keyed by Argon2id |
| 4 | Dictionary / brute force | No (finding it) | Yes, for weak passwords | Mitigated: one Argon2id (t=2, 64 MiB) per candidate |
| 5 | MtE bit flip | — | No | Integrity holds (300/300 flips rejected); informational |
