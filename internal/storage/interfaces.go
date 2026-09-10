package storage

import "time"

// Registry manages key-type bindings and TTL.
// Ensures one key can only have one type at a time.
type Registry interface {
	// Acquire registers a key with a type, or validates existing type matches.
	// Returns ErrWrongType if key exists with different type.
	Acquire(key string, typ DataType) error

	// Release removes a key from the registry (on DEL).
	Release(key string)

	// SetTTL sets expiry on an existing key. Returns false if key not registered.
	SetTTL(key string, ttl time.Duration) bool

	// GetTTL returns TTL info for a key.
	GetTTL(key string) TTLResult

	// IsExpired checks if a key has expired.
	IsExpired(key string) bool

	// Type returns the type of a key, if registered.
	Type(key string) (DataType, bool)

	// Keys returns all non-expired keys of a given type.
	Keys(typ DataType) []string
}

// StringStore handles string key-value operations.
type StringStore interface {
	// Get returns value for key. Returns ("", false) if not found or expired.
	Get(key string) (string, bool)

	// Set stores a key-value pair. Returns ErrWrongType if key exists as different type.
	Set(key, value string) error

	// SetWithTTL stores a key-value pair with expiry.
	SetWithTTL(key, value string, ttl time.Duration) error

	// Delete removes a key. Returns true if key existed.
	Delete(key string) bool

	// Exists returns true if key exists and is not expired.
	Exists(key string) bool

	// IncrBy increments value by delta. Returns ErrWrongType or ErrNotInt on failure.
	IncrBy(key string, delta int64) (int64, error)

	// Keys returns all string keys.
	Keys() []string

	// ForEach iterates over all non-expired string entries (for persistence).
	ForEach(fn func(key, value string, expiry time.Time, hasTTL bool))

	// Load adds a key-value pair during database load (for persistence).
	Load(key, value string, expiry time.Time, hasTTL bool)
}

// ListStore handles list operations.
type ListStore interface {
	// LPush prepends values to list. Creates list if not exists.
	// Returns new length or ErrWrongType.
	LPush(key string, values ...string) (int, error)

	// RPush appends values to list. Creates list if not exists.
	// Returns new length or ErrWrongType.
	RPush(key string, values ...string) (int, error)

	// LRange returns elements from start to stop (inclusive, 0-indexed, negative supported).
	// Returns empty slice if key not found.
	LRange(key string, start, stop int) ([]string, error)

	// LLen returns length of list, 0 if not found.
	LLen(key string) (int, error)

	// LPop removes and returns up to count elements from head.
	LPop(key string, count int) ([]string, error)

	// BLPop blocks until an element is available or timeout.
	// timeout=0 means block forever. Returns key, value, ok.
	BLPop(keys []string, timeout time.Duration) (key, value string, ok bool)

	// Delete removes a list. Returns true if existed.
	Delete(key string) bool
}

// StreamStore handles stream operations.
type StreamStore interface {
	// XAdd adds entry to stream. id can be "*" for auto-generate.
	// Returns generated ID or error.
	XAdd(key, id string, fields map[string]string) (string, error)

	// XRange returns entries between start and end IDs (inclusive).
	XRange(key, start, end string) ([]StreamEntry, error)

	// XRead reads from multiple streams after given IDs.
	// Returns map of stream key -> entries.
	XRead(keys, ids []string) map[string][]StreamEntry

	// XReadBlock reads with blocking. timeout=0 means block forever.
	XReadBlock(keys, ids []string, timeout time.Duration) (map[string][]StreamEntry, error)

	// RegisterWaiter adds a waiter for blocking reads.
	RegisterWaiter(waiter *StreamWaiter)

	// UnregisterWaiter removes a waiter.
	UnregisterWaiter(waiter *StreamWaiter)

	// Delete removes a stream. Returns true if existed.
	Delete(key string) bool
}

// DataStore is the facade interface providing access to all stores.
type DataStore interface {
	Strings() StringStore
	Lists() ListStore
	Streams() StreamStore
	Registry() Registry

	// Delete removes a key regardless of type.
	Delete(key string) bool

	// Type returns the type of a key.
	Type(key string) (DataType, bool)

	// Exists returns true if key exists (any type).
	Exists(key string) bool
}
