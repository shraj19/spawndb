# FlashDB: High-Performance Key-Value Store

A lightweight, concurrent, in-memory key-value database built in Go, implementing the RESP (Redis Serialization Protocol).

## Key Features

* **Custom Protocol Parser:** Built a raw TCP handler to parse RESP messages from scratch
* **Concurrency Safe:** Handles concurrent read/writes using `sync.RWMutex` locks for optimal performance
* **TTL Support:** Implemented lazy key expiration with active background eviction
* **Replication Support:** Master/replica handshake flow, `REPLCONF`, `PSYNC`, and `WAIT` handling with offset tracking
* **Stream Processing:** Support for Redis-like streams with blocking reads
* **Transaction Support:** MULTI/EXEC/DISCARD transaction handling with command queuing
* **Persistence:** Binary snapshot persistence with atomic writes
* **Basic Metrics:** Periodic logging of keys count, connected clients, and command count

## Tech Stack

* **Go (Golang)** - Core language for high-performance concurrency
* **TCP Networking** - Low-level socket programming using `net` package
* **Docker** - Containerized deployment for portability

## Supported Commands

### String Operations
- `SET key value [EX seconds] [PX milliseconds]` - Set key with optional TTL
- `GET key` - Retrieve value by key
- `INCR key` - Increment integer value
- `TYPE key` - Get the type of a key (string, list, stream, or none)

### List Operations
- `LPUSH key value [value ...]` - Push to list head
- `RPUSH key value [value ...]` - Push to list tail
- `LPOP key [count]` - Pop from list head
- `BLPOP key timeout` - Blocking pop from list head
- `LRANGE key start stop` - Get range of elements
- `LLEN key` - Get list length

### Stream Operations
- `XADD stream_key * field value [field value ...]` - Add entry to stream
- `XRANGE stream_key start end` - Query stream entries
- `XREAD [BLOCK milliseconds] STREAMS stream_key [stream_key ...] id [id ...]` - Read from streams

### Transactions
- `MULTI` - Start transaction
- `EXEC` - Execute queued commands
- `DISCARD` - Abort transaction and discard queued commands

### General
- `PING` - Health check
- `ECHO message` - Echo message back
- `INFO [section]` - Server information (sections: server, replication)

### Replication
- `REPLCONF ...` - Replica configuration and ACK
- `PSYNC ...` - Initial replication handshake
- `WAIT numreplicas timeout` - Wait for replicas to acknowledge writes

## Replication

FlashDB supports a Redis-like replication flow for basic leader/follower setups:

- Master keeps a replication offset and tracks connected replicas
- Replicas connect with `PING`, `REPLCONF`, and `PSYNC`
- Master emits a full resync frame and propagates writes to connected replicas
- `WAIT` blocks until enough replicas acknowledge the master offset

### Start a master
```bash
go run . --port 6379
```

### Start a replica
```bash
go run . --port 6380 --replicaof 127.0.0.1 6379
```

### Validate replication flow
```bash
redis-cli -p 6379
> SET hello world
OK
> WAIT 1 1000
(integer) 1
```

## How to Run

### Using Go directly
```bash
go build -o flashdb .
./flashdb
```

### Using Docker
```bash
docker build -t flashdb .
docker run -p 6379:6379 flashdb
```

### Testing with redis-cli
```bash
redis-cli -p 6379
> PING
PONG
> SET mykey "Hello FlashDB"
OK
> GET mykey
"Hello FlashDB"
```

## Architecture

```
flashdb/
├── main.go                  # Entry point
├── cmd/
│   └── server/
│       └── main.go          # Alternative entry point
├── internal/
│   ├── commands/
│   │   ├── general.go       # PING, ECHO, INFO, WAIT, REPLCONF, PSYNC
│   │   ├── strings.go       # SET, GET, INCR, TYPE
│   │   ├── lists.go         # LPUSH, RPUSH, LPOP, BLPOP, LRANGE, LLEN
│   │   ├── streams.go       # XADD, XRANGE, XREAD
│   │   └── transactions.go  # MULTI, EXEC, DISCARD
│   ├── persistence/
│   │   └── rdb.go           # Binary snapshot save/load
│   ├── replication/
│   │   └── replica.go       # Replica connection and sync logic
│   ├── resp/
│   │   └── resp.go          # RESP protocol parser
│   ├── server/
│   │   └── server.go        # TCP server & client handler
│   ├── storage/
│   │   ├── database.go      # Thread-safe key-value store
│   │   ├── storage.go       # Global state, streams, lists
│   │   └── metrics.go       # Connection and command metrics
│   └── utilities/
│       └── utilities.go     # Helper functions
```

## Performance Features

- **Goroutine-per-connection:** Concurrent client handling with dedicated goroutines
- **RWMutex optimization:** Read-write mutex allows concurrent reads
- **Lazy + Active expiration:** TTL keys expired on access and via background sweep
- **Atomic persistence:** Temp file + rename pattern prevents corruption
- **Metrics logging:** Background task logs keys, clients, and commands every 10s

## Development

```bash
# Run tests
go test ./...

# Format code
go fmt ./...

# Build binary
go build -o flashdb .
```

## License

MIT License - Feel free to use this project for learning and development.

## Author

Built with ❤️ as a learning project to understand low-level database internals and network programming in Go.
