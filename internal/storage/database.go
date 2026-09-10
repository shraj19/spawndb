package storage

import (
	"sync"
	"time"
	"strconv"
)

// Database encapsulates all data storage for FlashDB.
// Thread-safe via internal mutex.
type Database struct {
	store  map[string]string
	expiry map[string]time.Time
	lists  map[string][]string
	// streams map[string]*Stream // TODO: add later

	mu sync.RWMutex
}

// New creates an empty Database.
func New() *Database {
	return &Database{
		store:  make(map[string]string),
		expiry: make(map[string]time.Time),
		lists:  make(map[string][]string),
	}
}

// --- String operations ---

// Get returns the value for key, or ("", false) if not found or expired.
func (db *Database) Get(key string) (string, bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if db.isExpired(key) {
		return "", false
	}

	val, ok := db.store[key]
	return val, ok
}

// Set stores a key-value pair with no expiry.
func (db *Database) Set(key, value string) {
	db.mu.Lock()
	defer db.mu.Unlock()

	db.store[key] = value
	delete(db.expiry, key) // clear any existing TTL
}

// SetWithTTL stores a key-value pair with expiry.
func (db *Database) SetWithTTL(key, value string, ttl time.Duration) {
	db.mu.Lock()
	defer db.mu.Unlock()

	db.store[key] = value
	db.expiry[key] = time.Now().Add(ttl)
}

// SetExpiry sets TTL on an existing key. Returns false if key doesn't exist.
func (db *Database) SetExpiry(key string, ttl time.Duration) bool {
	db.mu.Lock()
	defer db.mu.Unlock()

	if _, ok := db.store[key]; !ok {
		return false
	}
	db.expiry[key] = time.Now().Add(ttl)
	return true
}

// Delete removes a key. Returns true if key existed.
func (db *Database) Delete(key string) bool {
	db.mu.Lock()
	defer db.mu.Unlock()

	_, existed := db.store[key]
	delete(db.store, key)
	delete(db.expiry, key)
	return existed
}

// Exists returns true if key exists and is not expired.
func (db *Database) Exists(key string) bool {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if db.isExpired(key) {
		return false
	}
	_, ok := db.store[key]
	return ok
}

// Keys returns all non-expired keys (for debugging/KEYS command).
func (db *Database) Keys() []string {
	db.mu.RLock()
	defer db.mu.RUnlock()

	keys := make([]string, 0, len(db.store))
	for k := range db.store {
		if !db.isExpired(k) {
			keys = append(keys, k)
		}
	}
	return keys
}

func (db *Database) INCRby(key string, delta int64) (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.isExpired(key) {
		db.store[key] = "0"
	}

	val, ok := db.store[key]
	if !ok {
		db.store[key] = "0"
		val = "0"
	}
	num, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return 0, err
	}
	num += delta
	db.store[key] = strconv.FormatInt(num, 10)
	return num, nil
}
// --- TTL operations ---

// TTLResult holds TTL query result.
type TTLResult struct {
	Duration time.Duration
	Exists   bool
	HasTTL   bool
}

// TTL returns remaining duration and key status.
func (db *Database) TTL(key string) TTLResult {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if db.isExpired(key) {
		return TTLResult{Exists: false}
	}

	if _, ok := db.store[key]; !ok {
		return TTLResult{Exists: false}
	}

	exp, hasTTL := db.expiry[key]
	if !hasTTL {
		return TTLResult{Exists: true, HasTTL: false}
	}

	return TTLResult{
		Duration: time.Until(exp),
		Exists:   true,
		HasTTL:   true,
	}
}

// --- Internal helpers ---

// isExpired checks if key has expired. Caller must hold at least RLock.
func (db *Database) isExpired(key string) bool {
	exp, ok := db.expiry[key]
	return ok && time.Now().After(exp)
}

// --- Iteration for persistence (exposes internals safely) ---

// ForEachString calls fn for each non-expired string key-value pair.
// Used by persistence layer.
func (db *Database) ForEachString(fn func(key, value string, expiry time.Time, hasTTL bool)) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	for key, value := range db.store {
		if db.isExpired(key) {
			continue
		}
		exp, hasTTL := db.expiry[key]
		fn(key, value, exp, hasTTL)
	}
}

// LoadString adds a key-value pair during database load.
// Used by persistence layer.
func (db *Database) LoadString(key, value string, expiry time.Time, hasTTL bool) {
	db.mu.Lock()
	defer db.mu.Unlock()

	db.store[key] = value
	if hasTTL {
		db.expiry[key] = expiry
	}
}

// Clear removes all data (for testing).
func (db *Database) Clear() {
	db.mu.Lock()
	defer db.mu.Unlock()

	db.store = make(map[string]string)
	db.expiry = make(map[string]time.Time)
	db.lists = make(map[string][]string)
}

// --- Low-level access for atomic operations (like INCR) ---

// Lock acquires write lock. Caller must call Unlock.
func (db *Database) Lock() {
	db.mu.Lock()
}

// Unlock releases write lock.
func (db *Database) Unlock() {
	db.mu.Unlock()
}

// GetUnlocked returns value without locking. Caller must hold lock.
func (db *Database) GetUnlocked(key string) (string, bool) {
	if db.isExpired(key) {
		return "", false
	}
	val, ok := db.store[key]
	return val, ok
}

// SetUnlocked stores value without locking. Caller must hold lock.
func (db *Database) SetUnlocked(key, value string) {
	db.store[key] = value
}
