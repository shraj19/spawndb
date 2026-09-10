package storage

import (
	"testing"
	"time"
)

func newStringStoreWithDeps() (StringStore, Registry, TTLManager) {
	ttl := NewTTLManager()
	reg := NewRegistry(ttl)
	store := NewStringStore(reg, ttl)
	return store, reg, ttl
}

func TestStringStore_SetGet(t *testing.T) {
	store, _, _ := newStringStoreWithDeps()

	// Set and get
	if err := store.Set("name", "shashank"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	val, ok := store.Get("name")
	if !ok || val != "shashank" {
		t.Errorf("expected 'shashank', got '%s', ok=%v", val, ok)
	}

	// Get non-existent
	_, ok = store.Get("nokey")
	if ok {
		t.Error("should return false for non-existent key")
	}
}

func TestStringStore_SetWithTTL(t *testing.T) {
	store, _, ttl := newStringStoreWithDeps()

	// Set with TTL
	store.SetWithTTL("session", "abc123", 1*time.Hour)

	val, ok := store.Get("session")
	if !ok || val != "abc123" {
		t.Errorf("expected 'abc123', got '%s'", val)
	}

	// Check TTL is set
	rem, hasTTL := ttl.Remaining("session")
	if !hasTTL || rem < 59*time.Minute {
		t.Errorf("expected TTL ~1 hour, got %v", rem)
	}
}

func TestStringStore_ExpiredKey(t *testing.T) {
	store, _, ttl := newStringStoreWithDeps()

	store.Set("key1", "value1")
	ttl.Set("key1", time.Now().Add(-1*time.Second)) // expire it

	// Get should return not found and clean up
	val, ok := store.Get("key1")
	if ok {
		t.Errorf("expected not found for expired key, got '%s'", val)
	}
}

func TestStringStore_Exists(t *testing.T) {
	store, _, ttl := newStringStoreWithDeps()

	if store.Exists("nokey") {
		t.Error("should return false for non-existent key")
	}

	store.Set("key1", "value1")
	if !store.Exists("key1") {
		t.Error("should return true for existing key")
	}

	// Expired key
	ttl.Set("key1", time.Now().Add(-1*time.Second))
	if store.Exists("key1") {
		t.Error("should return false for expired key")
	}
}

func TestStringStore_WrongType(t *testing.T) {
	ttl := NewTTLManager()
	reg := NewRegistry(ttl)
	strings := NewStringStore(reg, ttl)
	lists := NewListStore(reg)

	// Create a list
	lists.LPush("mykey", "item1")

	// Try to set as string — should fail
	if err := strings.Set("mykey", "value"); err != ErrWrongType {
		t.Errorf("expected ErrWrongType, got %v", err)
	}
}

func TestStringStore_IncrBy(t *testing.T) {
	store, _, _ := newStringStoreWithDeps()

	// INCR on non-existent key
	val, err := store.IncrBy("counter", 1)
	if err != nil || val != 1 {
		t.Errorf("expected 1, got %d, err=%v", val, err)
	}

	// INCR again
	val, err = store.IncrBy("counter", 5)
	if err != nil || val != 6 {
		t.Errorf("expected 6, got %d, err=%v", val, err)
	}

	// Negative delta
	val, err = store.IncrBy("counter", -2)
	if err != nil || val != 4 {
		t.Errorf("expected 4, got %d, err=%v", val, err)
	}

	// INCR on non-integer
	store.Set("name", "shashank")
	_, err = store.IncrBy("name", 1)
	if err != ErrNotInt {
		t.Errorf("expected ErrNotInt, got %v", err)
	}
}

func TestStringStore_Delete(t *testing.T) {
	store, reg, _ := newStringStoreWithDeps()

	store.Set("key1", "value1")

	if !store.Delete("key1") {
		t.Error("delete should return true for existing key")
	}

	if store.Delete("key1") {
		t.Error("delete should return false for non-existent key")
	}

	// After delete, key should be gone from registry too
	if _, exists := reg.Type("key1"); exists {
		t.Error("key should be removed from registry after delete")
	}
}

func TestStringStore_Keys(t *testing.T) {
	store, _, _ := newStringStoreWithDeps()

	store.Set("key1", "val1")
	store.Set("key2", "val2")
	store.Set("key3", "val3")

	keys := store.Keys()
	if len(keys) != 3 {
		t.Errorf("expected 3 keys, got %d", len(keys))
	}
}

func TestStringStore_ForEach(t *testing.T) {
	store, _, _ := newStringStoreWithDeps()

	store.Set("key1", "val1")
	store.SetWithTTL("key2", "val2", 1*time.Hour)

	count := 0
	store.ForEach(func(key, value string, expiry time.Time, hasTTL bool) {
		count++
		if key == "key2" && !hasTTL {
			t.Error("key2 should have TTL")
		}
	})

	if count != 2 {
		t.Errorf("expected 2 iterations, got %d", count)
	}
}

func TestStringStore_Load(t *testing.T) {
	store, reg, ttlMgr := newStringStoreWithDeps()

	expiry := time.Now().Add(1 * time.Hour)
	store.Load("loaded", "value", expiry, true)

	val, ok := store.Get("loaded")
	if !ok || val != "value" {
		t.Errorf("expected 'value', got '%s'", val)
	}

	// Should be registered
	if typ, exists := reg.Type("loaded"); !exists || typ != TypeString {
		t.Error("loaded key should be registered as string")
	}

	// Should have TTL
	if _, hasTTL := ttlMgr.Remaining("loaded"); !hasTTL {
		t.Error("loaded key should have TTL")
	}
}
