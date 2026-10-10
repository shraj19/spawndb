# FlashDB vs Redis Benchmark Analysis

**Date:** 2026-10-10
**Environment:** AWS us-east-1, 3x m7i-flex.large (2 vCPU, 8GB RAM each)
**Benchmark Tool:** redis6-benchmark
**Parameters:** 100,000 requests, 50 clients, no pipelining

## Raw Results (3 runs)

### SET (requests/second)
| Run | FlashDB | Redis | FlashDB/Redis |
|-----|---------|-------|---------------|
| 1 | 207,039 | 156,006 | 1.33x |
| 2 | 207,039 | 155,280 | 1.33x |
| 3 | 207,900 | 169,779 | 1.22x |
| **Avg** | **207,326** | **160,355** | **1.29x** |

### GET (requests/second)
| Run | FlashDB | Redis | FlashDB/Redis |
|-----|---------|-------|---------------|
| 1 | 200,803 | 159,490 | 1.26x |
| 2 | 208,333 | 166,667 | 1.25x |
| 3 | 208,333 | 164,745 | 1.26x |
| **Avg** | **205,823** | **163,634** | **1.26x** |

### INCR (requests/second)
| Run | FlashDB | Redis | FlashDB/Redis |
|-----|---------|-------|---------------|
| 1 | 194,932 | 172,712 | 1.13x |
| 2 | 209,205 | 170,358 | 1.23x |
| 3 | 207,900 | 162,866 | 1.28x |
| **Avg** | **204,012** | **168,645** | **1.21x** |

### LPUSH (requests/second) — THE BOTTLENECK
| Run | FlashDB | Redis | FlashDB/Redis |
|-----|---------|-------|---------------|
| 1 | 1,556 | 128,205 | 0.012x |
| 2 | 1,569 | 126,582 | 0.012x |
| 3 | 1,538 | 130,378 | 0.012x |
| **Avg** | **1,554** | **128,388** | **0.012x (83x slower)** |

### LPOP (requests/second)
| Run | FlashDB | Redis | FlashDB/Redis |
|-----|---------|-------|---------------|
| 1 | 211,864 | 159,490 | 1.33x |
| 2 | 199,203 | 144,300 | 1.38x |
| 3 | 205,761 | 151,057 | 1.36x |
| **Avg** | **205,609** | **151,616** | **1.36x** |

---

## Critical Analysis: Why FlashDB *Appears* Faster (But Probably Isn't)

### The Skeptical View (IMPORTANT)

FlashDB showing 1.2-1.3x faster than Redis on simple operations is **suspicious** and likely due to **missing features**, not superior implementation. Here's why:

#### 1. FlashDB Does Less Work Per Command

Redis does many things FlashDB doesn't:
- **Persistence (AOF/RDB):** Redis may be doing append-only file writes or snapshot preparation
- **Replication:** Even idle, Redis tracks replication state
- **Expire management:** Redis has sophisticated lazy + active expiration
- **Memory management:** Redis has complex memory allocation strategies for different data types
- **Client tracking:** Redis tracks per-client state, memory usage, etc.

FlashDB's simpler implementation means less code runs per command = faster, but also **less functionality**.

#### 2. Network Topology Difference

- FlashDB runs **natively** on EC2 (Go binary, single process)
- Redis runs **in Docker** (additional network hop through Docker bridge)

This adds latency to every Redis operation. A fairer test would run both in Docker or both natively.

#### 3. Latency Distribution Tells the Real Story

| Command | FlashDB p50 | Redis p50 | FlashDB p99 | Redis p99 |
|---------|-------------|-----------|-------------|-----------|
| SET | 0.183ms | 0.287ms | 0.503ms | 0.471ms |
| GET | 0.183ms | 0.271ms | 0.383ms | 0.487ms |
| LPUSH | **30.9ms** | 0.359ms | **70.3ms** | 0.591ms |

Notice: **p99 latencies are similar or Redis wins** — FlashDB's advantage disappears at the tail. LPUSH's 30-70ms latencies are catastrophic.

#### 4. The Test Is Not Extensive Enough

This benchmark tests:
- ✅ Simple SET/GET/INCR (FlashDB is competitive)
- ✅ Basic LPUSH/LPOP (LPUSH has severe issues)
- ❌ Concurrent access to same key
- ❌ Large values (only 3 bytes tested)
- ❌ Many keys (benchmark uses few keys)
- ❌ Pipelining (where Redis excels)
- ❌ Persistence under load
- ❌ Memory pressure scenarios
- ❌ Long-running stability

---

## Confirmed Finding: LPUSH Has a Severe Bug

This is the **only trustworthy conclusion** from this benchmark:

- LPUSH is **83x slower** than Redis (1,554 vs 128,388 ops/sec)
- Latency degrades over time: starts ~0.2ms, ends at 60-70ms
- This indicates **unbounded growth** or **lock contention** in the list implementation

This is a real bug worth investigating and fixing.

---

## Recommended Next Steps

1. **Fix the LPUSH bug** — this is clearly broken
2. **Run fairer comparison** — both in Docker OR both native
3. **Add pipelining tests** — `redis6-benchmark -P 16`
4. **Add concurrent same-key tests** — multiple clients hitting one key
5. **Add large value tests** — `-d 1024` (1KB values)
6. **Profile with pprof** — find where time is spent in LPUSH

---

## Files in This Directory

- `run-001/`, `run-002/`, `run-003/` — Three benchmark runs
  - `flashdb.csv` — FlashDB benchmark results
  - `redis.csv` — Redis benchmark results  
  - `flashdb-metrics.txt` — Prometheus metrics snapshot
  - `redis-metrics.txt` — redis_exporter metrics snapshot
