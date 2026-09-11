package storage

import "time"

// dataStore implements DataStore interface.
// This is the composition root — assembles all stores with shared dependencies.
// Uses KeyLock for per-key atomicity to prevent TOCTOU races.
type dataStore struct {
	strings  StringStore
	lists    ListStore
	streams  StreamStore
	registry Registry
	ttl      TTLManager
	keyLock  *KeyLock // Per-key lock for atomic operations
}

// NewDataStore creates a fully-wired DataStore.
// This is the single composition root for storage.
func NewDataStore() DataStore {
	ttl := NewTTLManager()
	registry := NewRegistry(ttl)

	return &dataStore{
		strings:  NewStringStore(registry, ttl),
		lists:    NewListStore(registry),
		streams:  NewStreamStore(registry),
		registry: registry,
		ttl:      ttl,
		keyLock:  NewKeyLock(),
	}
}

func (d *dataStore) Strings() StringStore {
	return d.strings
}

func (d *dataStore) Lists() ListStore {
	return d.lists
}

func (d *dataStore) Streams() StreamStore {
	return d.streams
}

func (d *dataStore) Registry() Registry {
	return d.registry
}

func (d *dataStore) KeyLock() *KeyLock {
	return d.keyLock
}

// --- Atomic String Operations ---

// Set atomically sets a string value.
func (d *dataStore) Set(key, value string) error {
	d.keyLock.Lock(key)
	defer d.keyLock.Unlock(key)
	return d.strings.Set(key, value)
}

// SetWithTTL atomically sets a string value with TTL.
func (d *dataStore) SetWithTTL(key, value string, ttl time.Duration) error {
	d.keyLock.Lock(key)
	defer d.keyLock.Unlock(key)
	return d.strings.SetWithTTL(key, value, ttl)
}

// Get atomically gets a string value.
func (d *dataStore) Get(key string) (string, bool) {
	d.keyLock.Lock(key)
	defer d.keyLock.Unlock(key)
	return d.strings.Get(key)
}

// IncrBy atomically increments a string value.
func (d *dataStore) IncrBy(key string, delta int64) (int64, error) {
	d.keyLock.Lock(key)
	defer d.keyLock.Unlock(key)
	return d.strings.IncrBy(key, delta)
}

// --- Atomic List Operations ---

// LPush atomically pushes to the left of a list.
func (d *dataStore) LPush(key string, values ...string) (int, error) {
	d.keyLock.Lock(key)
	defer d.keyLock.Unlock(key)
	return d.lists.LPush(key, values...)
}

// RPush atomically pushes to the right of a list.
func (d *dataStore) RPush(key string, values ...string) (int, error) {
	d.keyLock.Lock(key)
	defer d.keyLock.Unlock(key)
	return d.lists.RPush(key, values...)
}

// LPop atomically pops from the left of a list.
func (d *dataStore) LPop(key string, count int) ([]string, error) {
	d.keyLock.Lock(key)
	defer d.keyLock.Unlock(key)
	return d.lists.LPop(key, count)
}

// LRange atomically gets a range from a list.
func (d *dataStore) LRange(key string, start, stop int) ([]string, error) {
	d.keyLock.Lock(key)
	defer d.keyLock.Unlock(key)
	return d.lists.LRange(key, start, stop)
}

// LLen atomically gets the length of a list.
func (d *dataStore) LLen(key string) (int, error) {
	d.keyLock.Lock(key)
	defer d.keyLock.Unlock(key)
	return d.lists.LLen(key)
}

// BLPop is a blocking operation - handled specially (no keylock needed for wait)
// The underlying listStore handles its own synchronization for blocking.
func (d *dataStore) BLPop(keys []string, timeout time.Duration) (key, value string, ok bool) {
	// Note: BLPop is special - it waits on multiple keys.
	// We can't hold locks during the blocking wait.
	// The listStore handles internal synchronization.
	return d.lists.BLPop(keys, timeout)
}

// --- Atomic Stream Operations ---

// XAdd atomically adds an entry to a stream.
func (d *dataStore) XAdd(key, id string, fields map[string]string) (string, error) {
	d.keyLock.Lock(key)
	defer d.keyLock.Unlock(key)
	return d.streams.XAdd(key, id, fields)
}

// XRange atomically gets a range from a stream.
func (d *dataStore) XRange(key, start, end string) ([]StreamEntry, error) {
	d.keyLock.Lock(key)
	defer d.keyLock.Unlock(key)
	return d.streams.XRange(key, start, end)
}

// XRead reads from multiple streams (no keylock - reads are safe).
func (d *dataStore) XRead(keys []string, ids []string) (map[string][]StreamEntry, error) {
	// XRead across multiple keys - each key read is independent
	return d.streams.XRead(keys, ids), nil
}

// XReadBlock is a blocking operation - handled by streamStore's internal sync.
func (d *dataStore) XReadBlock(keys []string, ids []string, timeout time.Duration) (map[string][]StreamEntry, error) {
	return d.streams.XReadBlock(keys, ids, timeout)
}

// --- Atomic Delete ---

// Delete atomically removes a key regardless of type.
func (d *dataStore) Delete(key string) bool {
	d.keyLock.Lock(key)
	defer d.keyLock.Unlock(key)

	typ, exists := d.registry.Type(key)
	if !exists {
		return false
	}

	switch typ {
	case TypeString:
		return d.strings.Delete(key)
	case TypeList:
		return d.lists.Delete(key)
	case TypeStream:
		return d.streams.Delete(key)
	default:
		return false
	}
}

// --- Atomic Type/Exists ---

// Type atomically returns the type of a key.
func (d *dataStore) Type(key string) (DataType, bool) {
	d.keyLock.Lock(key)
	defer d.keyLock.Unlock(key)
	return d.registry.Type(key)
}

// Exists atomically checks if key exists (any type).
func (d *dataStore) Exists(key string) bool {
	d.keyLock.Lock(key)
	defer d.keyLock.Unlock(key)
	_, exists := d.registry.Type(key)
	return exists
}
