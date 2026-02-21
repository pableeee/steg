# Parallel Writing Performance Analysis

## Problem Statement

Benchmark results show that parallel writing is **8-15x slower** than sequential writing across all image sizes. This document analyzes why and identifies the root causes.

## Benchmark Results Summary

```
Small Image (100x100, 1875 bytes):
  Sequential:  486.656µs     3762.52 KB/s
  Parallel:    4.218303ms    434.07 KB/s  (8.67x slowdown)

Medium Image (500x500, 46875 bytes):
  Sequential:  9.075924ms    5043.71 KB/s
  Parallel:    123.060166ms  371.98 KB/s  (13.56x slowdown)

Large Image (2000x2000, 750000 bytes):
  Sequential:  156.948088ms  4666.65 KB/s
  Parallel:    2.428119942s  301.64 KB/s  (15.47x slowdown)
```

## Root Causes

### 1. Pre-Computation Overhead (Critical)

**The Problem:**

`PreComputeWritePlan()` essentially duplicates all the computational work of sequential writing:

```go
func (c *RNGCursor) PreComputeWritePlan(bits []uint8) ([]WriteOperation, error) {
    // For EVERY bit:
    // 1. Call tell() which does:
    //    - Multiple division/modulo operations
    //    - Array lookup in c.points[]
    //    - Channel calculation
    // 2. Create WriteOperation struct
    // 3. Increment cursor (simulating write)
}
```

**Sequential path does:**
- `tell()` calculation
- Read pixel
- Modify bit
- Write pixel
- Increment cursor

**Parallel path does:**
- `tell()` calculation (in PreComputeWritePlan)
- Create WriteOperation struct
- Group operations by pixel (expensive map operations)
- **THEN** read/modify/write pixels

**Impact:** The parallel path does **MORE work** than sequential, not less. We're doing all the position calculations twice - once during pre-computation, once during actual writing.

### 2. String-Based Map Hashing (Critical)

**The Problem:**

```go
func groupByPixel(ops []WriteOperation) map[string]*PixelWrite {
    for _, op := range ops {
        key := fmt.Sprintf("%d,%d", op.PixelX, op.PixelY)  // ❌ Creates string
        pixelMap[key] = ...                                 // ❌ String hash
    }
}
```

**Impact:**
- String allocation for every unique pixel coordinate
- String hashing for every map lookup
- Memory overhead for string storage
- For 1000x1000 image with 750KB data, this could be tens of thousands of string allocations

**Better approach:** Use `int64` composite key: `key := (int64(x) << 32) | int64(y)`

### 3. Goroutine/Channel Overhead Dominates

**The Problem:**

For small-to-medium workloads, the overhead of:
- Creating worker goroutines
- Channel communication (even buffered)
- WaitGroup synchronization
- Task queue management

**Exceeds the actual work** being done per pixel.

**Analysis:**
- Sequential: Read pixel (fast), modify bit (very fast), write pixel (fast)
- Per-pixel work: ~10-100 nanoseconds
- Goroutine creation: ~1-2 microseconds
- Channel send/receive: ~100-500 nanoseconds
- WaitGroup sync: ~100-500 nanoseconds

**For small images**, we might have only a few thousand pixels, so the overhead dominates.

### 4. Poor Cache Locality

**The Problem:**

Sequential writing processes pixels in cursor order (deterministic based on RNG sequence), which may have some spatial locality. Parallel writing processes pixels in **random work-queue order**, causing:

- Cache misses when reading pixels
- Cache misses when writing pixels
- No benefit from CPU prefetching

**Impact:** Memory access patterns are unpredictable, reducing CPU cache efficiency.

### 5. Memory Allocation Overhead

**The Problem:**

Parallel path allocates:
- `[]WriteOperation` - one per bit (750KB data = 6M bits = 6M structs)
- `map[string]*PixelWrite` - one entry per unique pixel
- Channel buffers
- Goroutine stacks
- Temporary slices during grouping

**Sequential path allocates:**
- Minimal - just a few local variables

**Impact:** Memory allocator pressure, GC pressure, memory bandwidth usage.

### 6. False Assumption: Pixel Writes Are Expensive

**The Reality:**

We assumed pixel writes (`img.Set()`) were expensive enough to benefit from parallelization. However:

- Modern image libraries (like Go's `image/draw`) are highly optimized
- Pixel writes are relatively fast (microseconds)
- The **coordination overhead** (channels, goroutines) exceeds the work itself

**Only beneficial if:**
- Each pixel write takes milliseconds (not microseconds)
- There are hundreds of thousands of pixels to update
- The work per pixel is CPU-intensive, not just memory I/O

### 7. Work Distribution Overhead

**The Problem:**

For each pixel write, we:
1. Create PixelWriteTask struct
2. Send to buffered channel
3. Worker receives from channel
4. Worker processes pixel
5. Worker signals completion

**For 1000 pixels, this is 1000 channel operations** - each with synchronization overhead.

## Why Sequential is Fast

Sequential writing is fast because:

1. **No overhead** - direct function calls, no coordination
2. **Cache-friendly** - processes pixels in order
3. **Minimal allocations** - stack-based, no heap allocations
4. **Simple control flow** - no context switching, no synchronization
5. **Optimized code path** - compiler can optimize the hot loop

## When Parallelization Would Help

Parallel writing would only be beneficial if:

1. **Pixel writes were CPU-intensive** - e.g., complex image processing, encryption per pixel
2. **Very large images** - e.g., 10K x 10K or larger, where overhead is amortized
3. **Independent expensive operations** - where coordination cost < work cost

## Recommendations

### Option 1: Accept Sequential as Default (Recommended)

**Keep parallel writing as an optional feature**, but document that:
- It's experimental
- It's only beneficial for specific use cases (very large images, CPU-intensive operations)
- Sequential is recommended for most cases

### Option 2: Optimize Parallel Implementation

If we want to improve parallel performance:

1. **Eliminate pre-computation**: Stream directly to pixel writes
2. **Use int64 keys instead of strings**: `key := (int64(x) << 32) | int64(y)`
3. **Batch pixel operations**: Group multiple pixels per goroutine task
4. **Use sync.Pool for allocations**: Reduce GC pressure
5. **Tune worker count**: Use fewer workers (maybe 2-4) to reduce overhead
6. **Profile and measure**: Use `pprof` to identify actual bottlenecks

### Option 3: Hybrid Approach

Use parallel writing only when:
- Image size > threshold (e.g., 2000x2000)
- Data size > threshold (e.g., 1MB)
- User explicitly requests it

Otherwise, use sequential.

### Option 4: Different Parallelization Strategy

Instead of pixel-level parallelism, consider:
- **Channel-level parallelism**: Process R, G, B channels in parallel (but this violates the cursor sequence)
- **Tile-based parallelism**: Divide image into tiles, process tiles in parallel (but cursor is random-order)
- **Batch processing**: Group bits into batches, process batches sequentially but optimize each batch

## Conclusion

The parallel implementation is slower because **the overhead of coordination exceeds the actual work**. This is a classic case where parallelization makes things worse due to:

1. Amdahl's Law - coordination overhead limits speedup
2. False assumption - pixel writes aren't expensive enough
3. Implementation overhead - pre-computation, string hashing, channel overhead

**Recommendation:** Keep sequential as the default, make parallel optional with clear documentation about when it might help.

