# Contention and CPU-Heavy Benchmarks — Corrected

**Date:** 2026-10-11
**Method:** Localhost benchmarks (fair comparison, no network bias)

---

## Network Bias Discovery

Initial tests ran from a loadgen instance with unequal network paths:

| Instance | RTT from Loadgen |
|----------|------------------|
| FlashDB | 0.35ms |
| Redis | 0.74ms |

This made FlashDB appear 67% faster on hot-key tests — completely wrong. All results below use localhost benchmarks.

---

## Test 1: Hot-Key Contention (Single Key INCR)

All 50 clients hammer the SAME key — maximum lock contention for FlashDB.

### Results (Localhost - Fair)

| System | ops/sec | Avg Latency | p99 Latency |
|--------|---------|-------------|-------------|
| FlashDB | 149,701 | 0.292ms | 1.103ms |
| **Redis** | **160,256** | **0.163ms** | **0.311ms** |

**Redis is 7% faster** with much better tail latency.

### Analysis

- FlashDB pays mutex acquisition cost on every operation
- Go's mutex is efficient (only 7% overhead), but adds latency variance
- p99 latency is 3.5x worse — occasional lock contention spikes

---

## Test 2: Large Value Operations (10KB)

Tests memory allocation and serialization overhead.

### Results (Localhost - Fair)

| Operation | FlashDB | Redis | FlashDB/Redis |
|-----------|---------|-------|---------------|
| SET | 54,765 | 90,009 | **61%** |
| GET | 90,744 | 107,411 | **84%** |

**Redis is 1.2-1.6x faster.**

### Analysis

- Redis uses jemalloc, optimized for this allocation pattern
- Go's runtime allocator is general-purpose, not tuned for large buffers
- SET is worse because it involves both allocation and copying
- Potential fix: sync.Pool for buffer reuse

---

## Test 3: LRANGE on Large List (10k elements)

CPU-bound operation: serialize 10,000 list elements.

### Results (Localhost - Fair)

| Test | FlashDB | Redis | FlashDB/Redis |
|------|---------|-------|---------------|
| Sequential (100 req) | 0.456s | 0.338s | **74%** |
| Parallel (5000 req) | 17.56s | 13.01s | **74%** |

**Redis is 1.35x faster.**

### Analysis

- No parallelism advantage: all requests hit same key → same lock
- RESP serialization of 10k elements is CPU-bound
- Redis's C implementation is more efficient at tight loops
- The ratio is identical for sequential vs parallel (both 74%)

---

## Test 4: Extreme Concurrency (500 clients, hot key)

Stress test for lock contention.

| System | ops/sec | p99 Latency |
|--------|---------|-------------|
| FlashDB | 173,732 | 3.647ms |
| Redis | 170,940 | 2.063ms |

**Throughput is similar, but FlashDB has worse tail latency.**

At extreme concurrency, FlashDB's locks cause occasional convoy effects.

---

## Conclusions

### Where Redis Wins

| Scenario | Redis Advantage | Cause |
|----------|-----------------|-------|
| Hot key ops | 7% faster | No lock overhead |
| Large values | 40-65% faster | jemalloc vs Go allocator |
| LRANGE | 35% faster | Optimized C serialization |
| Tail latency | 3-5x better p99 | No GC pauses, no lock waits |

### Where FlashDB Holds Its Own

| Scenario | FlashDB vs Redis |
|----------|------------------|
| Basic ops (small values) | 96-102% |
| High concurrency (200 clients) | 91-100% |
| Throughput at 500 clients | ~100% |

### Key Insight

> Go's mutex is efficient enough that lock contention doesn't severely hurt throughput. The bigger issues are: (1) memory allocation efficiency for large values, (2) missing pipelining support, and (3) GC-induced tail latency spikes.

---

## What Would Make FlashDB Win?

FlashDB's multi-goroutine model would show advantages in:

1. **Parallel operations on DIFFERENT keys** — e.g., MGET fetching 100 keys could parallelize across shards
2. **CPU-heavy operations spread across keys** — e.g., Lua scripts, sorted set operations
3. **Pattern matching** — KEYS/SCAN could parallelize across KeyLock shards

None of these are implemented yet. See `docs/ROADMAP.md`.
