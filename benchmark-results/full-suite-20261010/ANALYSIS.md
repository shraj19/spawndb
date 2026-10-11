# FlashDB Benchmark Results — Post-Optimization

**Date:** 2026-10-11
**Environment:** AWS us-east-1, 3x m7i-flex.large (2 vCPU, 8GB RAM each)
**Benchmark Tool:** redis6-benchmark
**FlashDB Version:** Post-deque optimization (commit with waiter_registry + deque)

---

## Executive Summary

After implementing a deque-based list storage and waiter registry, FlashDB achieved:

- **132x improvement** in LPUSH throughput (1,554 → 205,804 ops/sec)
- **95-97% of Redis performance** on core operations (SET, GET, INCR, LPUSH, LPOP)
- **Consistent performance** across different concurrency levels (10-200 clients)

---

## Key Finding: LPUSH Fix Verified

### Before Optimization (Baseline 2026-10-10)
| Command | FlashDB | Redis | FlashDB/Redis |
|---------|---------|-------|---------------|
| LPUSH | 1,554 | 128,388 | **0.012x (83x slower)** |

### After Optimization (2026-10-11)
| Command | FlashDB | Redis Native | FlashDB/Redis |
|---------|---------|--------------|---------------|
| LPUSH | 205,804 | 215,100 | **0.96x** |

**Improvement: 132x faster LPUSH**

---

## Fair Comparison: FlashDB vs Native Redis

Both running natively on EC2 (no Docker), same instance types.

### Pipeline Depth = 1 (50 clients, 100k requests)

| Command | FlashDB (ops/sec) | Redis Native (ops/sec) | Ratio |
|---------|-------------------|------------------------|-------|
| SET | 210,084 | ~216,000 | 0.97x |
| GET | 209,644 | ~215,000 | 0.97x |
| INCR | 205,761 | 216,920 | 0.95x |
| LPUSH | 208,333 | 218,818 | 0.95x |
| LPOP | 209,644 | 215,983 | 0.97x |

**FlashDB achieves 95-97% of Redis throughput on basic operations.**

### Pipeline Depth = 16

| Command | FlashDB (ops/sec) | Redis Native (ops/sec) | Ratio |
|---------|-------------------|------------------------|-------|
| SET | 483,092 | ~1,500,000 | 0.32x |
| GET | 641,026 | ~1,800,000 | 0.36x |
| INCR | 478,469 | 1,886,793 | 0.25x |
| LPUSH | 526,316 | 1,538,462 | 0.34x |
| LPOP | 617,284 | 1,449,275 | 0.43x |

Redis significantly outperforms FlashDB with pipelining. This is expected — Redis has highly optimized batch processing.

### Pipeline Depth = 64

| Command | FlashDB (ops/sec) | Redis Native (ops/sec) | Ratio |
|---------|-------------------|------------------------|-------|
| SET | 540,546 | ~2,500,000 | 0.22x |
| GET | 757,818 | ~2,700,000 | 0.28x |
| INCR | 531,979 | 2,778,667 | 0.19x |
| LPUSH | 633,089 | 2,084,000 | 0.30x |
| LPOP | 709,447 | 2,041,470 | 0.35x |

---

## Concurrency Scaling

FlashDB with different client counts (Pipeline=1, 100k requests):

| Clients | SET | GET | INCR | LPUSH | LPOP |
|---------|-----|-----|------|-------|------|
| 10 | 91,241 | 88,183 | 91,324 | 94,697 | 90,498 |
| 50 | 210,084 | 209,644 | 205,761 | 208,333 | 209,644 |
| 100 | 199,203 | 207,039 | 203,252 | 206,612 | 207,469 |
| 200 | 190,476 | 193,798 | 193,424 | 194,932 | 200,401 |

**Observation:** FlashDB scales well up to 50 clients, then plateaus. This is typical for single-node in-memory stores.

---

## Data Size Impact

FlashDB SET/GET performance with different value sizes (50 clients, 100k requests):

| Data Size | SET (ops/sec) | GET (ops/sec) |
|-----------|---------------|---------------|
| 3 bytes | 197,239 | 203,666 |
| 100 bytes | 199,601 | 202,429 |
| 1 KB | 171,821 | 173,913 |
| 10 KB | 54,289 | 90,253 |

**Observation:** Performance degrades gracefully with larger values. GET degrades less than SET (expected — less data to copy on read).

---

## Extended Run (1M requests, 50 clients)

Sustained performance over 1 million requests:

| Command | FlashDB (ops/sec) | Redis Native (ops/sec) |
|---------|-------------------|------------------------|
| SET | 203,252 | ~210,000 |
| GET | 206,868 | ~212,000 |
| INCR | 204,583 | 213,493 |
| LPUSH | 205,804 | 215,100 |
| LPOP | 206,697 | 211,775 |

**Observation:** FlashDB maintains consistent throughput over extended runs. No degradation observed.

---

## Latency Analysis

### FlashDB Latency (50 clients, P=1)

| Command | p50 (ms) | p95 (ms) | p99 (ms) | max (ms) |
|---------|----------|----------|----------|----------|
| SET | 0.183 | 0.295 | 0.615 | 3.703 |
| GET | 0.183 | 0.263 | 0.399 | 3.551 |
| INCR | 0.191 | 0.295 | 0.463 | 2.567 |
| LPUSH | 0.183 | 0.271 | 0.431 | 5.591 |
| LPOP | 0.183 | 0.263 | 0.367 | 9.311 |

### Redis Native Latency (50 clients, P=1)

| Command | p50 (ms) | p95 (ms) | p99 (ms) | max (ms) |
|---------|----------|----------|----------|----------|
| INCR | 0.175 | 0.223 | 0.263 | 0.775 |
| LPUSH | 0.175 | 0.223 | 0.263 | 0.775 |
| LPOP | 0.183 | 0.239 | 0.311 | 0.895 |

**Observation:** FlashDB has slightly higher tail latencies (p99, max) than Redis. This could be due to Go GC pauses.

---

## What Was Fixed

### Problem
LPUSH was 83x slower than Redis due to slice-based list implementation with O(n) prepend operations.

### Solution
1. **Deque implementation** (`internal/utilities/deque/deque.go`) — O(1) push/pop at both ends
2. **Waiter registry** (`internal/storage/waiter_registry.go`) — efficient management of blocking operation waiters
3. **Refactored list store** (`internal/storage/lists.go`) — uses deque instead of slices

### Result
- LPUSH: 1,554 → 205,804 ops/sec (**132x improvement**)
- LPUSH now matches SET/GET/INCR performance

---

## Conclusions

1. **FlashDB is production-viable for basic workloads** — 95-97% of Redis performance on core operations
2. **Pipelining is a weakness** — Redis is 3-5x faster with pipelining; this could be optimized
3. **The deque fix was successful** — LPUSH bottleneck completely eliminated
4. **Tail latency could be improved** — p99/max latencies higher than Redis, likely GC-related

---

## Files in This Directory

```
full-suite-20261010/
├── pipeline-1/          # P=1 comparison (FlashDB, Redis Native, Redis Docker)
├── pipeline-16/         # P=16 comparison
├── pipeline-64/         # P=64 comparison
├── clients-10/          # Concurrency: 10 clients
├── clients-50/          # Concurrency: 50 clients
├── clients-100/         # Concurrency: 100 clients
├── clients-200/         # Concurrency: 200 clients
├── datasize-3/          # Value size: 3 bytes
├── datasize-100/        # Value size: 100 bytes
├── datasize-1024/       # Value size: 1 KB
├── datasize-10240/      # Value size: 10 KB
├── extended/            # 1M requests sustained run
├── benchmark.log        # Full run log
├── system-info.txt      # Instance details
└── flashdb-metrics.txt  # Prometheus metrics snapshot
```

---

## Reproduction

```bash
# Start FlashDB
./flashdb --port 6379 --metrics-port 9121

# Run benchmark
redis-benchmark -h <host> -p 6379 -n 100000 -c 50 -t set,get,incr,lpush,lpop --csv
```
