package storage

import (
	"strconv"
	"sync"
	"time"
)

// stringStore implements StringStore interface.
type stringStore struct {
	data     map[string]string
	registry Registry
	ttl      TTLManager
	mu       sync.RWMutex
}

// NewStringStore creates a new string store with dependencies.
func NewStringStore(registry Registry, ttl TTLManager) StringStore {
	return &stringStore{
		data:     make(map[string]string),
		registry: registry,
		ttl:      ttl,
	}
}

func (s *stringStore) Get(key string) (string, bool) {
	// Lazy expiry check
	if s.ttl.CheckAndDelete(key) {
		s.mu.Lock()
		delete(s.data, key)
		s.mu.Unlock()
		return "", false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	val, ok := s.data[key]
	return val, ok
}

func (s *stringStore) Set(key, value string) error {
	// Lazy expiry — clean up if expired
	if s.ttl.CheckAndDelete(key) {
		s.mu.Lock()
		delete(s.data, key)
		s.mu.Unlock()
	}

	// Register type (checks for WRONGTYPE)
	if err := s.registry.Acquire(key, TypeString); err != nil {
		return err
	}

	s.mu.Lock()
	s.data[key] = value
	s.mu.Unlock()

	return nil
}

func (s *stringStore) SetWithTTL(key, value string, ttl time.Duration) error {
	if err := s.Set(key, value); err != nil {
		return err
	}

	s.ttl.SetTTL(key, ttl)
	return nil
}

func (s *stringStore) Delete(key string) bool {
	s.mu.Lock()
	_, existed := s.data[key]
	delete(s.data, key)
	s.mu.Unlock()

	if existed {
		s.registry.Release(key)
	}

	return existed
}

func (s *stringStore) Exists(key string) bool {
	// Lazy expiry check
	if s.ttl.CheckAndDelete(key) {
		s.mu.Lock()
		delete(s.data, key)
		s.mu.Unlock()
		return false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.data[key]
	return ok
}

func (s *stringStore) IncrBy(key string, delta int64) (int64, error) {
	// Lazy expiry check
	if s.ttl.CheckAndDelete(key) {
		s.mu.Lock()
		delete(s.data, key)
		s.mu.Unlock()
	}

	// Register type (INCR on non-existent key is allowed, will default to "0")
	if err := s.registry.Acquire(key, TypeString); err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	val, ok := s.data[key]
	if !ok {
		val = "0" // non-existent key defaults to 0
	}

	num, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return 0, ErrNotInt
	}

	num += delta
	s.data[key] = strconv.FormatInt(num, 10)

	return num, nil
}

func (s *stringStore) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}

	return keys
}

// ForEach iterates over all string entries.
// Used by persistence layer for RDB saves.
// Note: May include expired keys — persistence layer should check expiry on load.
func (s *stringStore) ForEach(fn func(key, value string, expiry time.Time, hasTTL bool)) {
	// Get TTL snapshot first (one lock)
	ttlSnapshot := s.ttl.Snapshot()

	s.mu.RLock()
	defer s.mu.RUnlock()

	for key, value := range s.data {
		expiry, hasTTL := ttlSnapshot[key]
		fn(key, value, expiry, hasTTL)
	}
}

// Load adds a key-value pair during database load.
// Used by persistence layer for RDB loads. Bypasses type check.
func (s *stringStore) Load(key, value string, expiry time.Time, hasTTL bool) {
	s.mu.Lock()
	s.data[key] = value
	s.mu.Unlock()

	// Register type directly (no check needed during load)
	_ = s.registry.Acquire(key, TypeString)

	if hasTTL {
		s.ttl.Set(key, expiry)
	}
}
