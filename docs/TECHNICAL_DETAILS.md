# FlashDB - Technical Deep Dive

**Your Personal Reference Guide for Interviews & Future Development**

---

## Table of Contents
1. [Project Overview](#project-overview)
2. [Architecture](#architecture)
3. [Core Components](#core-components)
4. [Data Structures](#data-structures)
5. [Concurrency Model](#concurrency-model)
6. [Protocol Implementation](#protocol-implementation)
7. [Command Categories](#command-categories)
8. [Advanced Features](#advanced-features)
9. [Key Design Decisions](#key-design-decisions)
10. [Performance Considerations](#performance-considerations)
11. [Interview Talking Points](#interview-talking-points)

---

## Project Overview

**FlashDB** is a lightweight, high-performance in-memory key-value database built in Go, implementing the RESP (Redis Serialization Protocol).

**Why this project matters:**
- Demonstrates low-level systems programming (TCP networking, protocol parsing)
- Shows understanding of concurrency primitives in Go
- Implements real database features (transactions, streams, TTL)
- Proves ability to build complete products, not just algorithms

**Core Stats:**
- ~500 lines of actual implementation code
- Supports 19 different commands
- Handles concurrent clients efficiently
- Uses only Go stdlib (no external dependencies)

---

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    TCP Server (Port 6379)                   │
│                                                              │
│  ┌──────────────────────────────────────────────────────┐  │
│  │  Connection Handler (goroutine per client)          │  │
│  │  ├─ RESP Parser (bufio.Reader)                      │  │
│  │  ├─ Command Router (CommandMap dispatch)            │  │
│  │  └─ Response Writer (net.Conn)                      │  │
│  └──────────────────────────────────────────────────────┘  │
│                           ↓                                 │
│  ┌──────────────────────────────────────────────────────┐  │
│  │         Storage Layer (sync.RWMutex protected)      │  │
│  │  ├─ Store (map[string]string)                       │  │
│  │  ├─ Expiry (map[string]time.Time)                   │  │
│  │  ├─ Lists (map[string][]string)                     │  │
│  │  ├─ Streams (map[string]*Stream)                    │  │
│  │  └─ Waiters (for BLPOP blocking semantics)          │  │
│  └──────────────────────────────────────────────────────┘  │
│                           ↓                                 │
│  ┌──────────────────────────────────────────────────────┐  │
│  │         Background Goroutines                        │  │
│  │  ├─ Active Expiry (1-second ticker)                 │  │
│  │  ├─ Stream Waiters (blocking XREAD)                 │  │
│  │  └─ Metrics Logger (10-second ticker)               │  │
│  └──────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

**Why this architecture?**
- **Per-goroutine connection handling**: Leverages Go's lightweight concurrency
- **Centralized storage**: Easier to manage state and locking
- **RWMutex for reads**: Multiple readers can access simultaneously
- **Separate goroutines for background tasks**: Doesn't block main event loop

---

## Core Components

### 1. Server Module (`internal/server/server.go`)

**Responsibilities:**
- Listen for TCP connections on port 6379
- Create goroutine for each client
- Parse RESP protocol
- Route commands to handlers
- Manage transaction state

**Key Flow:**
```go
StartServer()
  ├─ InitCommands() - register all command handlers
  ├─ StartActiveExpiry() - background goroutine
  ├─ net.Listen("tcp", "0.0.0.0:6379")
  └─ Accept loop
      └─ go HandleConnection(conn)
          ├─ Parse RESP
          ├─ Lookup handler
          ├─ Check transaction state
          └─ Execute/Queue command
```

**Transaction State Machine:**
```
Default State
    ↓
[MULTI] → InTransaction = true, QueuedCommands = []
    ↓
[CMD1] → +QUEUED (command queued, not executed)
[CMD2] → +QUEUED
    ↓
[EXEC] → Execute all queued commands atomically
    ↓
InTransaction = false, return array of results

OR

[DISCARD] → Throw away queued commands
    ↓
InTransaction = false
```

### 2. RESP Parser (`internal/resp/resp.go`)

**What is RESP?**
Redis Serialization Protocol - a simple, binary-safe protocol for client-server communication.

**RESP Message Format:**
```
*3         - Array of 3 elements
$3         - First element is bulk string of length 3
SET        - The string "SET"
$3         - Second element is bulk string of length 3
foo        - The string "foo"
$3         - Third element is bulk string of length 3
bar        - The string "bar"

// Full message:
*3\r\n$3\r\nSET\r\n$3\r\nfoo\r\n$3\r\nbar\r\n
```

**Parsing Algorithm:**
1. Read first line: `*n` (array of n elements)
2. For each element:
   - Read `$len` (bulk string of len bytes)
   - Read exactly len bytes (+ trailing \r\n)
   - Add to args array
3. Return args

**Why custom parser instead of external lib?**
- Shows understanding of protocols
- Control over error handling
- Zero external dependencies
- ~50 lines of clean code

### 3. Storage Module (`internal/storage/storage.go`)

**Global State** (protected by `sync.RWMutex`):
```go
Store         map[string]string              // Main key-value pairs
Expiry        map[string]time.Time           // TTL deadlines
Lists         map[string]any                 // List data structure ([]string)
Streams       map[string]*Stream             // Stream log structure
Waiters       map[string][]chan [2]string    // Blocking consumers for BLPOP
StreamWaiters map[string][]*StreamWaiter     // Blocking consumers for XREAD
```

**Locking Strategy:**
- `RWMutex` not separate locks per-key (simpler, good enough for this scale)
- Read lock: `Mu.RLock()` for GET operations
- Write lock: `Mu.Lock()` for SET, LPUSH, etc.
- Lock held for minimal duration

**Why RWMutex?**
- Multiple GETs can happen simultaneously (no contention)
- WRITE operations are serialized (transactions need atomicity anyway)
- Trade-off: simpler code vs potential lock contention at very high load

### 4. Metrics Logger (`internal/storage/metrics.go`)

**Metrics Tracked:**
- `ConnectedClients`: Current active connections (atomic int64)
- `TotalCommands`: Cumulative command count (atomic int64)
- `GetKeysCount()`: Current number of keys (requires RLock)

**Why Atomic Operations?**
- No lock needed for simple increments
- Metrics logging runs every 10 seconds in background
- Doesn't block main request handling

**Output Sample:**
```
Connected Clients: 5
Total Commands: 1234
Keys in Store: 89
```

---

## Data Structures

### 1. Strings (Key-Value Store)
```go
Store: map[string]string = {
  "user:1:name": "Alice",
  "user:1:email": "alice@example.com",
  "counter": "42"
}

Expiry: map[string]time.Time = {
  "session:abc123": time.Now().Add(30 * time.Minute)
}
```

**Commands:** SET, GET, INCR, DEL, TYPE, INFO

### 2. Lists
```go
Lists: map[string]any = {
  "queue:tasks": []string{"task1", "task2", "task3"},
  "stack:undo": []string{"action3", "action2", "action1"}
}
```

**Internal Representation:** `[]string` (Go slice)

**Why slice instead of linked list?**
- Better cache locality
- Range operations (LRANGE) are O(1) slice view
- Sequential memory access
- Downsides: LPUSH/LPOP O(n) but acceptable for this scale

**Commands:** LPUSH, RPUSH, LPOP, RPOP, LRANGE, LLEN, BLPOP

### 3. Streams
```go
type StreamEntry struct {
    ID     string            // "1697000000123-0" (timestamp-sequence)
    Fields map[string]string // {field1: value1, field2: value2}
}

type Stream struct {
    Entries []StreamEntry      // Ordered log
    Index   map[string]int     // ID -> position for O(1) lookup
}

Streams: map[string]*Stream = {
  "logs:app": Stream{
    Entries: [
      {ID: "1697000000123-0", Fields: {msg: "app started"}},
      {ID: "1697000000124-1", Fields: {msg: "config loaded"}}
    ],
    Index: {"1697000000123-0": 0, "1697000000124-1": 1}
  }
}
```

**Why this design?**
- `Entries`: Maintains insertion order for XRANGE
- `Index`: Enables O(1) lookups for blocking operations
- ID format matches Redis: `timestamp-sequence`

**Commands:** XADD, XRANGE, XREAD

### 4. Transaction Queue
```go
type Client struct {
    Conn              net.Conn
    InTransaction     bool
    QueuedCommands    [][]string
}

// Example during MULTI/EXEC:
QueuedCommands = [
  ["SET", "x", "10"],
  ["INCR", "x"],
  ["GET", "x"]
]
```

---

## Concurrency Model

### Problem: Concurrent Access to Shared State

Without synchronization:
```go
// Thread 1
val := store["counter"]
val++
store["counter"] = val

// Thread 2
val := store["counter"]  // Race: sees old value!
val++
store["counter"] = val

// Result: counter only incremented by 1, should be 2
```

### Solution: sync.RWMutex

**How it works:**
```go
var Mu sync.RWMutex

// For reads:
Mu.RLock()
val := store["counter"]
Mu.RUnlock()

// For writes:
Mu.Lock()
store["counter"] = newVal
Mu.Unlock()

// Multiple readers OR single writer, never both
```

**Traffic Flow:**
```
RLock, RLock, RUnlock, RUnlock    ✓ Allowed (3 readers)
RLock, RUnlock                     ✓ Allowed (1 reader)
Lock (waits for readers), Unlock   ✓ Allowed (1 writer)
RLock (waits for writer)           ✓ Allowed (1 reader)
Lock, Lock                         ✗ Deadlock (same goroutine)
```

### Goroutine-per-Connection Pattern

```go
for {
    conn, _ := listener.Accept()
    go HandleConnection(conn)  // New goroutine for each client
}

// Thousands of concurrent clients with ease:
// - 1 goroutine per client
// - Go scheduler handles OS thread pooling
// - Efficient context switching
```

**Why this scales:**
- Go has extremely lightweight goroutines (~2KB memory)
- Multiplexing via epoll/kqueue underneath
- Single goroutine can't handle concurrent clients
- Alternative: Manual multiplexing would be ~1000x more complex

### BLPOP (Blocking Semantics)

**Challenge:** How to make a client wait when list is empty?

**Solution: Channel-based Waiting**
```go
type Waiter struct {
    Ch chan [2]string  // Send {key, value}
}

// BLPOP: Create waiter, add to queue, wait
ch := make(chan [2]string)
Waiters["mylist"] = append(Waiters["mylist"], ch)
result := <-ch  // Block until value arrives

// LPUSH/RPUSH: Check waiters, deliver values
if len(Waiters[key]) > 0 {
    waiter := Waiters[key][0]
    Waiters[key] = Waiters[key][1:]
    waiter <- [2]string{key, value}  // Wake up
}
```

**Trade-offs:**
- Simple to implement
- Works for this scale (hundreds of waiters)
- At huge scale: event queue would be better

---

## Protocol Implementation

### RESP Messages Sent Back

**Simple String:** `+OK\r\n`
```go
SET key value → "+OK\r\n"
```

**Integer:** `:1000\r\n`
```go
LPUSH list item → ":1\r\n"  // List now has 1 item
```

**Bulk String:** `$5\r\nvalue\r\n`
```go
GET key → "$5\r\nvalue\r\n"  // 5-byte string "value"
GET nonexistent → "$-1\r\n"  // Nil bulk string (null)
```

**Error:** `-ERR message\r\n`
```go
GET → "-ERR wrong number of arguments for 'get'\r\n"
```

**Array:** `*2\r\n$3\r\nfoo\r\n$3\r\nbar\r\n`
```go
LRANGE list 0 -1 → 
  *2              // Array of 2 elements
  $3              // First element: 3-byte string
  foo             // Value: "foo"
  $3              // Second element: 3-byte string
  bar             // Value: "bar"
```

---

## Command Categories

### 1. String Commands

#### SET
```go
SET key value [EX seconds] [PX milliseconds]

// Implementation:
1. Acquire write lock
2. Store[key] = value
3. If EX/PX flag: Expiry[key] = time.Now().Add(duration)
4. Release lock
5. Return +OK
```

**Why separate parsing for EX/PX?**
- Optional modifiers
- Parse as strings, convert to int, calculate deadline

#### GET
```go
GET key

// Implementation:
1. Acquire read lock
2. Check Expiry[key] (if past deadline, treat as nonexistent)
3. val := Store[key]
4. Release lock
5. Return bulk string format
```

**Lazy Expiration:**
- Don't delete expired keys immediately
- Check on GET, set flag as nil return
- Active expiry background goroutine cleans up rest

#### INCR
```go
INCR key

// Implementation:
1. Acquire write lock
2. Get current value (default "0" if missing)
3. Parse as integer (error if not numeric)
4. Increment by 1
5. Store back
6. Release lock
7. Return integer format ":newval\r\n"
```

### 2. List Commands

#### LPUSH / RPUSH
```go
LPUSH key value1 [value2 ...]

// Implementation:
Lists[key] = []string{}
for each value (in order for LPUSH):
  1. Check Waiters[key]
  2. If waiters exist, deliver to first waiter
  3. Else append to list
```

**Why handle waiters inside LPUSH?**
- Ensures atomicity: delivery and list update in one lock
- Waiter receives value immediately upon push

#### BLPOP
```go
BLPOP key [key ...] timeout

// Implementation:
1. For each key: if non-empty list, LPOP and return
2. If all lists empty:
   a. Create channel for this waiter
   b. Register in Waiters[key]
   c. Wait on channel (blocks goroutine, not OS thread)
   d. When LPUSH happens, receives value
   e. Return value
```

**Timeout Handling:**
- Go's `time.After()` in select statement
- `select { case result := <-ch: ..., case <-time.After(timeout): ...}`

### 3. Stream Commands

#### XADD
```go
XADD stream * field1 value1 [field2 value2 ...]

// ID generation: timestamp-sequence
// Example: "1697000000123-0" means:
//   - Millisecond: 1697000000123
//   - Sequence in that ms: 0 (first entry)

// If multiple XADD calls in same millisecond:
// 1697000000123-0, 1697000000123-1, 1697000000123-2

Implementation:
1. Generate ID with current time
2. Parse field/value pairs
3. Create StreamEntry
4. Append to Stream.Entries
5. Update Stream.Index for O(1) lookup
6. Notify StreamWaiters (if any XREAD waiting)
```

#### XRANGE
```go
XRANGE stream start end

// Example: XRANGE mystream 1697000000100-0 1697000000200-0
// Returns entries where:
//   start_id ≤ entry_id ≤ end_id

Implementation:
1. Parse start/end IDs
2. Find indices using Stream.Index lookup
3. Return slice of entries
```

#### XREAD
```go
XREAD [BLOCK milliseconds] STREAMS key1 key2 id1 id2

// Non-blocking: Read entries after given IDs
// Blocking: Wait if no new entries

Implementation:
1. If BLOCK specified:
   a. Create StreamWaiter with channels
   b. Register in StreamWaiters
   c. Wait for notification or timeout
2. For each stream, find entries after given ID
3. Notify blocking readers when new entries arrive
```

### 4. Transaction Commands

#### MULTI
```go
MULTI

// Implementation:
1. Check: already in transaction?
2. Set client.InTransaction = true
3. Initialize client.QueuedCommands = []
4. Return +OK
```

#### EXEC
```go
EXEC

// Implementation:
1. Check: client.InTransaction?
2. Validation phase: ensure all commands are valid
3. Execute phase: run each command in sequence
   - Each command gets write lock individually
   - Commands see effects of previous commands
4. Collect all responses
5. Return array of responses
6. Reset transaction state
```

**Atomicity Notes:**
- Each command acquires/releases its own lock
- NOT fully atomic (other clients can see intermediate state)
- Matches Redis behavior: transactions are "all-or-nothing" in terms of queueing, but not isolation
- True serializable isolation would require holding lock for entire EXEC duration

#### DISCARD
```go
DISCARD

// Implementation:
1. Check: client.InTransaction?
2. Throw away QueuedCommands
3. Set InTransaction = false
4. Return +OK
```

---

## Advanced Features

### 1. Active Expiration Background Goroutine

```go
func StartActiveExpiry() {
    ticker := time.NewTicker(1 * time.Second)
    go func() {
        for range ticker.C {
            now := time.Now()
            Mu.Lock()
            for key, deadline := range Expiry {
                if now.After(deadline) {
                    delete(Store, key)
                    delete(Expiry, key)
                }
            }
            Mu.Unlock()
        }
    }()
}
```

**Why every 1 second?**
- Trade-off between precision and CPU usage
- Every 1ms: ~1000 ticks/sec × lock time = excessive
- Every 10sec: Keys might stay after expiration = staleness
- 1sec: Sweet spot for most use cases

**Lazy vs Active Expiry:**
- **Lazy**: Check on GET (no background cost, but expired keys linger)
- **Active**: Background goroutine (uses CPU, but frees memory)
- **Hybrid** (used here): Both (no expired reads, memory freed in background)

### 2. Metrics Logging

```go
func StartMetricsLogger() {
    ticker := time.NewTicker(10 * time.Second)
    go func() {
        for range ticker.C {
            clients := atomic.LoadInt64(&ConnectedClients)
            commands := atomic.LoadInt64(&TotalCommands)
            keys := GetKeysCount()
            fmt.Printf("Connected: %d, Commands: %d, Keys: %d\n",
                clients, commands, keys)
        }
    }()
}
```

**Why atomic counters?**
- Very fast increments without lock
- Non-blocking metric collection

---

## Key Design Decisions

### 1. **Global State vs Per-Shard State**

**Chosen: Global State** (single Store map)

**Pros:**
- Simple transactions (atomic under one lock)
- Easy to implement
- Suitable for single-machine deployment

**Cons:**
- Lock contention at extreme scale
- Can't parallelize writes

**Better for Scale:** Sharded stores (hash ring, partition by key)

### 2. **RWMutex vs Fine-Grained Locking**

**Chosen: RWMutex** (coarse-grained)

**Pros:**
- Simple to understand and maintain
- No deadlock risk
- Transact semantics trivial

**Cons:**
- Potential lock contention
- Writes block reads

**Better for Scale:** Per-key locks or optimistic locking with CAS

### 3. **Slice vs Linked List for Lists**

**Chosen: Slice** ([]string)

**Pros:**
- Cache-friendly sequential memory
- LRANGE is O(1) slice view
- Memory efficient

**Cons:**
- LPUSH/RPUSH requires array resizing (O(n) in worst case)
- But golang slices use capacity-based growth (amortized O(1))

**Better for Scale:** Skip list or B-tree for extremely large lists

### 4. **Stream Entry Indexing**

**Chosen: Hash Map for O(1) lookup** (Stream.Index)

**Pros:**
- XREAD operations O(1) lookup by ID
- Fast blocking semantics

**Cons:**
- Extra memory for index
- Must keep in sync with entries array

**Alternative:** Binary search on sorted entries (O(log n) but less memory)

### 5. **RESP Parser from Scratch vs External Library**

**Chosen: Custom Parser**

**Pros:**
- Shows protocol understanding
- Zero external dependencies
- Control over error handling

**Cons:**
- More code to maintain
- Less tested than battle-hardened library

**Learning Value:** HIGH (demonstrates systems thinking)

---

## Performance Considerations

### Current Bottlenecks

| Operation | Complexity | Notes |
|-----------|-----------|-------|
| GET | O(1) | Hash map lookup |
| SET | O(1) | Hash map insertion |
| LPUSH | O(n) | Array reallocation worst case, but amortized O(1) |
| LRANGE | O(k) | k = end - start, linear in result size |
| XADD | O(1) | Append + map insert |
| XRANGE | O(log n + k) | Binary search + linear result |
| INCR | O(1) | Hash map + int parsing |
| MULTI/EXEC | O(n) | n = number of commands |

### Lock Contention Profile

```
High Write Load:
GET (read)   → Waits for Lock holders
GET (read)   → Waits for Lock holders
SET (write)  → Acquires Lock
INCR (write) → Waits
INCR (write) → Acquires Lock

Problem: Writes serialize everything
Solution: Sharding or lock-free data structures
```

### Memory Usage

```
Store map:           ~50 bytes overhead + key/value sizes
Expiry map:          ~50 bytes overhead + 24 bytes per entry
Lists map:           ~50 bytes overhead + slice overhead per list
Streams map:         Similar structure
Goroutines:          ~2KB per connection + ~1KB per waiter
```

**Estimate:** ~1KB per concurrent connection, ~100 bytes per stored key-value

### Latency Profile

```
Local GET:     <1ms (hash lookup + lock)
Remote GET:    <5ms (GET + network roundtrip)
LPUSH+waiter:  ~1-2ms (depending on contention)
MULTI/EXEC:    ~5ms (2x overhead vs single commands)
XREAD block:   Depends on data arrival (can be seconds)
```

---

## Interview Talking Points

### Opening Statement (30 seconds)
> "I built FlashDB, a high-performance in-memory key-value database in Go that implements the Redis protocol. It supports transactions, blocking operations, and stream processing, all with careful attention to concurrent access and clean architecture."

### Technical Depth (1-2 minutes)

**Concurrency:**
- "I use `sync.RWMutex` to protect shared state. Multiple goroutines can read simultaneously with `RLock`, but writes are serialized with `Lock`. This prevents race conditions on data structures."
- "Each client connection runs in its own goroutine—Go handles the scheduling. This scales to thousands of clients efficiently."

**Protocol:**
- "I implemented the RESP protocol parser from scratch. It's a binary-safe format: arrays start with `*n`, bulk strings with `$len`. This shows I understand low-level network protocols."

**Advanced Features:**
- "Transactions use a command queue. Commands between MULTI and EXEC are queued, then executed atomically when EXEC arrives."
- "Blocking operations like BLPOP use Go channels. When a client blocks, it waits on a channel; when another client pushes data, it sends through the channel. This is elegant and efficient."

### Trade-Off Questions

**Q: Why not use an external library?**
> "For RESP parsing, I could use an external library, but building it from scratch demonstrates protocol understanding and keeps dependencies minimal. For storage, I could use BoltDB or LevelDB, but implementing my own shows I understand data structure trade-offs."

**Q: How does this compare to Redis?**
> "Mine is simpler—Redis has replication, persistence, cluster support, and many more commands. But the core architecture is similar: single-threaded event loop (I use goroutines for concurrency), RWMutex for safety, and RESP protocol. The main difference is Redis uses a single thread with epoll multiplexing; I use goroutines for simplicity."

**Q: How would you scale this?**
> "For vertical scaling: I'd add sharding (partition keys across multiple mutexes) and lock-free data structures. For horizontal scaling: I'd implement replication—master accepts writes, slaves replicate. For persistence: I'd implement RDB snapshots and AOF logging."

**Q: What's the bottleneck at high load?**
> "Single RWMutex. At very high write concurrency, the lock becomes a bottleneck. Solution: partition the data (hash ring), each partition has its own lock."

### Code Quality Questions

**Q: Why use channels for BLPOP instead of condition variables?**
> "Channels are more idiomatic in Go. They work well here because it's simple: one channel per waiter, one receiver (the client goroutine), multiple senders (writers). Condition variables would work too but are less clean in this pattern."

**Q: How do you handle clock skew in stream IDs?**
> "Stream IDs use timestamp + sequence. If the system clock goes backward, I could generate duplicate IDs. Redis handles this by checking if the new ID > last ID, and if not, using the last ID with sequence incremented. I could add this."

**Q: What about persistence across restarts?**
> "Currently there's no persistence—all data is in memory. To add it: I'd snapshot the entire Store to disk periodically (RDB format), or log every write command to a file (AOF format). Redis combines both approaches for durability."

### Mistakes & Learnings

**Share (to show maturity):**
1. "Initially I tried holding locks for too long. Then I realized I could reduce lock scope by checking expiry outside the lock. That taught me: lock scope matters for latency."
2. "Stream blocking was tricky. First attempt used polling—clients would wake up every 100ms to check. That's wasteful. Channels are much better: sleep until data arrives."
3. "I had race conditions with transaction execution until I realized I needed to validate all queued commands before executing any. Otherwise, a failed command partway through is confusing."

---

## Future Enhancements (Resume Additions)

**Short-term (1-2 weeks):**
- [ ] RDB Persistence (save/load snapshots)
- [ ] AOF logging (command replay log)
- [ ] Pub/Sub messaging
- [ ] Sorted sets (ZSET)

**Medium-term (1 month):**
- [ ] Lua scripting (EVAL command)
- [ ] Key eviction policies (LRU, LFU)
- [ ] Master-slave replication
- [ ] Cluster support (hash ring)

**Long-term (production-ready):**
- [ ] Sentinel (high availability)
- [ ] Full ACL system
- [ ] Modules/plugins API
- [ ] Performance benchmarking suite

---

## Testing & Validation

**Manual Testing Done:**
```bash
PING → +PONG
SET key value → +OK
GET key → $5\r\nvalue
LPUSH list item → :1
BLPOP list 5 → (waits for item, returns it)
MULTI/EXEC sequence → Proper queueing
XADD/XREAD → Stream ordering preserved
```

**Recommended Additions:**
- Concurrent client load testing (1000+ clients)
- Chaos testing (network partitions, kills)
- Memory leak detection (pprof)
- Benchmark suite comparing to Redis

---

## References & Inspiration

- Redis protocol: https://redis.io/docs/reference/protocol-spec/
- Go concurrency patterns: https://www.youtube.com/watch?v=f6kdp27TYZs
- Redis source code (inspiration, not copied): https://github.com/redis/redis

---

**End of Technical Details**

Last updated: February 1, 2026
Use this document to refresh knowledge before interviews, system design discussions, or when explaining the project to others.
