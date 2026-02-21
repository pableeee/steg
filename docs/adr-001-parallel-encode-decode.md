# ADR-001: Parallel Encode/Decode — Test-and-Compare Phase

**Date:** 2026-02-21
**Status:** Accepted
**Deciders:** pableeee

---

## Context

The sequential `Encode`/`Decode` functions process every bit of every pixel one at a
time. For large images or payloads the bottleneck is the inner bit loop, not key
derivation (Argon2id is already parallelised internally). A parallel approach using
Go goroutines should allow independent chunks of the payload to be encrypted/written
to different pixel ranges concurrently.

Two refactoring strategies were considered:

1. **Shared-pixel-sequence approach (this ADR)** — keep the existing `RNGCursor`
   unchanged in contract; export `GenerateSequence` so workers can share the same
   pre-shuffled `[]image.Point` slice. Each worker constructs its own
   `RNGCursor + CipherMiddleware + CursorAdapter` stack over the shared (read-only)
   point slice and seeks to its assigned byte offset before operating. No pixel-level
   interface changes required.

2. **Pixel-level interface refactor** — change `Cursor` to operate at the pixel level
   rather than the bit level, reducing indirection and enabling vectorised I/O.
   Deferred because it requires wider interface changes and a larger test matrix.

---

## Decision

Implement `EncodeParallel` / `DecodeParallel` **alongside** the existing sequential
functions using Option 1 (shared pixel sequence). This is the test-and-compare phase:
both paths produce identical on-image layouts so they are interoperable. A `--parallel`
CLI flag selects which path runs.

The existing `Encode` / `Decode` are **not modified**.

---

## On-image layout (unchanged from sequential)

```
bytes 0–3     nonce (plaintext)
bytes 4–7     encrypted LE length
bytes 8..N    encrypted payload
bytes N..N+32 encrypted HMAC-SHA256 tag
```

---

## Key design choices

### Shared pixel sequence, per-worker cipher state

`cursors.GenerateSequence(w, h, seed)` generates the shuffled pixel order once.
All workers receive the same `[]image.Point` slice via the new `WithSharedPoints`
option. Because the slice is **read-only after construction** there is no data race.

Each worker owns an independent `cipher.StreamCipherBlock`, `RNGCursor`, and
`CursorAdapter`, so cipher state and cursor position are never shared. The only
shared mutable state is the underlying `draw.Image` pixel array.

### Pixel-safe chunk alignment

With 3 bits/pixel and 8 bits/byte, a chunk boundary is pixel-safe only at byte
offsets that are multiples of `lcm(8, 3) / 8 = 3 bytes` (= 8 pixels). All chunks
except the last must be multiples of this alignment to prevent a worker from writing
half a pixel that another worker is simultaneously reading or writing. The final chunk
is exempt because no later worker follows it.

Default chunk size: `alignment × 1024 = 3072 bytes`.

### Worker count

`runtime.GOMAXPROCS(0)` workers — matches the number of logical CPUs available to
the process, letting the scheduler keep all cores busy without excessive goroutine
overhead.

### Nonce and length written sequentially

The 4-byte plaintext nonce is written **before** workers start (no cipher involved).
The encrypted 4-byte length and 32-byte HMAC tag are written **after** all workers
finish, using a single sequential cipher stack seeked to the correct offsets. This
avoids coordinating writers for these small critical fields.

### HMAC computation

Encode: HMAC is accumulated over plaintext chunks by the dispatcher goroutine as it
feeds the job channel. This is inherently sequential over the input stream but happens
concurrently with workers writing encrypted bits to the image.

Decode: HMAC is verified over the fully-assembled `decryptedBuf` after all workers
finish. `hmac.Equal` provides constant-time comparison.

---

## Consequences

**Positive:**
- No changes to existing `Encode`/`Decode` or any existing tests.
- On-image format is fully interoperable: `EncodeParallel` images can be decoded
  by `Decode`, and vice-versa.
- Chunk alignment ensures correctness even under parallel pixel writes.
- Benchmarks (`BenchmarkEncodeSequential`, `BenchmarkEncodeParallel`, etc.) provide
  a direct apples-to-apples comparison on a 1000×1000 image with a 100 KB payload.

**Negative / risks:**
- Concurrent writes to `draw.Image` pixels are safe only because each worker touches
  a disjoint set of byte offsets within the image buffer. This relies on the alignment
  invariant being maintained. If chunk sizes are changed, the alignment multiple (3
  bytes) must be preserved.
- The pixel-level refactor (Option 2) is deferred; if benchmarks show that goroutine
  overhead dominates, that refactor will be needed to see meaningful gains on smaller
  images.
- Key derivation (Argon2id) is still sequential and per-call; for very short payloads
  it will dominate latency regardless of parallelism.

---

## Alternatives considered

| Option | Pros | Cons |
|--------|------|------|
| Shared pixel sequence (chosen) | Minimal interface changes; safe read-only sharing | Workers still operate at the bit level |
| Pixel-level interface refactor | Lower per-bit overhead; enables vectorised I/O | Wider interface changes; larger diff; riskier |
| Channel-per-goroutine with image partitioning | No shared mutable state | Requires non-contiguous pixel assignments; complex cursor logic |

---

## References

- `steg/parallel.go` — implementation
- `steg/bench_test.go` — correctness tests and benchmarks
- `cursors/rng_cursor.go` — `GenerateSequence`, `WithSharedPoints`, `BitCount()`
- `steg/steg.go` — `gcd`, `lcm`, `lcmBytes` alignment helpers
