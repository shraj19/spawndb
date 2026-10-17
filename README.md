# spawndb

> **Note:** This project was formerly known as **FlashDB**. The rename reflects the concurrent "spawn goroutines" architecture.

A high-performance, concurrent, in-memory key-value database written in Go. Implements the Redis Serialization Protocol (RESP) from scratch — compatible with `redis-cli` and Redis client libraries.

## Performance

Benchmarked against Redis 6 on AWS EC2 m7i-flex.large (3 separate instances for fair comparison):

| Scenario | SpawnDB | Redis | Winner |
|----------|---------|-------|--------|
| SET (50 clients) | 221K ops/s | 148K ops/s | **SpawnDB +49%** |
| GET (50 clients) | 238K ops/s | 152K ops/s | **SpawnDB +56%** |
| LPUSH/RPUSH | 227-239K ops/s | 147-154K ops/s | **SpawnDB +50%** |
| LRANGE (100 elements) | 103K ops/s | 105K ops/s | **Tie** |
| LRANGE (600 elements) | 22K ops/s | 24K ops/s | Redis +9% |
| Pipelining (P=16) | 775K ops/s | 1.9M ops/s | Redis 2.4x |

### Why SpawnDB is Faster (on single operations)

SpawnDB outperforms Redis on simple operations because it's simpler:

1. **No feature tax** — Redis checks ACLs, tracks memory, evaluates keyspace notifications, and more on every command. SpawnDB just executes.
2. **Goroutine efficiency** — At moderate concurrency, Go's goroutine-per-connection has less overhead than Redis's event loop fd scanning.
3. **Shorter code path** — Fewer layers between network read and storage access.

**When Redis wins:** High pipelining (event loop batches efficiently), extreme concurrency (single-threaded = no locks), and any production requirement (durability, cluster, operational tooling).

This is not "SpawnDB > Redis." It's "minimal implementations win micro-benchmarks."

See [`benchmark-results/`](benchmark-results/) and [`docs/ARCHITECTURE_AUDIT.md`](docs/ARCHITECTURE_AUDIT.md) for full analysis.

## Features

### Core
- **RESP Protocol** — Hand-built parser/encoder, wire-compatible with Redis
- **Concurrent Architecture** — Goroutine-per-connection with sharded per-key locking (256 buckets)
- **TTL Support** — Lazy expiration on access + active background eviction
- **Persistence** — Binary RDB snapshots with atomic writes

### Data Structures
- **Strings** — SET, GET, INCR with TTL support
- **Lists** — LPUSH, RPUSH, LPOP, BLPOP (blocking), LRANGE, LLEN
- **Streams** — XADD, XREAD (with BLOCK), XRANGE

### Advanced
- **Transactions** — MULTI/EXEC/DISCARD with command queuing
- **Replication** — Master/replica sync via REPLCONF, PSYNC, WAIT
- **Observability** — Prometheus metrics endpoint, structured JSON logging

## Quick Start

```bash
# Build and run
go build -o spawndb .
./spawndb --port 6379

# Connect with redis-cli
redis-cli -p 6379
> PING
PONG
> SET user:1 "alice"
OK
> GET user:1
"alice"
```

### Docker

```bash
docker build -t spawndb .
docker run -p 6379:6379 -p 9121:9121 spawndb
```

### Replication

```bash
# Start master
./spawndb --port 6379

# Start replica
./spawndb --port 6380 --replicaof 127.0.0.1 6379

# Verify
redis-cli -p 6379 SET hello world
redis-cli -p 6379 WAIT 1 1000  # Wait for replica ACK
redis-cli -p 6380 GET hello    # Returns "world"
```

## Supported Commands

| Category | Commands |
|----------|----------|
| **Strings** | `SET` `GET` `INCR` `TYPE` |
| **Lists** | `LPUSH` `RPUSH` `LPOP` `BLPOP` `LRANGE` `LLEN` |
| **Streams** | `XADD` `XREAD` `XRANGE` |
| **Transactions** | `MULTI` `EXEC` `DISCARD` |
| **Server** | `PING` `ECHO` `INFO` |
| **Replication** | `REPLCONF` `PSYNC` `WAIT` |

## Architecture

```
internal/
├── server/       # TCP server, connection handling, metrics endpoint
├── resp/         # RESP protocol encoder/decoder
├── commands/     # Command implementations (strings, lists, streams, etc.)
├── storage/
│   ├── datastore.go   # Composition root
│   ├── keylock.go     # Sharded per-key locking
│   ├── strings.go     # String store
│   ├── lists.go       # List store with blocking ops
│   ├── streams.go     # Stream store
│   ├── ttl.go         # TTL manager
│   └── registry.go    # Type registry (WRONGTYPE checks)
├── replication/  # Master/replica sync logic
├── persistence/  # RDB snapshots
└── metrics/      # Prometheus instrumentation
```

### Concurrency Model

- **Goroutine-per-connection** — Each client gets a dedicated goroutine
- **Sharded KeyLock** — 256 lock buckets, keys hash to buckets for fine-grained locking
- **RWMutex per store** — Concurrent reads, serialized writes within each data structure
- **Atomic cross-store ops** — Registry + data operations protected against TOCTOU races

## Development

```bash
go test ./...      # Run tests
go build ./...     # Build
go run . --port 6379 --metrics-port 9121
```

### Metrics

spawndb exposes Prometheus metrics at `:9121/metrics`:

- `spawndb_commands_total` — Commands processed by type and status
- `spawndb_command_duration_seconds` — Latency histograms per command
- `spawndb_connected_clients` — Current client connections
- `spawndb_keys_total` — Total keys in database

This project demonstrates:

1. **Low-level network programming** — Raw TCP, custom protocol parsing
2. **Concurrent systems design** — Lock hierarchies, avoiding deadlocks, blocking operations
3. **Performance engineering** — Benchmarking methodology, profiling, optimization
4. **Redis internals** — How a production database actually works

## License

MIT
