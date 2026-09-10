package storage

import (
	"sync"
	"time"
)

// TTLManager tracks key expiry times.
// Uses lazy expiry only — no background goroutine.
// Expiry check and cleanup happens on access.
type TTLManager interface {
	// Set sets expiry for a key.
	Set(key string, expiry time.Time)

	// SetTTL sets TTL duration from now.
	SetTTL(key string, ttl time.Duration)

	// Get returns expiry time and whether TTL is set.
	Get(key string) (expiry time.Time, hasTTL bool)

	// IsExpired checks if key has expired. Does NOT delete.
	IsExpired(key string) bool

	// CheckAndDelete checks if expired, deletes if so. Returns true if was expired.
	// Use this for lazy expiry on access.
	CheckAndDelete(key string) (wasExpired bool)

	// Delete removes TTL for a key.
	Delete(key string)

	// Remaining returns remaining TTL. Returns (0, false) if no TTL or expired.
	Remaining(key string) (time.Duration, bool)

	// Snapshot returns a copy of all expiry data for batch operations.
	Snapshot() map[string]time.Time
}

// ttlManager implements TTLManager.
type ttlManager struct {
	expiry map[string]time.Time
	mu     sync.RWMutex
}

// NewTTLManager creates a new TTL manager.
func NewTTLManager() TTLManager {
	return &ttlManager{
		expiry: make(map[string]time.Time),
	}
}

func (t *ttlManager) Set(key string, expiry time.Time) {
	t.mu.Lock()
	t.expiry[key] = expiry
	t.mu.Unlock()
}

func (t *ttlManager) SetTTL(key string, ttl time.Duration) {
	t.mu.Lock()
	t.expiry[key] = time.Now().Add(ttl)
	t.mu.Unlock()
}

func (t *ttlManager) Get(key string) (time.Time, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	exp, ok := t.expiry[key]
	return exp, ok
}

func (t *ttlManager) IsExpired(key string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	exp, ok := t.expiry[key]
	if !ok {
		return false // no TTL = not expired
	}
	return time.Now().After(exp)
}

// CheckAndDelete checks expiry and deletes if expired.
// This is the lazy expiry mechanism — call on every access.
func (t *ttlManager) CheckAndDelete(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	exp, ok := t.expiry[key]
	if !ok {
		return false // no TTL
	}

	if time.Now().After(exp) {
		delete(t.expiry, key)
		return true // was expired, cleaned up
	}

	return false
}

func (t *ttlManager) Delete(key string) {
	t.mu.Lock()
	delete(t.expiry, key)
	t.mu.Unlock()
}

func (t *ttlManager) Remaining(key string) (time.Duration, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	exp, ok := t.expiry[key]
	if !ok {
		return 0, false
	}

	remaining := time.Until(exp)
	if remaining <= 0 {
		return 0, false // expired
	}

	return remaining, true
}

// Snapshot returns a copy of all expiry data.
// Use for batch operations to avoid per-key locking.
func (t *ttlManager) Snapshot() map[string]time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()

	snapshot := make(map[string]time.Time, len(t.expiry))
	for k, v := range t.expiry {
		snapshot[k] = v
	}
	return snapshot
}
