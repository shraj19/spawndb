package storage

import (
	"net"
	"strings"
	"sync"
)

// DB is the legacy database instance used by persistence layer.
// TODO: Migrate persistence to use DataStore, then remove this.
var DB = New()

// Client represents a connected client with transaction state.
type Client struct {
	Conn           net.Conn
	InTransaction  bool
	QueuedCommands [][]string
}

// CommandHandler is the function signature for a Redis command.
type CommandHandler func(args []string, client *Client) string

// CommandMap holds registered command handlers.
var CommandMap map[string]CommandHandler

// InitCommands initializes the command map.
func InitCommands(commands map[string]CommandHandler) {
	CommandMap = make(map[string]CommandHandler)
	for name, handler := range commands {
		CommandMap[strings.ToUpper(name)] = handler
	}
}

// --- Replication types and state ---

// ReplicaConnection represents a connected replica.
type ReplicaConnection struct {
	Conn          net.Conn
	ListeningPort string
	AckOffset     int64
	Connected     bool
}

// ReplicationState holds master/replica state.
type ReplicationState struct {
	Role            string
	ReplID          string
	Offset          int64
	LastAskedOffset int64
	Replicas        map[string]*ReplicaConnection
	Mu              sync.RWMutex
}

// Replication is the global replication state.
var Replication = &ReplicationState{
	Role:     "master",
	ReplID:   "8371b4fb1155b71f4a04d3e1bc3e18c4a990aeeb",
	Offset:   0,
	Replicas: make(map[string]*ReplicaConnection),
}

// EnsureReplication ensures replication state is initialized.
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

// IncreaseOffset atomically increases the replication offset.
func (r *ReplicationState) IncreaseOffset(n int64) {
	r.Mu.Lock()
	r.Offset += n
	r.Mu.Unlock()
}

// CountReplicasAcked counts replicas that have acknowledged up to targetOffset.
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
