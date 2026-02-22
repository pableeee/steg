# Session Notes — Parallel Encode/Decode

**Date:** 2026-02-21
**Last commit:** `4ec902f` (perf: inline byteToBits)

---

## What was done this session

1. **Implemented `EncodeParallel` / `DecodeParallel`** (`steg/parallel.go`)
   - GOMAXPROCS worker pool; shared pixel sequence via `WithSharedPoints`
   - Pixel-safe chunk alignment: `lcm(8, 3) / 8 = 3 bytes` minimum
   - On-image layout identical to sequential — fully interoperable
   - `--parallel / -P` CLI flag on both `encode` and `decode` subcommands

2. **`cursors/rng_cursor.go`** — exported `GenerateSequence`, added `WithSharedPoints`, nil-guard, `BitCount()`

3. **`steg/steg.go`** — added `gcd` / `lcm` / `lcmBytes` helpers

4. **`steg/bench_test.go`** — correctness tests + size-variant benchmarks (100×100 → 4K)

5. **Fixed `byteToBits`** (`cursors/adapter.go`) — inlined bit extraction, eliminated ~8M heap allocs/op at 4K

6. **`docs/adr-001-parallel-encode-decode.md`** — design decision recorded

---

## Benchmark results (Ryzen 9 9950X3D, 32 cores)

| Size | Payload | Enc seq | Enc par | Enc speedup | Dec seq | Dec par | Dec speedup |
|---|---|---|---|---|---|---|---|
| 100×100 | 1 KB | 10.1 ms | 10.2 ms | ~1× | 10.1 ms | 10.1 ms | ~1× |
| 500×500 | 50 KB | 23.8 ms | 14.0 ms | 1.70× | 19.6 ms | 13.3 ms | 1.47× |
| 2000×2000 | 500 KB | 280 ms | 123 ms | 2.28× | 251 ms | 139 ms | 1.81× |
| 4K 3840×2160 | 2 MB | 1158 ms | 328 ms | 3.53× | 932 ms | 303 ms | 3.07× |

Small images are dominated by Argon2id (~10 ms fixed cost). Speedup grows with payload size.

---

## Outstanding items (prioritised)

### 1. ~~Security — `bytesEqual` timing oracle in sequential decode~~ ✅ DONE

`container.ReadPayload` now uses `hmac.Equal` (constant-time) for tag comparison.
`bytesEqual` has been deleted.

---

### 2. Performance — per-bit interface dispatch (~34M allocs/op at 4K)

**Files:** `cursors/adapter.go`, `cursors/rng_cursor.go`, `cursors/middleware.go`

`WriteBit`/`ReadBit` are called 8× per byte through two interface hops
(`CipherMiddleware` → `RNGCursor`). This is the dominant remaining allocation source.

Fix: pixel-level interface refactor (ADR Option 2) — change `Cursor` to operate on
whole pixels/bytes, collapsing 8 (or 24) interface calls per byte into one.
This is a larger change requiring updates to mocks and all cursor tests.

---

### 3. Cleanliness — spurious temp cursor in parallel functions

**File:** `steg/parallel.go:58` (EncodeParallel) and similar in DecodeParallel

A `NewRNGCursor` is constructed solely to call `.BitCount()`, which is always 3
given the fixed `UseGreenBit()`+`UseBlueBit()` options. Replace with a constant:

```go
const bitCount = 3  // R_Bit | G_Bit | B_Bit
alignment := lcmBytes(8, bitCount)
```
