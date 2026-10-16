# SpawnDB Architecture Audit

**Date:** 2026-10-16  
**Status:** Milestone A - Audit in progress

---

## 1. Command Execution Path

```
main.go
  └─→ server.StartServer()
        │
        ├─→ storage.NewDataStore()          # Composition root
        │     ├─→ NewTTLManager()           # Lazy expiration
        │     ├─→ NewRegistry(ttl)          # Type enforcement
        │     ├─→ NewStringStore(reg, ttl)
        │     ├─→ NewListStore(reg)
        │     ├─→ NewStreamStore(reg)
        │     └─→ NewKeyLock()              # 256 sharded mutexes
        │
        ├─→ core.InitCommands(...)          # Register command handlers
        │     └─→ Commands use factory pattern: MakeSET(db), MakeGET(db), etc.
        │
        ├─→ net.Listen("tcp", ":6379")
        │
        └─→ for { conn := Accept(); go HandleConnection(conn) }

HandleConnection(conn):
  └─→ loop:
        ├─→ resp.ParseRESP(reader)          # Parse RESP protocol
        ├─→ core.CommandMap[cmdName]        # Lookup handler
        ├─→ Transaction check (queue if MULTI active)
        ├─→ handler(args, client)           # Execute
        └─→ conn.Write(response)            # Send response
```

---

## 2. Lock Hierarchy

### 2.1 KeyLock (Per-Key Atomicity)
```
storage/keylock.go
├── 256 sync.Mutex shards
├── Keys hash to buckets via FNV-1a
└── Prevents TOCTOU races for single-key operations
```

**Used by:** DataStore atomic methods (Set, Get, LPush, etc.)

### 2.2 Store-Level RWMutex
```
stringStore.mu   sync.RWMutex   # Protects data map
listStore.mu     sync.RWMutex   # Protects data map + waiters
streamStore.mu   sync.RWMutex   # Protects data map + waiters
```

### 2.3 Registry/TTL Mutexes
```
typeRegistry.mu  sync.RWMutex   # key → DataType mapping
ttlManager.mu    sync.RWMutex   # key → expiry time
```

### 2.4 Replication Mutex
```
ReplicationState.Mu   sync.RWMutex   # Protects Replicas map, Offset
ReplicaConnection.writeMu  sync.Mutex   # Serializes writes per replica
```

### Lock Acquisition Order (to prevent deadlocks)
```
1. keyLock.Lock(key)      # Per-key lock first
2. store.mu.Lock()        # Then store lock
3. registry.mu / ttl.mu   # Internal locks
```

**⚠️ Potential Issue:** BLPop and XReadBlock do NOT hold keyLock during wait — this is intentional (can't hold lock while blocking), but waiter management must be careful.

---

## 3. Shared Mutable State

| Component | State | Protection | Notes |
|-----------|-------|------------|-------|
| `stringStore.data` | map[string]string | RWMutex | String values |
| `listStore.data` | map[string]*deque.Deque | RWMutex | List data |
| `listStore.waiterReg` | waiterRegistry | internal sync | BLPOP waiters |
| `streamStore.data` | map[string]*stream | RWMutex | Stream entries |
| `streamStore.waiters` | map[string][]*StreamWaiter | internal sync | XREAD BLOCK waiters |
| `typeRegistry.types` | map[string]DataType | RWMutex | Key type bindings |
| `ttlManager.expiry` | map[string]time.Time | RWMutex | TTL tracking |
| `ReplicationState` | Offset, Replicas | RWMutex | Replication state |
| `core.CommandMap` | map[string]CommandHandler | None (read-only after init) | Command registry |

---

## 4. Implemented Commands

| Command | Implemented | Replicates | Persists | Notes |
|---------|-------------|------------|----------|-------|
| PING | ✅ | N/A | N/A | Stateless |
| ECHO | ✅ | N/A | N/A | Stateless |
| INFO | ✅ | N/A | N/A | Stateless |
| SET | ✅ | ✅ | ✅ | With EX/PX TTL |
| GET | ✅ | N/A | N/A | Read-only |
| INCR | ✅ | ✅ | ✅ | |
| DEL | ✅ | ✅ | N/A | Deletes any type |
| TYPE | ✅ | N/A | N/A | Read-only |
| LPUSH | ✅ | ✅ | ❌ **BUG** | Fixed 2026-10-16 |
| RPUSH | ✅ | ✅ | ❌ **BUG** | Fixed 2026-10-16 |
| LPOP | ✅ | ✅ | ❌ **BUG** | Fixed 2026-10-16 |
| BLPOP | ✅ | N/A | N/A | Blocking read |
| LRANGE | ✅ | N/A | N/A | Read-only |
| LLEN | ✅ | N/A | N/A | Read-only |
| XADD | ✅ | ✅ | ❌ **BUG** | Fixed 2026-10-16, sends resolved ID |
| XREAD | ✅ | N/A | N/A | Read-only |
| XRANGE | ✅ | N/A | N/A | Read-only |
| MULTI | ✅ | N/A | N/A | Transaction start |
| EXEC | ✅ | ? | ? | Executes queued |
| DISCARD | ✅ | N/A | N/A | Aborts transaction |
| REPLCONF | ✅ | N/A | N/A | Replication protocol |
| PSYNC | ✅ | N/A | N/A | Replication protocol |
| WAIT | ✅ | N/A | N/A | Wait for replicas |

---

## 5. Critical Defects Found

### 5.1 Replication: Only SET and INCR Propagate ✅ FIXED
**Severity:** HIGH  
**Status:** Fixed 2026-10-16  
**Fix:** Added `propagateToReplicas(args)` to LPUSH, RPUSH, LPOP, XADD  
**Note:** XADD now propagates resolved ID (not `*`) to prevent replica divergence

### 5.2 Persistence: Only Strings Saved ❌
**Severity:** HIGH  
**Location:** `internal/persistence/rdb.go` only calls `db.Strings().ForEach()`  
**Missing:** Lists and Streams are not serialized.  
**Impact:** Data loss on restart for lists/streams.

### 5.3 Transaction Atomicity: Not Guaranteed
**Severity:** MEDIUM  
**Location:** `internal/server/server.go` HandleConnection  
**Issue:** EXEC runs commands sequentially but doesn't hold any lock during execution.  
**Impact:** Other clients can interleave between commands in a transaction.

### 5.4 DEL Command: Not Implemented ✅ FIXED
**Severity:** MEDIUM  
**Status:** Fixed 2026-10-16  
**Fix:** Implemented `MakeDEL(db)` in strings.go, registered in server.go

---

## 6. Known Limitations (Future Optimization)

### 6.1 Replication: Replica Re-validates Commands
**Type:** Performance limitation (not a bug)  
**Current behavior:** Replica receives commands and re-executes them fully, including argument validation, type checks, and all storage logic.  
**Impact:** Redundant CPU work on replicas.

**What Redis does:** Redis uses a hybrid approach where:
1. Master validates, executes, and generates any non-deterministic values
2. Master sends deterministic command (with resolved IDs, etc.) to replica  
3. Replica applies with minimal validation (trusts master)

**Potential fix:** Add `fromReplication bool` flag to command handlers. When `true`, skip validation and apply directly.

**Complexity:** Medium — requires threading flag through command dispatch  
**Priority:** Low — correctness issue was fixed (XADD resolved ID), this is pure optimization

### 6.2 TTL Replication: Relative vs Absolute Timestamps
**Type:** Minor timing drift (not a correctness bug for most use cases)  
**Current behavior:** `SET key val EX 60` propagates as-is. Replica sets TTL relative to its receive time.

**Example drift:**
```
Master:  SET key val EX 60  at T=0      → expires at T+60s
Replica: receives at T=0.1s (100ms lag) → expires at T+60.1s
```

**Impact:** 
- 100ms drift on 60s TTL = 0.17% error (negligible)
- 100ms drift on 1s TTL = 10% error (noticeable for very short TTLs)
- Reading from replica may see key ~100ms longer than master

**What Redis does:** Redis also propagates relative TTL, but master actively sends DEL when keys expire. Replicas don't expire independently.

**Potential fix (simpler):** Convert to absolute timestamp before replication:
```go
// Instead of propagating: SET key val EX 60
// Propagate: SET key val PXAT 1697450060000  (absolute unix ms)
```
This requires:
1. Implementing PXAT option in SET command
2. Converting EX/PX to PXAT before `propagateToReplicas()`

**Complexity:** Low-Medium — need PXAT support in SET, then simple conversion  
**Priority:** Low — drift is usually negligible, Redis has same limitation

### 6.3 Distributed State: TTL + Registry + Store Coordination
**Type:** Architectural trade-off (not a bug after fix)  

**Context — Redis is single-threaded:** Redis executes commands sequentially in one thread. No locks, no races, no coordination needed. The complexity below exists **only because SpawnDB chose multi-threaded execution** to explore parallelism.

**Current design:** Key metadata is split across three data structures:
```
┌─────────────────────────────────────────────────────────────┐
│                       DataStore                              │
├─────────────┬─────────────┬─────────────┬──────────────────┤
│ TTLManager  │ Registry    │ ListStore   │ StreamStore      │
│ (expiry)    │ (key→type)  │ (key→data)  │ (key→data)       │
│ [mutex]     │ [mutex]     │ [mutex]     │ [mutex]          │
└─────────────┴─────────────┴─────────────┴──────────────────┘
```

**Challenge:** Lazy expiry requires coordinating deletions across all three:
1. TTLManager.CheckAndDelete() — removes expiry entry
2. Store.data — removes actual data  
3. Registry — removes type binding

**Race condition found and fixed (2026-10-16):**
```
Thread A (checkExpiry)              Thread B (LPush same key)
──────────────────────              ─────────────────────────
ttl.CheckAndDelete() → true         
                                    checkExpiry() → false (TTL gone)
                                    registry.Acquire() → registers key
delete(l.data, key)
registry.Release(key)               ← Would delete Thread B's registration!
```

**Fix applied:** Move `registry.Release()` inside store lock:
```go
l.mu.Lock()
_, existed := l.data[key]
if existed {
    delete(l.data, key)
    l.registry.Release(key)  // Atomic: delete data + unregister
}
l.mu.Unlock()
```

**Lock order (deadlock-free):** TTL → Store → Registry (always acquired in this order)

**Alternative design (Redis approach):** Unified key entry:
```go
type KeyEntry struct {
    Type   DataType
    Expiry time.Time     // 0 = no TTL
    Data   interface{}   // string | *Deque | *Stream
}
// Single map, single lock per key
```

**Trade-offs:**
| Aspect | Current (Distributed) | Alternative (Unified) |
|--------|----------------------|----------------------|
| Lock contention | Lower (sharded) | Higher |
| Race complexity | Higher | Lower |
| Code complexity | Higher | Lower |
| Testability | Higher (isolated stores) | Lower |
| Type safety | Higher | Lower (interface{}) |

**Decision:** Keep current design. The coordination complexity is manageable and the separation makes testing and reasoning about individual stores easier. Document the lock ordering invariant.

**SpawnDB vs Redis concurrency models:**
| Aspect | Redis | SpawnDB |
|--------|-------|---------|
| Command execution | Single-threaded event loop | Goroutine per connection |
| Parallelism | None (sequential) | True parallel execution |
| Locking | Not needed | Required everywhere |
| Race conditions | Impossible by design | Must be carefully prevented |
| Multi-core utilization | Limited (I/O only) | Full |
| Code complexity | Lower | Higher |

SpawnDB's multi-threaded design is intentional — it explores whether parallelism can outperform Redis's simplicity on multi-core systems.

### 6.4 Persistence: Snapshot-Keys vs Full-Lock Trade-off
**Type:** Design decision (documented 2026-10-16)

**Context:** The `ForEach` method iterates over all keys for RDB persistence. Two approaches considered:

**Option A: Full Lock (Simple)**
```go
func ForEach(fn func(...)) {
    s.mu.RLock()
    defer s.mu.RUnlock()  // Held entire iteration
    for key, value := range s.data {
        fn(key, value, ...)
    }
}
```
- ✅ Perfect point-in-time snapshot
- ❌ Blocks ALL writes during save (O(n) lock time)
- ❌ Save freezes database for large datasets

**Option B: Snapshot-Keys (Chosen)**
```go
func ForEach(fn func(...)) {
    // Brief lock: get keys only
    s.mu.RLock()
    keys := make([]string, 0, len(s.data))
    for k := range s.data { keys = append(keys, k) }
    s.mu.RUnlock()  // Released!

    // Per-key brief locks
    for _, key := range keys {
        s.mu.RLock()
        data := s.data[key]  // Copy
        s.mu.RUnlock()
        fn(key, data, ...)   // Callback without lock
    }
}
```
- ✅ Writers only blocked briefly per key (O(1) per key)
- ✅ Save doesn't freeze database
- ⚠️ Not a perfect snapshot:
  - Keys added during save may be missed (acceptable: they're new)
  - Keys deleted during save are skipped safely
  - Keys modified get either old or new value (both valid states)

**What Redis does:** Forks process for RDB save — OS copy-on-write gives perfect snapshot without blocking. Not portable (no fork on Windows), more complex.

**Decision:** Use snapshot-keys approach for all stores (StringStore, ListStore, StreamStore). The "imperfect" snapshot is acceptable because:
1. Missed new keys aren't data loss
2. We skip expired keys on load anyway
3. Concurrent writes during save are rare in practice
4. Interview talking point: "Optimized persistence to not block writes"

**Workload assumption:** This approach optimizes for databases that remain responsive during saves:
- Write-heavy workloads (chat, logs, queues) — writes don't block
- Read-heavy workloads (cache, sessions) — reads always fast
- Large datasets — no latency spike proportional to data size

**When full-lock would be better:**
- Small datasets where save is <10ms
- Need transactional consistency (perfect point-in-time)
- Scheduled saves during low traffic (blocking acceptable)

### 6.5 RWMutex vs Mutex Trade-off
**Type:** Design decision (documented 2026-10-16)

**Context:** SpawnDB uses `sync.RWMutex` everywhere (KeyLock, stores, registry, TTL). This has performance implications.

**Performance comparison:**
| Aspect | `sync.Mutex` | `sync.RWMutex` |
|--------|--------------|----------------|
| Memory | 8 bytes | 24 bytes (3x) |
| Lock() speed | ~20ns | ~30-40ns (slower) |
| RLock() speed | N/A | ~25-30ns |
| Concurrent reads | ❌ No | ✅ Yes |

**Trade-off:** RWMutex makes writes slower (~50-100% overhead) to enable concurrent reads.

**When RWMutex hurts (write-heavy, same key):**
```
99% SET, 1% GET on same key:
- Extra lock overhead on every write
- Minimal benefit from concurrent reads
```

**When RWMutex helps (read-heavy, same key):**
```
99% GET, 1% SET on same key:
- 100 concurrent GETs run in parallel (not serialized)
- Potential 80x+ throughput improvement for reads
```

**Decision:** Use RWMutex because:
1. Most real database workloads are read-heavy (80-95% reads typical)
2. SpawnDB targets cache/session use cases
3. Sharded KeyLock (256 shards) mitigates write contention across different keys
4. Matches what other stores (StringStore, ListStore, etc.) already use

**Optimal workloads for current design:**
| Workload | Performance |
|----------|-------------|
| Read-heavy, same key | ⭐⭐⭐⭐⭐ Excellent |
| Read-heavy, many keys | ⭐⭐⭐⭐⭐ Excellent |
| Write-heavy, many keys | ⭐⭐⭐⭐ Good (sharding helps) |
| Write-heavy, same key | ⭐⭐ Fair (unavoidable serialization) |

---

## 7. Test Coverage

```
Package                    Tests    Race-safe
commands                   ✅       ✅
persistence                ✅       ✅
replication                ✅       ✅
resp                       ✅       ✅
storage                    ✅       ✅ (216s with race detector)
utilities/deque            ✅       ✅
```

**Missing tests:**
- Server integration tests
- Transaction isolation tests
- Replication propagation tests
- Persistence for lists/streams

---

## 8. Component Dependencies

```
     ┌─────────┐
     │  main   │
     └────┬────┘
          │
          ▼
     ┌─────────┐
     │ server  │──────────────┐
     └────┬────┘              │
          │                   │
          ▼                   ▼
     ┌─────────┐        ┌───────────┐
     │commands │◄───────│   core    │
     └────┬────┘        └───────────┘
          │                   ▲
          ▼                   │
     ┌─────────┐              │
     │ storage │──────────────┘
     └────┬────┘
          │
    ┌─────┴─────┐
    ▼           ▼
┌──────┐   ┌─────────────┐
│ resp │   │ replication │
└──────┘   └─────────────┘
```

---

## 9. Blocking Operations Audit

### BLPOP
- **Registration:** Creates `waiter` struct, registers on all keys via `waiterReg.Add()`
- **Notification:** LPush/RPush call `pushLocked()` which claims waiter atomically
- **Cancellation:** On timeout, claims self, removes from registry
- **Double-delivery protection:** `waiter.claim()` uses atomic.Bool
- **Status:** ✅ Appears correct after Sep 2026 fixes

### XREAD BLOCK
- **Registration:** Creates `StreamWaiter`, registers on all streams
- **Notification:** XAdd calls `notifyWaiters()` with `sync.Once`
- **Cancellation:** Uses context or timeout channel
- **Status:** ✅ Has `closeOnce` to prevent double-close panic

---

## 10. Prioritized Remediation Plan

### Priority 1: Data Integrity (Must Fix)

#### 1.1 Fix Replication Propagation
**Defect:** LPUSH, RPUSH, LPOP, XADD don't replicate  
**Impact:** Replicas silently diverge  
**Fix:**
```go
// In lists.go commands, add after successful operation:
propagateToReplicas(args)

// In streams.go XADD, add after successful operation:
propagateToReplicas(args)
```
**Acceptance:** All mutating commands appear in replica after WAIT

#### 1.2 Fix Persistence Coverage
**Defect:** Only strings persisted  
**Impact:** Lists/Streams lost on restart  
**Fix:** Extend `rdb.go` to serialize:
- Lists: key → []string (or deque serialized)
- Streams: key → []StreamEntry

**Acceptance:** `Save` → kill → `Load` preserves all data types

#### 1.3 Implement DEL Command
**Defect:** No way to delete keys  
**Impact:** Memory grows forever  
**Fix:** Add `MakeDEL(db)` that calls `db.Delete(key)`  
**Acceptance:** `DEL key` returns 1, subsequent GET returns nil

---

### Priority 2: Correctness (Should Fix)

#### 2.1 Document Transaction Semantics
**Issue:** EXEC doesn't guarantee atomicity  
**Decision needed:** Do we want Redis-style (no isolation) or stronger?  
**Options:**
1. Document current behavior (no isolation, matches Redis)
2. Add global lock during EXEC (simple but serializes all transactions)
3. Per-key locks held during EXEC (complex)

**Recommendation:** Option 1 — document that SpawnDB transactions match Redis (no isolation, just batching)

#### 2.2 Add Missing Error Cases
- WRONGTYPE for all commands (mostly done)
- Argument validation edge cases

---

### Priority 3: Features (Nice to Have)

#### 3.1 Pipelining
**Gap:** 5-6x slower than Redis with P=16  
**Fix:** Buffer responses, write in batch  
**Complexity:** Medium

#### 3.2 More Commands
- EXPIRE, TTL (TTL infrastructure exists)
- MGET, MSET (batch operations)
- KEYS, SCAN (iteration)

---

### Execution Order

| Week | Tasks | Owner |
|------|-------|-------|
| 1 | 1.1 Replication + 1.3 DEL | 🤝 AI+You |
| 1 | 1.2 Persistence | 🤝 AI+You |
| 2 | 2.1 Transaction docs | 👤 You |
| 2 | Integration tests | 🤖 AI |
| 3 | 3.1 Pipelining | 🤝 AI+You |

---

## 11. Summary

**What Works:**
- Core command execution pipeline ✅
- Per-key locking (KeyLock) ✅
- Type registry (WRONGTYPE) ✅
- Lazy TTL expiration ✅
- BLPOP/XREAD BLOCK waiter handling ✅
- All tests pass with race detector ✅
- Replication for all mutating commands ✅ (fixed 2026-10-16)
- DEL command ✅ (added 2026-10-16)

**What's Broken:**
- Persistence only covers strings ❌
- Transaction isolation not guaranteed (but matches Redis) ⚠️

**Known Limitations (Future Optimization):**
- Replica re-validates commands (redundant work)

**Recommended Next Action:**
Fix persistence to include lists and streams — this is now the highest-impact bug (data loss on restart).
