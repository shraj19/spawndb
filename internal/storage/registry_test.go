package storage

import (
	"testing"
	"time"
)

func TestRegistry_AcquireAndType(t *testing.T) {
	ttl := NewTTLManager()
	reg := NewRegistry(ttl)

	// Acquire new key
	if err := reg.Acquire("key1", TypeString); err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Check type
	typ, exists := reg.Type("key1")
	if !exists || typ != TypeString {
		t.Errorf("expected TypeString, got %v, exists=%v", typ, exists)
	}

	// Acquire same key, same type — ok
	if err := reg.Acquire("key1", TypeString); err != nil {
		t.Errorf("same type should succeed: %v", err)
	}

	// Acquire same key, different type — error
	if err := reg.Acquire("key1", TypeList); err != ErrWrongType {
		t.Errorf("expected ErrWrongType, got %v", err)
	}
}

func TestRegistry_Release(t *testing.T) {
	ttl := NewTTLManager()
	reg := NewRegistry(ttl)

	reg.Acquire("key1", TypeString)
	reg.Release("key1")

	// After release, can acquire as different type
	if err := reg.Acquire("key1", TypeList); err != nil {
		t.Errorf("should allow different type after release: %v", err)
	}
}

func TestRegistry_ExpiredKeyReuse(t *testing.T) {
	ttl := NewTTLManager()
	reg := NewRegistry(ttl)

	// Acquire and set short TTL
	reg.Acquire("key1", TypeString)
	ttl.Set("key1", time.Now().Add(-1*time.Second)) // already expired

	// Should allow acquiring as different type (expired = gone)
	if err := reg.Acquire("key1", TypeList); err != nil {
		t.Errorf("should allow different type for expired key: %v", err)
	}
}

func TestRegistry_SetTTL(t *testing.T) {
	ttl := NewTTLManager()
	reg := NewRegistry(ttl)

	// SetTTL on non-existent key
	if reg.SetTTL("nokey", 1*time.Hour) {
		t.Error("should return false for non-existent key")
	}

	// SetTTL on existing key
	reg.Acquire("key1", TypeString)
	if !reg.SetTTL("key1", 1*time.Hour) {
		t.Error("should return true for existing key")
	}

	// Verify TTL is set
	result := reg.GetTTL("key1")
	if !result.HasTTL || result.Duration < 59*time.Minute {
		t.Errorf("expected TTL ~1 hour, got %v", result)
	}
}

func TestRegistry_Keys(t *testing.T) {
	ttl := NewTTLManager()
	reg := NewRegistry(ttl)

	reg.Acquire("str1", TypeString)
	reg.Acquire("str2", TypeString)
	reg.Acquire("list1", TypeList)

	stringKeys := reg.Keys(TypeString)
	if len(stringKeys) != 2 {
		t.Errorf("expected 2 string keys, got %d", len(stringKeys))
	}

	listKeys := reg.Keys(TypeList)
	if len(listKeys) != 1 {
		t.Errorf("expected 1 list key, got %d", len(listKeys))
	}
}
