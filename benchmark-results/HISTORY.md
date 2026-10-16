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
