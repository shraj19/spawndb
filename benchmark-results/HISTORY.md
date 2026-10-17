# SpawnDB Benchmark History

Consolidated benchmark results. Raw data is gitignored — only conclusions here.

## How to Run Benchmarks

```bash
# On EC2 (Linux)
./scripts/benchmark.sh --flashdb-host <ip> --redis-host <ip> --requests 100000

# Full suite
./scripts/run-all-benchmarks.sh
```

---

## 2026-10-10: Baseline (Pre-Pipelining)

**Environment:** EC2 t3.medium, Amazon Linux 2, Go 1.21  
**SpawnDB:** Commit `9f4a277` (before Milestone B)  
**Redis:** 7.2 (Docker on same instance)

### Results (50 clients, pipeline=1, 100K requests)

| Command | SpawnDB ops/s | Redis ops/s | Ratio |
|---------|---------------|-------------|-------|
| SET | 47,393 | 51,282 | 0.92x |
| GET | 49,261 | 52,083 | 0.95x |
| INCR | 47,619 | 51,020 | 0.93x |
| LPUSH | 46,083 | 50,761 | 0.91x |
| RPUSH | 45,872 | 50,505 | 0.91x |
| LPOP | 46,512 | 50,251 | 0.93x |

**Latency (p99):**
- SpawnDB: 1.2-1.5ms
- Redis: 1.0-1.2ms

### Known Issues at Baseline
1. No pipelining support — each command = 1 syscall
2. Redis in Docker (slight disadvantage)
3. List/stream mutations not replicated

---

## 2026-10-17: Fair Comparison + LRANGE Optimization

**Environment:** AWS EC2 m7i-flex.large (Intel Xeon Platinum 8488C)  
**Setup:** 3 separate instances (SpawnDB, Redis, Client) — eliminates localhost bias  
**SpawnDB:** Commit `c285122` (post-optimization)  
**Redis:** 6.2 (native Amazon Linux)

### Results (50 clients, pipeline=1, 100K requests)

| Command | SpawnDB ops/s | Redis ops/s | Winner |
|---------|---------------|-------------|--------|
| SET | 221,238 | 147,710 | **SpawnDB +50%** |
| GET | 237,529 | 152,207 | **SpawnDB +56%** |
| INCR | 216,450 | 149,031 | **SpawnDB +45%** |
| LPUSH | 239,234 | 146,627 | **SpawnDB +63%** |
| RPUSH | 227,272 | 153,846 | **SpawnDB +48%** |
| LPOP | 219,298 | 144,300 | **SpawnDB +52%** |
| LRANGE_100 | 102,880 | 105,263 | Tie |
| LRANGE_300 | 41,425 | 42,716 | Tie |
| LRANGE_500 | 26,143 | 28,082 | Redis +7% |
| LRANGE_600 | 21,992 | 23,832 | Redis +8% |

### Pipelining Results (100 clients, P=16)

| Command | SpawnDB ops/s | Redis ops/s | Winner |
|---------|---------------|-------------|--------|
| SET | 775K | 1.9M | Redis 2.4x |
| GET | 1.4M | 2.2M | Redis 1.5x |

### Client Scaling (SET, P=1)

| Clients | SpawnDB | Redis | Winner |
|---------|---------|-------|--------|
| 50 | 237K | 147K | SpawnDB +61% |
| 500 | 185K | 193K | Redis +4% |
| 1000 | 179K | 177K | Tie |
| 5000 | 105K | 99K | SpawnDB +6% |

### Key Optimizations Applied
1. **RESP encoding**: Replaced `fmt.Sprintf` with `strconv.Itoa` in BulkString() — 57% CPU savings
2. **Array encoding**: Inlined BulkString, pre-allocated builder with Grow()
3. **Read buffer**: Increased from 4KB to 16KB
4. **Accumulated buffer**: Pre-allocated to avoid append allocations
5. **Deque Range()**: Bulk copy for contiguous segments

### Analysis
- SpawnDB wins on single-command latency (simpler design, no persistence checks, no ACL)
- Redis wins on pipelining (single-threaded event loop = no lock contention)
- LRANGE now matches Redis after removing fmt.Sprintf bottleneck

---

## 2026-10-16: Post-Milestone B + Pipelining

**Changes:**
- Pipelining support (batched responses)
- RWMutex for read-heavy workloads
- TTL for all types
- Replication for all mutations
- Persistence v2 (lists + streams)

**Benchmarks:** TODO — run on EC2 with same setup

### Expected Improvements
- Pipelining: 2-10x throughput at high pipeline depths
- Read-heavy: Better with RWMutex (concurrent readers)

---

## Template for New Runs

```markdown
## YYYY-MM-DD: <Description>

**Environment:** <instance type, OS, Go version>
**SpawnDB:** Commit `<hash>` (<branch/tag>)
**Redis:** <version>

### Results (<clients> clients, pipeline=<N>, <requests> requests)

| Command | SpawnDB ops/s | Redis ops/s | Ratio |
|---------|---------------|-------------|-------|
| SET | | | |
| GET | | | |

### Observations
- <what changed, why>
```
