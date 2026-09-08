package storage

import (
	"sync"
	"strings"
	"time"
	"net"
)

// DB is the global database instance (for gradual migration from globals)
var DB = New()

// Deprecated: use DB.Get/Set instead
var Store = make(map[string]string)
// Deprecated: use DB methods instead
var Expiry = make(map[string]time.Time)
var Waiters = make(map[string][]chan [2]string)
var Mu sync.RWMutex
var Lists = make(map[string]any)
var Streams = make(map[string]*Stream)
var StreamWaiters = make(map[string][]*StreamWaiter)

type StreamEntry struct {
    ID    string            // e.g. "1697000000123-0"
    Fields map[string]string
}

type Stream struct {
    Entries []StreamEntry    // ordered
    Index   map[string]int   // ID → position in slice, for fast lookup
    // maybe trimming parameters
}

type Client struct {
	Conn           net.Conn
	InTransaction  bool
	QueuedCommands [][]string
}

// Replication-related types and global state
type ReplicaConnection struct {
	Conn         net.Conn
	ListeningPort string
	AckOffset    int64
	Connected    bool
}

type ReplicationState struct {
	Role           string
	ReplID         string
	Offset         int64
	LastAskedOffset int64
	Replicas       map[string]*ReplicaConnection
	Mu             sync.RWMutex
}

var Replication = &ReplicationState{
	Role:     "master",
	ReplID:   "8371b4fb1155b71f4a04d3e1bc3e18c4a990aeeb",
	Offset:   0,
	Replicas: make(map[string]*ReplicaConnection),
}

func EnsureReplication() *ReplicationState {
	if Replication == nil {
		Replication = &ReplicationState{
			Role:     "master",
			ReplID:   "",
			Offset:   0,
			Replicas: make(map[string]*ReplicaConnection),
		}
	}
	return Replication
}

func (r *ReplicationState) IncreaseOffset(n int64) {
	r.Mu.Lock()
	r.Offset += n
	r.Mu.Unlock()
}

func (r *ReplicationState) CountReplicasAcked(targetOffset int64) int {
	r.Mu.RLock()
	defer r.Mu.RUnlock()
	count := 0
	for _, rep := range r.Replicas {
		if rep != nil && rep.AckOffset >= targetOffset {
			count++
		}
	}
	return count
}

// CommandHandler is the function signature for a Redis command.
type CommandHandler func(args []string, client *Client) string
var CommandMap map[string]CommandHandler

type StreamWaiter struct {
	Conn net.Conn
	Done chan struct{}
	StreamIDs map[string]string // stream key -> last ID seen
}


func StartActiveExpiry() {
	ticker := time.NewTicker(1 * time.Second) // check every second
	go func() {
		for range ticker.C {
			now := time.Now()
			Mu.Lock()
			for key, exp := range Expiry {
				if now.After(exp) {
					delete(Store, key)
					delete(Expiry, key)
				}
			}
			Mu.Unlock()
		}
	}()
}

func GetStore() map[string]string {
	return Store
}

func GetExpiry() map[string]time.Time {
	return Expiry
}

func GetWaiters() map[string][]chan [2]string {
	return Waiters
}

func GetLists() map[string]any {
	return Lists
}

func GetMu() *sync.RWMutex {
	return &Mu
}

func GetStreams() *map[string]*Stream {
	return &Streams
}

func GetStreamWaiters() map[string][]*StreamWaiter {
	return StreamWaiters
}

func InitCommands(commands map[string]CommandHandler) {
	CommandMap = make(map[string]CommandHandler)
	for name, handler := range commands {
		CommandMap[strings.ToUpper(name)] = handler
	}
}
