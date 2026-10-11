# SpawnDB Development Guide

## Quick Start

```bash
# Build
go build -o spawndb .

# Run
./spawndb --port 6379 --metrics-port 9121

# Test
go test ./...

# Run with race detector
go test -race ./...
```

## Project Structure

```
internal/
├── core/              # Shared types (breaks import cycles)
│   └── types.go       # Client, CommandHandler, CommandMap
├── commands/          # Command factories (MakeSET, MakeGET, etc.)
│   ├── strings.go
│   ├── lists.go
│   ├── streams.go
│   ├── general.go
│   └── transactions.go
├── server/            # TCP server, connection handling
│   └── server.go
├── storage/           # Data stores
│   ├── interfaces.go  # DataStore, StringStore, ListStore, StreamStore
│   ├── datastore.go   # Composition root
│   ├── keylock.go     # Sharded per-key locking (256 buckets)
│   ├── strings.go     # String operations
│   ├── lists.go       # List operations + BLPOP waiters
│   ├── streams.go     # Stream operations + XREAD waiters
│   ├── ttl.go         # TTL manager (lazy expiration)
│   └── registry.go    # Type registry (WRONGTYPE enforcement)
├── replication/       # Master/replica sync
│   ├── state.go       # ReplicationState, replica tracking
│   └── replica.go     # Replica connection logic
├── persistence/       # RDB snapshots
│   └── rdb.go
├── resp/              # RESP protocol encoder/decoder
│   └── resp.go
└── metrics/           # Prometheus instrumentation
    └── metrics.go
```

## Architecture Decisions

### 1. Goroutine-per-Connection
Each client gets a dedicated goroutine. Simple, scales well for moderate connection counts.

### 2. Sharded Per-Key Locking
`KeyLock` uses 256 mutex buckets. Keys hash to buckets, so operations on different keys rarely contend.

```go
type KeyLock struct {
    shards [256]sync.Mutex
}

func (k *KeyLock) Lock(key string) {
    k.shards[hash(key) & 255].Lock()
}
```

### 3. Command Factory Pattern
Commands receive `DataStore` via closure capture, not global state:

```go
func MakeSET(db storage.DataStore) core.CommandHandler {
    return func(args []string, client *core.Client) string {
        db.Set(args[1], args[2])
        return resp.OK()
    }
}
```

### 4. Lazy TTL Expiration
No background goroutine. Keys checked and cleaned on access. Simpler, less lock contention.

### 5. Type Registry
Prevents same key existing in multiple data structures. Returns `WRONGTYPE` error like Redis.

## Testing

```bash
# Run all tests
go test ./...

# Run with verbose output
go test -v ./...

# Run specific package
go test ./internal/storage/...

# Run with coverage
go test -cover ./...
```

### Test Organization
- `storage/*_test.go` — Contract tests for stores
- `commands/factory_test.go` — Command handler tests
- `replication/*_test.go` — Replication integration tests

## Benchmarking

```bash
# Build
go build -o spawndb .
./spawndb --port 6379 &

# Run redis-benchmark
redis-benchmark -p 6379 -t set,get,incr,lpush,lpop -n 100000 -c 50

# With pipelining
redis-benchmark -p 6379 -t set,get -n 100000 -c 50 -P 16
```

## Common Tasks

### Adding a New Command

1. Add to appropriate file in `internal/commands/`
2. Create factory function: `func MakeXXX(db storage.DataStore) core.CommandHandler`
3. Register in `internal/server/server.go` under `StartServer()`
4. Add tests in `commands/factory_test.go`

### Adding a New Data Structure

1. Define interface in `storage/interfaces.go`
2. Implement in `storage/newtype.go`
3. Add to `DataStore` interface and `dataStore` struct
4. Wire in `NewDataStore()`
5. Create command factories
6. Add contract tests

## Debugging

### Race Detection
```bash
go test -race ./...
go build -race -o spawndb . && ./spawndb
```

### Profiling
```bash
# CPU profile
go test -cpuprofile=cpu.prof -bench=. ./internal/storage/
go tool pprof cpu.prof

# Memory profile
go test -memprofile=mem.prof -bench=. ./internal/storage/
go tool pprof mem.prof
```

### Metrics
Prometheus metrics available at `:9121/metrics`:
- `spawndb_commands_total`
- `spawndb_command_duration_seconds`
- `spawndb_connected_clients`
- `spawndb_keys_total`
