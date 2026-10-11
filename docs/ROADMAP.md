# FlashDB Roadmap

## Why This Matters

FlashDB is multi-goroutine with per-key sharding. Redis is single-threaded.

**Current state:** On basic ops (SET/GET/INCR/LPUSH), FlashDB achieves ~95% of Redis performance. These operations are too fast for parallelism to help — they're I/O bound.

**Goal:** Implement CPU-heavy operations where FlashDB's parallelism provides a real advantage over Redis's single-threaded model.

---

## Priority 1: Parallelism Advantage Features

These are CPU-bound operations where multi-core execution should beat Redis.

### 1.1 Pipelining Support
**Why:** Currently FlashDB processes commands one-by-one. With pipelining, clients send N commands without waiting; server should batch responses.

**Impact:** 3-5x throughput gap vs Redis with P=16+

**Implementation:**
- Buffer multiple commands from reader before executing
- Batch responses into single Write() call
- Reduces syscall overhead

**Benchmark to beat:** Redis at P=16 does 1.5M ops/sec, FlashDB does 500k

---

### 1.2 Sorted Sets (ZADD, ZRANGE, ZRANGEBYSCORE, ZCARD, ZSCORE)
**Why:** O(log n) skip-list operations are actually CPU-bound. Parallel ZADD on different keys can utilize multiple cores.

**Commands:**
- `ZADD key score member [score member ...]`
- `ZRANGE key start stop [WITHSCORES]`
- `ZRANGEBYSCORE key min max [WITHSCORES] [LIMIT offset count]`
- `ZCARD key` — cardinality
- `ZSCORE key member` — get score
- `ZREM key member [member ...]`
- `ZINCRBY key increment member`
- `ZRANK key member` — rank by score

**Data structure:** Skip list or balanced tree (Go's stdlib doesn't have one — implement or use `github.com/emirpasic/gods`)

---

### 1.3 Set Operations (SADD, SMEMBERS, SINTER, SUNION, SDIFF)
**Why:** Set operations on large sets are CPU-bound (hashing, iteration, intersection algorithms).

**Commands:**
- `SADD key member [member ...]`
- `SREM key member [member ...]`
- `SMEMBERS key`
- `SISMEMBER key member`
- `SCARD key`
- `SINTER key [key ...]` — intersection
- `SUNION key [key ...]` — union
- `SDIFF key [key ...]` — difference

**Parallelism win:** SINTER/SUNION/SDIFF across large sets can be parallelized internally.

---

### 1.4 KEYS and SCAN (Pattern Matching)
**Why:** Regex matching against all keys is CPU-bound. Can parallelize across key shards.

**Commands:**
- `KEYS pattern` — return all matching keys (dangerous in prod, but useful)
- `SCAN cursor [MATCH pattern] [COUNT count]` — cursor-based iteration

**Implementation:**
- Parallelize across KeyLock shards
- Each goroutine scans its shard, merge results

---

### 1.5 SORT Command
**Why:** Actual sorting is CPU-bound. Go's sort is single-threaded, but parallel sort algorithms exist.

**Command:**
- `SORT key [BY pattern] [LIMIT offset count] [ASC|DESC] [ALPHA] [STORE destination]`

---

### 1.6 MGET / MSET (Batch Operations)
**Why:** Fetching 100 keys in one command. Can parallelize fetches across shards.

**Commands:**
- `MGET key [key ...]`
- `MSET key value [key value ...]`

**Implementation:**
- Group keys by shard
- Parallel fetch per shard
- Merge results

---

## Priority 2: Feature Completeness

### 2.1 Pub/Sub
**Commands:**
- `SUBSCRIBE channel [channel ...]`
- `PUBLISH channel message`
- `UNSUBSCRIBE [channel ...]`
- `PSUBSCRIBE pattern [pattern ...]`

**Parallelism:** Fan-out to many subscribers can be parallelized.

---

### 2.2 Lua Scripting (EVAL)
**Why:** This is THE use case where Redis users hit single-thread limits. Lua VM execution is CPU-bound.

**Commands:**
- `EVAL script numkeys key [key ...] arg [arg ...]`
- `EVALSHA sha1 numkeys key [key ...] arg [arg ...]`
- `SCRIPT LOAD script`

**Implementation:** Embed `github.com/yuin/gopher-lua`

**Parallelism win:** Different scripts on different keys can run in parallel (Redis can't do this).

---

### 2.3 Hash Operations
**Commands:**
- `HSET key field value [field value ...]`
- `HGET key field`
- `HMGET key field [field ...]`
- `HGETALL key`
- `HDEL key field [field ...]`
- `HEXISTS key field`
- `HLEN key`

---

### 2.4 HyperLogLog
**Commands:**
- `PFADD key element [element ...]`
- `PFCOUNT key [key ...]`
- `PFMERGE destkey sourcekey [sourcekey ...]`

---

## Priority 3: Production Readiness

### 3.1 Cluster Mode
- Hash slot distribution (16384 slots)
- CLUSTER commands
- Resharding

### 3.2 Better Persistence
- AOF (Append-Only File)
- AOF rewriting
- Hybrid RDB+AOF

### 3.3 Memory Management
- `MEMORY USAGE key`
- Eviction policies (LRU, LFU, volatile-*)
- `MAXMEMORY` config

---

## Benchmarks to Run

### Lock Contention Test (FlashDB should lose)
```bash
# All clients hitting SAME key
redis-benchmark -t incr -n 100000 -c 50 -r 1
```

### Parallelism Advantage Test (FlashDB should win)
```bash
# Large LRANGE on different keys
# Need custom benchmark — redis-benchmark doesn't support this well

# Large value GET across many keys
redis-benchmark -t get -n 100000 -c 100 -d 10240 -r 100000
```

### After implementing sorted sets
```bash
# Parallel ZADD on different keys
redis-benchmark -t zadd -n 100000 -c 100 -r 100000
```

---

## Comparison Targets

| System | Threading Model | Notes |
|--------|-----------------|-------|
| **Redis** | Single-threaded (I/O threads in v6+) | The baseline |
| **KeyDB** | Multi-threaded Redis fork | Direct competitor, similar goals |
| **Dragonfly** | Multi-threaded, shared-nothing | Very fast, modern C++ |
| **ElastiCache** | Managed Redis/Memcached | AWS service, not OSS |

**Note:** ElastiCache is AWS's managed Redis service — it runs standard Redis under the hood. It's not a separate implementation. For multi-threaded comparison, look at **KeyDB** or **Dragonfly**.

---

## Implementation Order (Suggested)

1. **Pipelining** — biggest current gap, relatively simple
2. **MGET/MSET** — easy win, useful, shows parallel fetch
3. **Sets** — prerequisite for many patterns
4. **Sorted Sets** — high value, shows O(log n) parallel ops
5. **SCAN/KEYS** — shows parallel pattern matching
6. **Pub/Sub** — common feature request
7. **Lua** — hardest but highest parallelism payoff
