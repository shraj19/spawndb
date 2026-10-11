# Contention and CPU-Heavy Benchmarks

**Date:** 2026-10-11
**Purpose:** Test scenarios where FlashDB's multi-goroutine model might show advantages or disadvantages vs Redis's single-threaded model.

---

## Test 1: Hot-Key Contention (Single Key INCR)

All clients hammer the SAME key — maximum lock contention for FlashDB.

### Initial Results (Network Bias - INVALID)

| System | ops/sec | Avg Latency | Network RTT |
|--------|---------|-------------|-------------|
| FlashDB | 210,526 | 0.192ms | 0.35ms |
| Redis | 125,945 | 0.372ms | 0.74ms |

**Problem:** FlashDB instance was physically closer to the loadgen. The 2x network latency difference dominated the results.

### Corrected Results (Localhost - VALID)

| System | ops/sec | Avg Latency |
|--------|---------|-------------|
| FlashDB | 148,810 | 0.296ms |
| **Redis** | **162,338** | **0.161ms** |

**Redis wins by 9%** — this is expected. Redis has no lock overhead; FlashDB pays mutex acquisition cost.

### Analysis

The lock contention penalty is smaller than expected because:
1. Go's `sync.Mutex` uses efficient spinlocking before blocking
2. INCR is extremely fast — lock held for nanoseconds
3. At 50 clients, contention isn't severe enough to cause convoy effects

---

## Test 2: Large Value Operations (10KB)

SET/GET with 10KB values — tests memory copy and serialization overhead.

| Operation | FlashDB | Redis | Ratio |
|-----------|---------|-------|-------|
| SET (10KB) | 72,516 | 95,877 | 0.76x |
| GET (10KB) | 125,628 | 143,062 | 0.88x |

**Redis wins by 24% (SET) and 12% (GET).**

### Analysis

Redis's optimized memory allocator (jemalloc) and buffer management outperforms Go's runtime for large allocations. This is expected — Redis has years of optimization for this specific use case.

---

## Test 3: LRANGE on Large List (10k elements)

CPU-bound operation: serialize 10,000 elements.

| Test Type | FlashDB | Redis |
|-----------|---------|-------|
| Sequential (100 requests) | 0.487s | 0.476s |
| Parallel (50×100 requests) | 11.467s | 11.393s |

**Nearly identical** — no parallelism advantage for FlashDB.

### Analysis

LRANGE on a single key serializes to the same client connection. Even with parallel clients, they're all blocked on the same list's lock. The CPU work per operation is too small (~50μs) to see parallelism benefits.

---

## Test 4: Extreme Concurrency (500 clients, single key)

| System | ops/sec | p99 Latency |
|--------|---------|-------------|
| FlashDB | 173,732 | 3.647ms |
| Redis | 170,940 | 2.063ms |

**Throughput tie, but Redis has better tail latency.**

### Analysis

At extreme concurrency, FlashDB's lock contention causes occasional delays (higher p99). Redis's single-threaded model has more predictable latency.

---

## Conclusions

### Where FlashDB Loses
1. **Hot-key contention** — 9% slower due to mutex overhead
2. **Large values** — 12-24% slower due to Go memory management vs jemalloc
3. **Tail latency** — higher p99 under contention

### Where FlashDB Ties
1. **LRANGE on large lists** — both I/O bound
2. **Extreme concurrency** — throughput similar, latency worse

### Where FlashDB Could Win (Not Yet Implemented)
1. **Parallel operations on different keys** — e.g., MGET with parallel fetch
2. **CPU-heavy operations on different keys** — e.g., parallel Lua scripts
3. **Pattern matching across shards** — parallel KEYS/SCAN

### Key Learning

> Initial benchmarks were misleading due to network latency variance. Always run localhost benchmarks to eliminate network factors when comparing database performance.

---

## Raw Data

### Localhost Benchmarks (Fair Comparison)
```
FlashDB (localhost):
"INCR",148809.53,0.296ms avg,0.231ms p50,0.751ms p99

Redis (localhost, persistence disabled):
"INCR",162337.66,0.161ms avg,0.159ms p50,0.303ms p99
```

### Network Latency (from loadgen)
```
FlashDB (10.0.1.87): 0.238/0.345/0.485 ms (min/avg/max)
Redis (10.0.1.73):   0.487/0.740/0.883 ms (min/avg/max)
```
