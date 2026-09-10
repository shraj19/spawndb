# FlashDB Development Notes

Tracks ongoing refactoring, design decisions, and next steps.

---

## Recent Commits (Sep 8-10, 2026)

```
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
- `TTLManager` — Set, SetTTL, Get, IsExpired, CheckAndDelete, Remaining
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

**Step 4b: storage_test.go (21 tests)**
- TTLManager: SetAndCheck, CheckAndDelete, Remaining
- Registry: AcquireAndType, Release, ExpiredKeyReuse
- StringStore: SetGet, SetWithTTL, ExpiredKey, WrongType, IncrBy, Delete
- ListStore: PushAndRange, LLen, LPop, WrongType, BLPop (immediate, priority, blocking, multi-key, timeout)

### Remaining Steps
- Step 5: streams.go (StreamStore with StreamWaiters)
- Step 6: database.go (facade)
- Step 7: migrate commands to use new stores
- Step 8: delete old globals

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

---

## Open Questions

- Should `Client` (transaction state) move to `server` package instead of `storage`?
- Replication state — stays global or gets injected?
