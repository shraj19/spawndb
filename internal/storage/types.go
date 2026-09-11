package storage

import (
	"errors"
	"net"
	"sync"
	"time"
)

// --- Data Types ---

// DataType identifies the type of value stored at a key.
type DataType uint8

const (
	TypeString DataType = iota
	TypeList
	TypeStream
)

func (t DataType) String() string {
	switch t {
	case TypeString:
		return "string"
	case TypeList:
		return "list"
	case TypeStream:
		return "stream"
	default:
		return "unknown"
	}
}

// --- Errors ---

var (
	ErrWrongType = errors.New("WRONGTYPE Operation against a key holding the wrong kind of value")
	ErrNotFound  = errors.New("key not found")
	ErrNotInt    = errors.New("value is not an integer or out of range")
)

// --- TTL Types ---

// TTLResult holds TTL query result.
type TTLResult struct {
	Duration time.Duration
	Exists   bool
	HasTTL   bool
}

// --- Stream Types ---

// StreamEntry represents a single entry in a stream.
type StreamEntry struct {
	ID     string            // e.g. "1697000000123-0"
	Fields map[string]string // field-value pairs
}

// Stream represents a stream data structure.
type Stream struct {
	Entries []StreamEntry      // ordered entries
	Index   map[string]int     // ID → position in slice, for fast lookup
}

// StreamWaiter represents a client waiting for stream data.
type StreamWaiter struct {
	Conn      net.Conn
	Done      chan struct{}
	StreamIDs map[string]string // stream key -> last ID seen
	closeOnce sync.Once         // Guards Done channel close to prevent double-close panic
}
