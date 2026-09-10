package storage

import (
	"sync"
	"time"
)

// typeRegistry implements Registry interface.
// Tracks key-type bindings only. Depends on TTLManager for expiry checks.
// Uses lazy expiry (Memcached style) — cleanup on access, no background goroutine.
type typeRegistry struct {
	types map[string]DataType
	ttl   TTLManager
	mu    sync.RWMutex
}

// NewRegistry creates a new type registry with TTL manager dependency.
func NewRegistry(ttl TTLManager) Registry {
	return &typeRegistry{
		types: make(map[string]DataType),
		ttl:   ttl,
	}
}

// Acquire registers a key with a type, or validates existing type matches.
// Performs lazy expiry check first.
func (r *typeRegistry) Acquire(key string, typ DataType) error {
	// Lazy expiry: check and clean up if expired
	if r.ttl.CheckAndDelete(key) {
		r.mu.Lock()
		delete(r.types, key)
		r.mu.Unlock()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, exists := r.types[key]; exists {
		if existing != typ {
			return ErrWrongType
		}
		return nil
	}

	r.types[key] = typ
	return nil
}

// Release removes a key from the registry and its TTL.
func (r *typeRegistry) Release(key string) {
	r.mu.Lock()
	delete(r.types, key)
	r.mu.Unlock()

	r.ttl.Delete(key)
}

// SetTTL sets expiry on an existing key.
func (r *typeRegistry) SetTTL(key string, ttl time.Duration) bool {
	r.mu.RLock()
	_, exists := r.types[key]
	r.mu.RUnlock()

	if !exists {
		return false
	}

	r.ttl.SetTTL(key, ttl)
	return true
}

// GetTTL returns TTL info for a key.
// Performs lazy expiry check.
func (r *typeRegistry) GetTTL(key string) TTLResult {
	// Lazy expiry check
	if r.ttl.CheckAndDelete(key) {
		r.mu.Lock()
		delete(r.types, key)
		r.mu.Unlock()
		return TTLResult{Exists: false}
	}

	r.mu.RLock()
	_, exists := r.types[key]
	r.mu.RUnlock()

	if !exists {
		return TTLResult{Exists: false}
	}

	remaining, hasTTL := r.ttl.Remaining(key)
	if !hasTTL {
		return TTLResult{Exists: true, HasTTL: false}
	}

	return TTLResult{
		Duration: remaining,
		Exists:   true,
		HasTTL:   true,
	}
}

// IsExpired checks if a key has expired.
func (r *typeRegistry) IsExpired(key string) bool {
	return r.ttl.IsExpired(key)
}

// Type returns the type of a key, if registered and not expired.
// Performs lazy expiry check.
func (r *typeRegistry) Type(key string) (DataType, bool) {
	// Lazy expiry check
	if r.ttl.CheckAndDelete(key) {
		r.mu.Lock()
		delete(r.types, key)
		r.mu.Unlock()
		return 0, false
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	typ, exists := r.types[key]
	return typ, exists
}

// Keys returns all non-expired keys of a given type.
// Note: Does not perform lazy cleanup on all keys (expensive).
// Filters out expired keys from result.
func (r *typeRegistry) Keys(typ DataType) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	keys := make([]string, 0)
	for key, t := range r.types {
		if t != typ {
			continue
		}
		if r.ttl.IsExpired(key) {
			continue // skip expired, don't clean (would need write lock)
		}
		keys = append(keys, key)
	}

	return keys
}
