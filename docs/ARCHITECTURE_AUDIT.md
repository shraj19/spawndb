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
| TYPE | ✅ | N/A | N/A | Read-only |
| LPUSH | ✅ | ❌ **BUG** | ❌ **BUG** | Not propagated! |
| RPUSH | ✅ | ❌ **BUG** | ❌ **BUG** | Not propagated! |
| LPOP | ✅ | ❌ **BUG** | ❌ **BUG** | Not propagated! |
| BLPOP | ✅ | N/A | N/A | Blocking read |
| LRANGE | ✅ | N/A | N/A | Read-only |
| LLEN | ✅ | N/A | N/A | Read-only |
| XADD | ✅ | ❌ **BUG** | ❌ **BUG** | Not propagated! |
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

### 5.1 Replication: Only SET and INCR Propagate ❌
**Severity:** HIGH  
**Location:** `internal/commands/strings.go` has `propagateToReplicas(args)`  
**Missing:** Lists (LPUSH, RPUSH, LPOP) and Streams (XADD) don't call it.  
**Impact:** Replicas silently diverge from master.

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

### 5.4 DEL Command: Not Implemented
**Severity:** MEDIUM  
**Location:** Not registered in server.go  
**Impact:** Cannot delete keys.

---

## 6. Test Coverage

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

## 7. Component Dependencies

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

## 8. Blocking Operations Audit

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

## 9. Prioritized Remediation Plan

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

## 10. Summary

**What Works:**
- Core command execution pipeline ✅
- Per-key locking (KeyLock) ✅
- Type registry (WRONGTYPE) ✅
- Lazy TTL expiration ✅
- BLPOP/XREAD BLOCK waiter handling ✅
- All tests pass with race detector ✅

**What's Broken:**
- Replication only covers SET/INCR ❌
- Persistence only covers strings ❌
- DEL not implemented ❌
- Transaction isolation not guaranteed (but matches Redis) ⚠️

**Recommended Next Action:**
Fix replication propagation for LPUSH/RPUSH/LPOP/XADD — this is the highest-impact bug (silent data divergence).
