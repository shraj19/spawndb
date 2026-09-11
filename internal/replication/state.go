package replication

import (
	"net"
	"sync"
)

// ReplicaConnection represents a connected replica.
type ReplicaConnection struct {
	Conn          net.Conn
	ListeningPort string
	AckOffset     int64
	Connected     bool
	writeMu       sync.Mutex // Guards writes to Conn to prevent interleaving
}

// Write sends data to the replica connection with serialization.
// Multiple goroutines can safely call Write concurrently.
func (r *ReplicaConnection) Write(data []byte) (int, error) {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	return r.Conn.Write(data)
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

// State is the global replication state singleton.
var State = &ReplicationState{
	Role:     "master",
	ReplID:   "8371b4fb1155b71f4a04d3e1bc3e18c4a990aeeb",
	Offset:   0,
	Replicas: make(map[string]*ReplicaConnection),
}

// EnsureState ensures replication state is initialized.
func EnsureState() *ReplicationState {
	if State == nil {
		State = &ReplicationState{
			Role:     "master",
			ReplID:   "",
			Offset:   0,
			Replicas: make(map[string]*ReplicaConnection),
		}
	}
	return State
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
