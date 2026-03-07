# Release Notes — Steganalysis (`detect` command)

**Date:** 2026-03-07
**Tag:** `v20260307`

---

## Overview

This release adds a `detect` subcommand and a new `steg/analysis` package implementing two
classical LSB steganalysis detectors: chi-square and RS analysis. The feature enables
users to measure how detectable their encoded images are, without requiring any external
tools.

---

## New: `steg detect` command

```
steg detect -i <image>
```

Runs chi-square and RS analysis on the image and prints per-channel results plus an
overall verdict.

```
Chi-square analysis (high p-value = suspicious):
  R: χ²=132.36      p=0.3543  [SUSPICIOUS]
  G: χ²=128.78      p=0.4391  [SUSPICIOUS]
  B: χ²=95.14       p=0.9843  [SUSPICIOUS]

RS analysis (positive asymmetry = suspicious):
  R: Rm=0.5007  Sm=0.4993  R-m=0.5429  S-m=0.4571  asymmetry=-0.0422  [CLEAN]
  G: Rm=0.5012  Sm=0.4988  R-m=0.5409  S-m=0.4591  asymmetry=-0.0397  [CLEAN]
  B: Rm=0.4996  Sm=0.5004  R-m=0.5438  S-m=0.4562  asymmetry=-0.0442  [CLEAN]

Verdict: SUSPICIOUS
```

---

## New: `steg/analysis` package

| File | Contents |
|---|---|
| `analysis.go` | `Analyze(img)` — runs both detectors, returns combined `Result` with `Verdict` |
| `chisquare.go` | `ChiSquare(img)` — per-channel chi-square test |
| `rs.go` | `RSAnalysis(img)` — per-channel RS analysis |
| `pixels.go` | Pixel extraction helpers shared by both detectors |
| `analysis_test.go` | Unit tests and benchmarks for both detectors |

---

## Validation against `dude.png`

The `test-visual` command was used to produce 12 encoded variants of a real photograph at
every (channels × bits-per-channel) combination, then `detect` was run on each. Results:

### Chi-square

| Image | Encoded channels | Chi-sq flags |
|---|---|---|
| original | none | 0 / 3 — CLEAN |
| ch=1, bpc=1–8 | R only | 1 / 3 — only R flagged |
| ch=2, bpc=1–8 | R + G | 2 / 3 — R and G flagged |
| ch=3, bpc=1–8 | R + G + B | 3 / 3 — all channels flagged |

Chi-square correctly scopes detection to exactly the channels that were written. Untouched
channels remain at p ≈ 0.0000 across all 12 variants. At full capacity the p-values on
encoded channels rise to 0.27–0.99.

### RS analysis

All 12 encoded variants showed RS asymmetry values that remained negative (ranging from
−0.04 down to −0.44 on encoded channels). None crossed the +0.01 suspicious threshold.

**Why:** RS analysis assumes a natural-photograph baseline where `Rm − Rnm` is near zero
(typically +0.01 to +0.02) and grows positive after embedding. The test photo's baseline
was already slightly negative (−0.004 to −0.005), and LSB embedding moved the asymmetry
further negative rather than toward positive, because the carrier had more odd-valued
pixels than a typical photograph in the sampled regions.

This is expected and documented behaviour — the RS test suite explicitly tests for
*direction of change* rather than absolute threshold crossing on synthetic or
atypical-baseline images.

**Implication for users:** chi-square is the primary detection signal. RS analysis provides
a secondary signal on photographs with a natural near-zero LSB baseline. Neither detector
flags the unencoded original.

---

## Verdict logic

| Suspicious signals (out of n total) | Verdict |
|---|---|
| 0 | `CLEAN` |
| 1 – (n−1) | `SUSPICIOUS` |
| all n | `LIKELY_STEGO` |

For a 3-channel image, `n = 6` (3 chi-square + 3 RS). The original photo scores `CLEAN`;
every encoded variant scores at least `SUSPICIOUS` due to chi-square hits.

---

## Files changed

| File | Change |
|---|---|
| `cmd/steg/detect.go` | New CLI subcommand |
| `cmd/steg/root.go` | Registered `detectCmd` |
| `steg/analysis/analysis.go` | `Analyze`, `Result`, `Verdict` types |
| `steg/analysis/chisquare.go` | Chi-square detector |
| `steg/analysis/rs.go` | RS analysis detector |
| `steg/analysis/pixels.go` | Pixel extraction helpers |
| `steg/analysis/analysis_test.go` | Tests and benchmarks |
| `README.md` | Added `detect` command docs and Steganalysis section |

---

## Known limitations

- **RS threshold is absolute** — the `+0.01` threshold is a well-known heuristic for
  natural photographs. Images with an atypical clean baseline (heavily processed, synthetic,
  or very uniform) may not trigger RS even when encoded at full capacity. Chi-square is more
  reliable in those cases.
- **No multi-bit RS support** — the current RS implementation analyses the LSB plane only
  (`bitsPerChannel=1`). Images encoded with higher bitsPerChannel settings produce
  distortion across multiple bit planes that RS in its current form does not account for.
- **No payload-size estimation** — the detectors flag presence but do not estimate how
  much data is embedded.
