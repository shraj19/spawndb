# SpawnDB Development Notes

Development history, design decisions, and war stories for interview prep.

---

## Major Refactoring (Sep 2026)

### Storage Layer Rewrite
Migrated from global variables to clean architecture:
- `DataStore` facade as composition root
- Interface-based design (`StringStore`, `ListStore`, `StreamStore`)
- Per-key sharded locking (256 buckets)
- Type registry for WRONGTYPE enforcement

### Command Factory Pattern
Moved from global state to dependency injection via closures:
```go
// Before: global access
func SET(args []string, client *Client) string {
    storage.Store[args[1]] = args[2]  // global!
}

// After: injected via factory
func MakeSET(db DataStore) CommandHandler {
    return func(args []string, client *Client) string {
        db.Set(args[1], args[2])  // injected
    }
}
```

### Package Reorganization
Created `internal/core/` for shared types to break import cycles:
- `server → commands → replication → server` cycle broken
- `core` has zero internal dependencies

---

## Performance Optimization (Oct 2026)

### LPUSH 132x Improvement
**Problem:** LPUSH was 83x slower than Redis (1,554 vs 128,388 ops/sec).

**Root Cause:** Slice-based list with O(n) prepend:
```go
// Before: O(n) - copies entire slice
list = append([]string{value}, list...)
```

**Solution:** Deque with O(1) operations:
```go
// After: O(1) - ring buffer
deque.PushFront(value)
```

**Result:** 1,554 → 205,804 ops/sec (132x faster)

### Benchmark Methodology Fix
**Problem:** Initial benchmarks showed SpawnDB beating Redis — suspicious.

**Root Cause:** Unequal network latency (SpawnDB 0.35ms RTT, Redis 0.74ms RTT from loadgen).

**Solution:** Localhost benchmarks for fair comparison.

**Corrected Results:**
- Basic ops: 96% of Redis (not 167% as initially measured)
- Pipelining: 17% of Redis (major gap identified)

---

## War Stories (STAR Format)

### 1. LRANGE Race Condition
**S:** LRANGE reading list while another goroutine modifies it.
**T:** Fix data race without hurting read performance.
**A:** Added `RLock` — lock was completely missing. Concurrent reads still allowed.
**R:** Race detector clean. Zero perf impact for read-heavy workloads.

### 2. BLPOP Deadlock
**S:** BLPOP registers waiter on multiple keys. Concurrent LPUSH on two keys both try to send on same channel while holding lock → deadlock.
**T:** Fix deadlock without losing notifications.
**A:** Collect-then-deliver pattern: release lock before sending on channels.
**R:** Deadlock eliminated. Added regression tests.

### 3. Stream Waiter Double-Close Panic
**S:** XREAD BLOCK on [a, b]. Concurrent XADD on both → both try to `close(w.Done)` → panic.
**T:** Prevent double-close while ensuring notification.
**A:** Added `sync.Once` to waiter struct. First close wins, subsequent calls are no-ops.
**R:** Panic eliminated. Single notification guaranteed.

### 4. WRONGTYPE Bug
**S:** Same key could exist in both strings AND lists maps simultaneously.
**T:** Match Redis behavior: one type per key.
**A:** Added `TypeRegistry.Acquire()` — all stores call before write. Returns error if type mismatch.
**R:** Type collision impossible. Matches Redis exactly.

### 5. Replica Write Corruption
**S:** Multiple goroutines calling `conn.Write()` on same replica connection → interleaved bytes → corrupted RESP.
**T:** Serialize writes without global lock.
**A:** Added per-replica `writeMu`. All writes go through `rep.Write()` method.
**R:** RESP framing guaranteed intact under concurrent commands.

### 6. Network Bias in Benchmarks
**S:** SpawnDB appeared 67% faster than Redis on hot-key test.
**T:** Validate suspicious result.
**A:** Measured network RTT — SpawnDB instance was 2x closer to loadgen. Switched to localhost benchmarks.
**R:** True result: Redis 7% faster (expected due to no lock overhead). Lesson: always validate benchmark conditions.

---

## Design Decisions

### Why Sharded Locks (not Global Lock)?
- Global lock: simple but bottleneck at scale
- Per-key lock: memory overhead (1 mutex per key)
- **Sharded (256 buckets):** best of both — parallel access to different keys, fixed memory

### Why Lazy TTL (not Background Expiry)?
- Background goroutine competes for locks
- Lazy expiry: check on access, clean then
- Tradeoff: expired-but-unaccessed keys linger until accessed
- Simpler, less contention, good enough for most workloads

### Why Factory Pattern (not Global State)?
- Testable: create isolated DataStore per test
- No global state to reset between tests
- Explicit dependencies: clear what each command needs
- Go-idiomatic: functions returning functions is common

---

## Known Gaps

### Pipelining (5-6x slower than Redis)
Current: parse command → execute → write response → repeat
Needed: buffer commands → execute batch → write all responses
Impact: Would dramatically improve throughput for batched workloads

### Large Values (40% slower than Redis)
Cause: Go allocator vs Redis's jemalloc
Potential fix: `sync.Pool` for buffer reuse

### Tail Latency (3-5x worse p99)
Cause: Go GC pauses
Potential fix: GOGC tuning, reduce allocations in hot path

---

## Deferred Issues

### Replication Only Propagates SET/INCR
Other write commands (LPUSH, XADD, etc.) don't propagate to replicas. Silent divergence.

**Why deferred:** Needs comprehensive design for:
- Transactions (propagate at EXEC time)
- Blocking ops (LPUSH that triggers BLPOP needs synthetic LPOP)
- Auto-generated IDs (XADD * must propagate resolved ID)

### Replication Offset Mismatch
Master and replica count different bytes → WAIT never satisfied.
Blocked by: need to fix what commands propagate first.
