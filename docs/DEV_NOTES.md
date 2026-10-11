# FlashDB Development Notes

Tracks ongoing refactoring, design decisions, and next steps.

---

## Recent Commits (Sep 8-11, 2026)

```
169073f refactor: move Client to core, ReplicationState to replication package
ad5888a refactor(persistence): migrate to DataStore, delete legacy Database struct
9e7c7db docs: add testing philosophy and contract testing guide to DEV_NOTES
7376954 refactor: delete legacy handlers and globals, keep only factory-based commands
f5e3b32 test(commands): add contract tests for factory-based command handlers
12f529e docs: update DEV_NOTES with factory pattern decision and war stories
04c0e3a refactor(commands): migrate to DataStore via factory pattern
8296f45 test(storage): add tests for DataStore facade
e07cb3f refactor(storage): implement DataStore facade as composition root
01d2f80 test(storage): add tests for StreamStore
f8d79e1 refactor(storage): implement StreamStore with waiters and blocking reads
bf8ebd3 test(storage): add tests for TTL, Registry, StringStore, ListStore
c3340e6 refactor(storage): implement ListStore with waiters and BLPop multi-key fix
10a7aaf refactor(storage): implement StringStore with type safety and lazy expiry
80f6022 refactor(storage): add TTLManager and TypeRegistry with lazy expiry
e059b25 test: add table-driven tests for stream helpers
9aeaa34 refactor: extract helpers from streams, improve SRP
6630216 fix: add RLock to LRANGE to prevent race condition
4a5235d refactor: add RESP helpers, decouple handlers from wire format
```

---

## Completed Refactoring

### RESP Helpers (`internal/resp/resp.go`)
Commands no longer know RESP encoding details. Added:
- `SimpleString`, `OK`, `PONG`
- `Error`
- `BulkString`, `NullBulkString`
- `Integer`
- `Array`, `EmptyArray`, `NullArray`

### Command Handlers Refactored
All handlers now use RESP helpers instead of manual `fmt.Sprintf`:
- `general.go` — PING, ECHO, INFO, WAIT, REPLCONF
- `strings.go` — SET, GET, TYPE, INCR
- `transactions.go` — MULTI, EXEC, DISCARD
- `lists.go` — all list commands
- `streams.go` — XADD, XRANGE, XREAD

### Streams SRP Refactor (`internal/commands/streams.go`)
Extracted helpers following single responsibility:
- **ID helpers:** `resolveStreamID`, `parseID`
- **Stream helpers:** `getOrCreateStream`, `getLastID`
- **Encoding:** `encodeStreamEntry`, `encodeStreamEntries`, `encodeStreamResult`
- **Search:** `searchEntryRange`, `searchEntryAfter` (binary search)
- **Waiters:** `notifyWaiters`, `unregisterWaiter`
- **XREAD-specific:** `parseXREADArgs`, `resolveDollarIDs`, `queryStreams`, `encodeXREADResponse`

### Tests Added
- `streams_test.go` — 56 table-driven test cases:
  - `TestResolveStreamID` (18 cases)
  - `TestParseID` (4 cases)
  - `TestSearchEntryRange` (9 cases)
  - `TestSearchEntryAfter` (7 cases)
  - `TestParseXREADArgs` (11 cases)
  - `TestQueryStreams` (7 cases)

### Bug Fixes
- LRANGE race condition — added `RLock` (was missing lock entirely)

---

## Uncommitted Changes

Files modified but not yet committed:
- `README.md` — fixed inaccurate command list, architecture diagram
- `general.go`, `strings.go`, `server.go` — RESP helper usage
- `utilities.go` — removed unused search functions

**TODO:** Review and commit these.

---

## Next: Storage Refactoring

### Current Problem
Split-brain architecture:
- `database.go` — new encapsulated `Database` struct (partially used)
- `storage.go` — old globals (`Store`, `Expiry`, `Streams`, `Mu`) still in use

Commands mix both patterns. Need to consolidate.

### Design Decision: Interface-Based (SOLID)

**Interfaces** (commands depend on these, not concrete types):
```go
type StringStore interface {
    Get(key string) (string, bool)
    Set(key, value string)
    SetWithTTL(key, value string, ttl time.Duration)
    Delete(key string) bool
    IncrBy(key string, delta int64) (int64, error)
    // ...
}

type ListStore interface {
    LPush(key string, values ...string) int
    RPush(key string, values ...string) int
    LRange(key string, start, stop int) []string
    BLPop(keys []string, timeout time.Duration) (key, value string, ok bool)
    // ...
}

type StreamStore interface {
    XAdd(key, id string, fields map[string]string) (string, error)
    XRange(key, start, end string) []StreamEntry
    XRead(keys, ids []string) map[string][]StreamEntry
    // ...
}
```

**File structure:**
```
storage/
├── interfaces.go      # All interfaces
├── types.go           # StreamEntry, TTLResult, Client, etc.
├── strings.go         # StringStore impl
├── lists.go           # ListStore impl (waiters inside)
├── streams.go         # StreamStore impl (StreamWaiters inside)
├── ttl.go             # TTLManager + active expiry goroutine
└── database.go        # Facade composing all stores
```

**Locking strategy:**
- Phase 1: One mutex per store (strings, lists, streams independent)
- Phase 2 (future): Sharded locks within a store (hash % N buckets)

**TTL scope:** Currently strings-only (`SET key value EX/PX`). TTLManager injected into StringStore.

**Waiters:** Live inside their respective stores (ListStore has list waiters, StreamStore has stream waiters). Tightly coupled to data.

### Migration Order
1. `interfaces.go` + `types.go` — define contracts
2. `strings.go` — migrate (mostly done in database.go)
3. `lists.go` — migrate with waiters
4. `streams.go` — migrate with StreamWaiters
5. Update commands to accept interfaces
6. Delete old globals

---

## Storage Refactoring (In Progress)

### Design Decisions Made

**1. Type Registry + TTLManager (SRP)**
- `TTLManager` — only tracks key → expiry
- `TypeRegistry` — only tracks key → type, depends on TTLManager
- Avoids coupling two responsibilities in one struct

**2. Lazy Expiry (Memcached style)**
- No background goroutine
- Check and cleanup on every access
- Less lock contention, simpler, embeddable

**3. WRONGTYPE Enforcement**
- Current bug: same key can exist in strings AND lists (separate maps)
- Fix: Registry.Acquire() checks type before any operation
- Matches Redis behavior

### Completed Steps

**Step 1: types.go + interfaces.go**
- `DataType` enum (TypeString, TypeList, TypeStream)
- Errors: `ErrWrongType`, `ErrNotFound`, `ErrNotInt`
- Interfaces: `Registry`, `StringStore`, `ListStore`, `StreamStore`, `DataStore`

**Step 2: ttl.go + registry.go**
- `TTLManager` — Set, SetTTL, Get, IsExpired, CheckAndDelete, Remaining, Snapshot
- `typeRegistry` — Acquire, Release, SetTTL, GetTTL, Type, Keys
- Dependency injection: Registry depends on TTLManager

**Step 3: strings.go (StringStore)**
- Implements full StringStore interface
- Lazy expiry on Get, Set, Exists, IncrBy
- Type registration via Registry.Acquire()
- ForEach/Load for persistence layer
- Snapshot-based TTL iteration (avoids O(n) lock acquisitions)

**Step 4: lists.go (ListStore)**
- Implements full ListStore interface
- Waiters live inside the store (not global)
- Type safety via Registry.Acquire() on writes
- LRange returns copy to avoid exposing internal slice
- BLPop with timeout, multi-key support, proper waiter cleanup

**Step 5: streams.go (StreamStore)**
- Implements full StreamStore interface
- XAdd, XRange, XRead, XReadBlock
- Waiters inside store (StreamWaiters)
- resolveReadIDs, queryStreams helpers
- Deep copy on XRange to prevent mutation

**Step 6: datastore.go (DataStore facade)**
- Composition root: `NewDataStore()` wires TTL → Registry → all stores
- Polymorphic Delete (checks type, delegates to correct store)
- Type() and Exists() facade methods
- 6 tests covering composition, type safety, cross-store interaction

**Step 7: Command migration to DataStore (DONE)**
- Created factory functions: `MakeSET`, `MakeGET`, `MakeTYPE`, `MakeINCR`
- Created factory functions: `MakeRPUSH`, `MakeLPUSH`, `MakeLRANGE`, `MakeLLEN`, `MakeLPOP`, `MakeBLPOP`
- Created factory functions: `MakeXADD`, `MakeXRANGE`, `MakeXREAD`
- Updated server.go to create DataStore and use factories
- Deleted legacy handlers (Step 8)

**Step 8: Delete legacy handlers (DONE)**
- Removed ~1100 lines of legacy handler code
- Deleted redundant test files (`strings_test.go`, `lists_test.go`)
- Only factory-based commands remain

**Step 9: Persistence migration (DONE)**
- Updated `rdb.go` to use `DataStore` interface
- Deleted old `database.go` (legacy Database struct)
- Rewrote `rdb_test.go` to use DataStore

**Step 10: Package reorganization (DONE)**
- Created `internal/core/` package for shared types
- Moved `Client`, `CommandHandler`, `CommandMap` from storage → core
- Moved `ReplicationState`, `ReplicaConnection` from storage → replication
- Deleted empty `storage/storage.go`
- Broke import cycle: server → commands → replication → server

### Remaining Steps
None — storage refactoring complete!

---

## Design Decision: Command Factory Pattern (Sep 11)

### Problem
Command handlers have signature `func(args []string, client *Client) string`.
No place to pass `DataStore` dependency.

### Solution: Factory Pattern with Closure Capture

```go
func MakeSET(db storage.DataStore) storage.CommandHandler {
    return func(args []string, client *storage.Client) string {
        db.Strings().Set(args[1], args[2])  // db captured in closure
        return resp.OK()
    }
}

// Server wires it once at startup
db := storage.NewDataStore()
commands["SET"] = MakeSET(db)
```

### Alternatives Considered

| Alternative | Pros | Cons | Decision |
|-------------|------|------|----------|
| **Change handler signature** | Explicit dependency | Changes ALL call sites (server, EXEC, replication) | ❌ Too invasive |
| **Global DataStore** | Zero changes | Back to global state, untestable | ❌ Defeats purpose |
| **Context struct** | Extensible | Still changes signature | ❌ Same invasion |
| **Method on struct** | OOP style | Verbose, un-Go-like | ❌ Not idiomatic |
| **Factory pattern** ✓ | Minimal changes, testable, no globals | Slight verbosity | ✅ Chosen |

### Why Factory Won
- Handler signature unchanged — no changes to dispatch, EXEC, replication
- Testable — create isolated DataStore per test, no global reset
- No globals — DataStore captured in closure, not package-level var
- Go idiomatic — functions returning functions is common pattern

### Testing Benefits

```go
func TestSET(t *testing.T) {
    db := storage.NewDataStore()  // isolated per test
    handler := MakeSET(db)
    
    handler([]string{"SET", "key", "value"}, nil)
    
    val, _ := db.Strings().Get("key")
    assert.Equal(t, "value", val)  // no global state to reset
}
```

---

## War Stories (STAR ammo)

### LRANGE Race Condition (Sep 9)
**S:** LRANGE command reading list while another goroutine modifies.
**T:** Fix data race without hurting read performance.
**A:** Added `RLock` — was completely missing lock. Concurrent reads still allowed.
**R:** Race detector clean. Zero perf impact for read-heavy workloads.

### Replica Offset Drift
_(TODO: mine from commits)_

### RESP Encoding Coupling
**S:** Every command handler had `fmt.Sprintf("$%d\r\n%s\r\n", ...)` scattered throughout.
**T:** Decouple commands from wire format for maintainability and correctness.
**A:** Created `resp` helpers, refactored all handlers to use them.
**R:** ~40% less code in handlers, single source of truth for encoding, easier to test commands in isolation.

### TTL Design: Redis vs Memcached (Sep 10)
**S:** Needed to decide TTL strategy — Redis uses lazy + active expiry, Memcached uses lazy only.
**T:** Choose approach that fits multi-threaded design with per-store locks.
**A:** Chose Memcached style (lazy only). No background goroutine competing for locks. Cleanup happens on access via `CheckAndDelete()`.
**R:** Simpler code, less lock contention, easier to embed. Tradeoff: expired-but-unaccessed keys stay in memory until accessed or evicted.

### SRP Violation Caught: Entry Struct (Sep 10)
**S:** Initial design had `entry{typ, expiry}` combining type and TTL.
**T:** Reviewer (self) flagged SRP violation — two responsibilities in one struct.
**A:** Split into separate `TTLManager` and `TypeRegistry`. Registry depends on TTLManager via injection.
**R:** Each component has single responsibility. Can modify TTL logic without touching type logic. Better testability.

### WRONGTYPE Bug Discovery (Sep 10)
**S:** Discovered same key could exist in both `Store` (strings) and `Lists` maps simultaneously.
**T:** Fix cross-store type collision to match Redis behavior.
**A:** Added `Registry.Acquire()` that all stores call before write. Returns `ErrWrongType` if key exists with different type.
**R:** Impossible to have type collision. Matches Redis: "WRONGTYPE Operation against a key holding the wrong kind of value"

### Waiters Encapsulation (Sep 10)
**S:** List waiters (for BLPOP) were global `map[string][]chan` in storage.go, separate from list data.
**T:** Encapsulate waiters with their data following SRP — waiters are tightly coupled to list operations.
**A:** Moved waiters inside `listStore` struct. LPUSH/RPUSH wake waiters before appending. BLPop registers waiter and blocks.
**R:** Self-contained ListStore. No global state. Easier to test, mock, and reason about.

### LRange Slice Safety (Sep 10)
**S:** Original LRange returned slice of internal data — callers could mutate store's data.
**T:** Prevent accidental mutation of internal state.
**A:** LRange now returns `copy(result, list[start:stop+1])` instead of direct slice.
**R:** Defensive copy. Internal data is never exposed. Small overhead, but worth it for safety.

### BLPop Multi-Key Bug (Sep 10)
**S:** BLPOP supports multiple keys (priority order) but original impl only registered waiter on first key.
**T:** If user does `BLPOP high medium low 0` and `low` gets data first, should unblock.
**A:** Register waiter channel on ALL keys. On wake or timeout, remove from ALL keys.
**R:** Correct priority queue behavior. Test `TestListStore_BLPop_MultiKeyBlocking` verifies the fix.

### XReadBlock $ Resolution Race (Sep 11)
**S:** `$` in XREAD means "only new entries." Must resolve to last ID atomically with waiter registration.
**T:** If resolved in command layer, race window exists: entry added between resolve and register → missed.
**A:** Keep `$` resolution inside `XReadBlock`, under the same lock as waiter registration. Extract to `resolveReadIDs` helper for readability.
**R:** Single lock acquisition, no race. Small design sacrifice (store knows about `$` syntax) for correctness.

### ForEach O(n) Lock Problem (Sep 10)
**S:** `ForEach` for persistence was calling `ttl.IsExpired(key)` per key — O(n) lock acquisitions while holding store lock.
**T:** Reduce lock contention during RDB save.
**A:** Added `TTLManager.Snapshot()` — copies all expiry data in one lock. ForEach uses snapshot.
**R:** 2 locks total instead of 2n. Save operation no longer hammers TTL mutex.

### DataStore as Composition Root (Sep 11)
**S:** New stores built (StringStore, ListStore, StreamStore) but no way to wire them together.
**T:** Need single entry point that assembles all dependencies correctly.
**A:** Created `NewDataStore()` as composition root. Wires: TTLManager → Registry → all stores. Returns `DataStore` interface.
**R:** One place to construct entire storage layer. Commands receive interface, not concrete types. Testable with mock if needed.

### Import Cycle Resolution (Sep 11)
**S:** Moving `Client` to server and `ReplicationState` to replication created import cycle: server → commands → replication → server.
**T:** Break the cycle without major restructuring.
**A:** Created `internal/core/` package for shared types (`Client`, `CommandHandler`, `CommandMap`). All three packages import core, none import each other directly.
**R:** Clean dependency graph. `core` has zero internal dependencies. Pattern is standard Go approach for shared types.

### BLPOP Deadlock + Double-Delivery Fix (Sep 11)
**S:** BLPOP registers one waitCh on multiple keys. LPush/RPush sent on waiter channels *while holding l.mu*. If two keys receive concurrent pushes, second send blocks (buffer full), holding l.mu forever → **total deadlock**. Also, waiter was only removed from one key's list, so second key's push could double-deliver.
**T:** Fix three bugs: (1) deadlock from send-under-lock, (2) double-delivery from incomplete waiter removal, (3) wrong return count (included delivered items).
**A:** Restructured LPush/RPush to collect-then-deliver pattern:
```go
// BEFORE (buggy)
l.mu.Lock()
defer l.mu.Unlock()
ch <- [2]string{key, v}  // BLOCKS while holding lock!

// AFTER (fixed)
l.mu.Lock()
var toDeliver []delivery
for ... {
    l.removeWaiterFromAllKeys(ch)  // Remove from ALL keys atomically
    toDeliver = append(toDeliver, delivery{ch, val})
}
l.mu.Unlock()  // Release BEFORE sending

for _, d := range toDeliver {
    select {
    case d.ch <- d.val:  // Non-blocking
    default:  // Re-add to list if delivery failed
    }
}
return len(list)  // Not len(list)+delivered
```
Key changes:
1. **Unlock before send** — prevents deadlock
2. **removeWaiterFromAllKeys()** — prevents double-delivery 
3. **Return len(list)** — matches Redis (delivered items aren't in list)
**R:** All three bugs fixed. Added tests for deadlock scenario, double-delivery, and return count. Tests pass reliably.

### Per-Key Lock for Cross-Store Atomicity (Sep 11)
**S:** Operations like Set() span multiple locks (ttl.mu → registry.mu → store.mu). Race window between each step allows TOCTOU bugs:
- SET k and RPUSH k both pass Acquire() before either writes data → type confusion
- Delete runs between Set's Acquire and data write → orphaned data
**T:** Make single-key operations atomic to prevent cross-store races.
**A:** Created `KeyLock` with 256 sharded mutexes. Keys hash to buckets; same-bucket keys serialize. Added atomic methods to DataStore:
```go
type KeyLock struct {
    shards [256]sync.Mutex
    mask   uint32
}

func (d *dataStore) Set(key, value string) error {
    d.keyLock.Lock(key)       // Lock THIS key's bucket
    defer d.keyLock.Unlock(key)
    return d.strings.Set(key, value)  // Entire operation atomic
}
```
Benefits:
- **Atomicity:** Registry check + data write happen under one lock
- **Parallelism preserved:** Different keys (different buckets) proceed in parallel
- **Memory efficient:** 256 mutexes ≈ 10KB regardless of key count
**R:** TOCTOU tests pass. Orphan data bug eliminated. Type confusion impossible through atomic DataStore methods.

### Replica Write Serialization (Sep 11)
**S:** `propagateToReplicas` takes RLock then multiple client goroutines do `rep.Conn.Write(encoded)` on the same net.Conn concurrently. Concurrent writes interleave bytes → corrupted RESP framing.
**T:** Serialize writes per-replica to prevent byte interleaving.
**A:** Added `writeMu` to ReplicaConnection and a `Write()` method:
```go
type ReplicaConnection struct {
    Conn    net.Conn
    writeMu sync.Mutex  // Guards writes
    // ...
}

func (r *ReplicaConnection) Write(data []byte) (int, error) {
    r.writeMu.Lock()
    defer r.writeMu.Unlock()
    return r.Conn.Write(data)
}
```
Updated `propagateToReplicas` and WAIT's GETACK sender to use `rep.Write()` instead of `rep.Conn.Write()`.
**R:** Each replica connection's writes are now serialized. RESP framing guaranteed intact even under concurrent SET/INCR commands.

### Stream Waiter Double-Close Fix (Sep 11)
**S:** XREAD BLOCK on streams [a, b] registers one waiter with one Done channel on both. If concurrent XADD on 'a' and 'b' both call `notifyWaiters`, both try to `close(w.Done)` → **panic: close of closed channel**.
**T:** Prevent double-close and ensure waiter is only notified once.
**A:** Added `closeOnce sync.Once` to `StreamWaiter` and updated `notifyWaiters`:
```go
type StreamWaiter struct {
    Done      chan struct{}
    closeOnce sync.Once  // Guards close
}

func (s *streamStore) notifyWaiters(key, newID string) {
    for _, w := range waiters {
        w.closeOnce.Do(func() {
            close(w.Done)  // Safe: only executes once
        })
        // Remove from ALL keys, not just this one
        for k := range w.StreamIDs {
            s.removeWaiterFromKey(k, w)
        }
    }
}
```
**R:** Double-close panic eliminated. Waiter is atomically removed from all streams when first notified.

---

## Testing Philosophy

### The Testing Pyramid

```
         /\
        /  \     E2E (few, slow, high confidence)
       /----\
      /      \   Integration (medium)
     /--------\
    /          \  Unit Tests (many, fast, isolated)
   --------------
```

**Unit tests** — Test one component in isolation. Mock dependencies.
**Integration tests** — Test components working together. Real dependencies.
**E2E tests** — Test the whole system through external interface.

Each layer has ONE owner. Don't test the same behavior at multiple layers.

### Contract Testing (Preferred Approach)

**Core idea:** Each component has a *contract* — what it promises to do. Tests verify the contract, not implementation details.

#### How It Works

```go
// StringStore's CONTRACT:
// - Set(k, v) stores v under k
// - Get(k) returns v if exists, ("", false) if not
// - Get on expired key returns ("", false)
// - IncrBy on non-integer returns error

// StringStore contract tests verify ONLY these promises
func TestStringStore_Contract(t *testing.T) {
    store := NewStringStore(...)
    
    store.Set("k", "v")
    got, ok := store.Get("k")
    assert(ok && got == "v")  // contract: Set then Get returns value
}
```

```go
// MakeSET's CONTRACT:
// - Parses args correctly
// - Calls StringStore.Set (or SetWithTTL)
// - Returns "+OK\r\n" on success
// - Returns error on wrong args

// MakeSET contract tests verify ONLY these promises
// It ASSUMES StringStore works (proven by StringStore's contract tests)
func TestMakeSET_Contract(t *testing.T) {
    mockStore := &MockStringStore{}  // or real store
    handler := MakeSET(mockStore)
    
    result := handler([]string{"SET", "k", "v"}, nil)
    assert(result == "+OK\r\n")           // contract: returns OK
    assert(mockStore.SetWasCalled("k","v")) // contract: called Set
}
```

#### Why Contract Testing Reduces Redundancy

| Without Contracts | With Contracts |
|-------------------|----------------|
| StringStore tests "Set stores value" | StringStore tests "Set stores value" |
| MakeSET tests "Set stores value" (redundant!) | MakeSET tests "calls Set correctly" (different!) |
| Integration tests "Set stores value" (triple redundant!) | Integration tests "whole flow works" (different!) |

Each layer tests **its own responsibility**, not its dependencies' responsibilities.

#### Contracts as Documentation

The contract IS the interface documentation:

```go
// StringStore is the contract for string storage.
// 
// Contract:
// - Set(k,v) stores v; overwrites existing; clears TTL
// - SetWithTTL(k,v,ttl) stores v with expiration
// - Get(k) returns (v, true) or ("", false) if missing/expired
// - IncrBy(k,n) atomically adds n; returns error if not integer
// - Delete(k) removes key; returns true if existed
type StringStore interface {
    Set(key, value string) error
    SetWithTTL(key, value string, ttl time.Duration) error
    Get(key string) (string, bool)
    IncrBy(key string, delta int64) (int64, error)
    Delete(key string) bool
}
```

Tests prove the implementation honors the contract. If tests pass, callers can trust the contract.

#### When to Use Mocks vs Real Dependencies

| Situation | Use Mock | Use Real |
|-----------|----------|----------|
| Unit testing a component | ✓ | |
| Dependency is slow (DB, network) | ✓ | |
| Dependency is hard to set up | ✓ | |
| Testing integration between components | | ✓ |
| Dependency is fast and simple | | ✓ |
| Contract is complex and easy to mock wrong | | ✓ |

**Our approach:** Real dependencies for now (NewDataStore is fast). Mocks when testing edge cases the real store can't easily produce.

### FlashDB Test Structure (Post-Cleanup)

```
storage/*_test.go        → Contract tests for stores
                           "Does StringStore honor its contract?"
                           
commands/factory_test.go → Contract tests for command factories
                           "Does MakeSET parse, call store, return RESP correctly?"
                           Assumes stores work (proven by storage tests)

replication/*_test.go    → Integration tests for replication
                           "Does SET propagate to replicas correctly?"
```

**Redundant (to delete):**
- `commands/strings_test.go` — legacy handler tests
- `commands/lists_test.go` — legacy handler tests

---

## Deferred Issues (Requires Design Work)

### Issue #8: Replication Only Propagates SET/INCR
**Severity:** High (silent replica divergence)
**Status:** Deferred — needs comprehensive design

**Problem:**
Only `SET` and `INCR` call `propagateToReplicas()`. All other write commands (`LPUSH`, `RPUSH`, `LPOP`, `XADD`, `DEL`, etc.) do NOT propagate. Replicas silently diverge from master.

**Hard Cases Identified:**

1. **Transactions (MULTI/EXEC)**
   - When to propagate? At EXEC time, per-command
   - Redis propagates individual commands, not MULTI/EXEC wrapper
   - Replica doesn't see transaction boundary

2. **Blocking Operations (BLPOP triggers)**
   - `LPUSH` that unblocks a `BLPOP` waiter causes implicit pop
   - Must propagate BOTH `LPUSH` AND synthetic `LPOP`
   - Otherwise replica has item, master doesn't

3. **Auto-generated IDs (XADD *)**
   - `XADD stream * field value` generates timestamp-based ID
   - Must propagate resolved ID, not "*"
   - Otherwise replica generates different ID → divergence

4. **TTL Commands**
   - `EXPIRE`, `SETEX`, `PSETEX` must propagate
   - Expiry times must be absolute or adjusted for replication lag

**Proposed Approach:**
1. Create write command registry: `isWriteCommand(cmd string) bool`
2. Centralize propagation in command dispatcher or DataStore layer
3. Handle edge cases:
   - Transactions: propagate each at EXEC time
   - BLPOP triggers: emit synthetic LPOP after LPUSH
   - Generated IDs: propagate resolved values
4. Consider: command result contains `[]string` of commands to propagate

**Commands Needing Propagation:**
- Strings: `SET`, `SETEX`, `PSETEX`, `SETNX`, `INCR`, `INCRBY`, `DECR`, `DECRBY`, `APPEND`
- Lists: `LPUSH`, `RPUSH`, `LPOP`, `RPOP`, `LSET`, `LINSERT`, `LTRIM`
- Streams: `XADD`, `XDEL`, `XTRIM`
- Keys: `DEL`, `EXPIRE`, `EXPIREAT`, `PERSIST`, `RENAME`
- Transactions: commands within EXEC

### Issue #9: Replication Offset Accounting Broken
**Severity:** High (WAIT never satisfied)
**Status:** Deferred — depends on #8

**Problem:**
- Master increments `Offset` only for SET/INCR bytes
- Replica increments `localOffset` for ALL commands including GETACK
- Offsets can never agree → WAIT hangs forever

**Root Cause:** No canonical definition of "replication stream"

**Fix Approach:**
1. Define: replication stream = exact bytes sent to replicas
2. Master: count bytes of all propagated commands
3. Replica: count bytes of all received commands (not GETACK responses)
4. Both must count identical byte sequences

**Blocked By:** Issue #8 (need to know what commands propagate first)

---

## Open Questions

~~Should `Client` (transaction state) move to `server` package instead of `storage`?~~
→ **Resolved:** Moved to `internal/core/` as shared type (used by server, commands, replication).

~~Replication state — stays global or gets injected?~~
→ **Resolved:** Moved to `internal/replication/state.go` with singleton `State` and `EnsureState()`. Global for now (appropriate for single-process), but encapsulated in its own package.

---

## Final Package Structure

```
internal/
├── core/              # Shared types (breaks import cycles)
│   └── types.go       # Client, CommandHandler, CommandMap, InitCommands
├── commands/          # Command factories (MakeSET, MakeGET, etc.)
│   ├── strings.go
│   ├── lists.go
│   ├── streams.go
│   ├── general.go
│   ├── transactions.go
│   └── factory_test.go
├── replication/       # Replication logic
│   ├── state.go       # ReplicationState, ReplicaConnection, State global
│   ├── replica.go     # StartReplica
│   └── replica_test.go
├── server/            # Connection handling
│   └── server.go      # HandleConnection, StartServer
├── storage/           # Data stores
│   ├── interfaces.go  # DataStore, StringStore, ListStore, StreamStore
│   ├── types.go       # StreamEntry, TTLResult, DataType, errors
│   ├── ttl.go         # TTLManager
│   ├── registry.go    # TypeRegistry
│   ├── strings.go     # StringStore impl
│   ├── lists.go       # ListStore impl (with waiters)
│   ├── streams.go     # StreamStore impl (with waiters)
│   ├── datastore.go   # DataStore facade (composition root)
│   └── *_test.go      # Contract tests
├── persistence/       # RDB save/load
│   ├── rdb.go
│   └── rdb_test.go
├── resp/              # RESP protocol helpers
│   └── resp.go
└── utilities/
    └── utilities.go
```

### Dependency Graph

```
           ┌─────────┐
           │  core   │ ◄── Shared types (no deps)
           └────┬────┘
                │
    ┌───────────┼───────────┐
    ▼           ▼           ▼
┌───────┐  ┌────────┐  ┌───────────┐
│server │  │commands│  │replication│
└───┬───┘  └────┬───┘  └─────┬─────┘
    │           │            │
    └───────────┼────────────┘
                ▼
          ┌─────────┐
          │ storage │
          └─────────┘
```

No import cycles. `core` has zero internal dependencies.
