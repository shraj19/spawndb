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

---

## Open Questions

- Should `Client` (transaction state) move to `server` package instead of `storage`?
- Replication state — stays global or gets injected?
