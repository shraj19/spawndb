package storage

import (
	"hash/fnv"
	"sync"
)

const defaultNumShards = 256

// KeyLock provides per-key locking using sharded mutexes.
// Keys are hashed to one of N buckets; keys in the same bucket serialize.
// This provides atomicity for single-key operations while allowing
// parallelism for operations on different keys (in different buckets).
type KeyLock struct {
	shards []sync.Mutex
	mask   uint32 // For fast modulo when numShards is power of 2
}

// NewKeyLock creates a new KeyLock with the default number of shards (256).
func NewKeyLock() *KeyLock {
	return NewKeyLockWithShards(defaultNumShards)
}

// NewKeyLockWithShards creates a KeyLock with a custom number of shards.
// numShards should be a power of 2 for best performance.
func NewKeyLockWithShards(numShards int) *KeyLock {
	if numShards <= 0 {
		numShards = defaultNumShards
	}
	// Round up to next power of 2 for fast modulo
	numShards = nextPowerOf2(numShards)

	return &KeyLock{
		shards: make([]sync.Mutex, numShards),
		mask:   uint32(numShards - 1),
	}
}

// Lock acquires the lock for a given key.
func (kl *KeyLock) Lock(key string) {
	bucket := kl.bucket(key)
	kl.shards[bucket].Lock()
}

// Unlock releases the lock for a given key.
func (kl *KeyLock) Unlock(key string) {
	bucket := kl.bucket(key)
	kl.shards[bucket].Unlock()
}

// bucket returns the shard index for a key using FNV-1a hash.
func (kl *KeyLock) bucket(key string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(key))
	return h.Sum32() & kl.mask
}

// nextPowerOf2 returns the next power of 2 >= n.
func nextPowerOf2(n int) int {
	if n <= 1 {
		return 1
	}
	n--
	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16
	return n + 1
}
