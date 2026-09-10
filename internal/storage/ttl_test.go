package storage

import (
	"testing"
	"time"
)

func TestTTLManager_SetAndCheck(t *testing.T) {
	ttl := NewTTLManager()

	// No TTL = not expired
	if ttl.IsExpired("nokey") {
		t.Error("non-existent key should not be expired")
	}

	// Set TTL in future
	ttl.SetTTL("future", 1*time.Hour)
	if ttl.IsExpired("future") {
		t.Error("future TTL should not be expired")
	}

	// Set TTL in past
	ttl.Set("past", time.Now().Add(-1*time.Second))
	if !ttl.IsExpired("past") {
		t.Error("past TTL should be expired")
	}
}

func TestTTLManager_CheckAndDelete(t *testing.T) {
	ttl := NewTTLManager()

	// Set expired key
	ttl.Set("expired", time.Now().Add(-1*time.Second))

	// CheckAndDelete should return true and remove it
	if !ttl.CheckAndDelete("expired") {
		t.Error("should return true for expired key")
	}

	// Second call should return false (already deleted)
	if ttl.CheckAndDelete("expired") {
		t.Error("should return false after deletion")
	}
}

func TestTTLManager_Remaining(t *testing.T) {
	ttl := NewTTLManager()

	// No TTL
	if _, ok := ttl.Remaining("nokey"); ok {
		t.Error("should return false for non-existent key")
	}

	// Future TTL
	ttl.SetTTL("future", 1*time.Hour)
	rem, ok := ttl.Remaining("future")
	if !ok || rem < 59*time.Minute {
		t.Errorf("expected ~1 hour remaining, got %v", rem)
	}

	// Expired
	ttl.Set("past", time.Now().Add(-1*time.Second))
	if _, ok := ttl.Remaining("past"); ok {
		t.Error("should return false for expired key")
	}
}

func TestTTLManager_Snapshot(t *testing.T) {
	ttl := NewTTLManager()

	ttl.SetTTL("key1", 1*time.Hour)
	ttl.SetTTL("key2", 2*time.Hour)

	snapshot := ttl.Snapshot()

	if len(snapshot) != 2 {
		t.Errorf("expected 2 keys in snapshot, got %d", len(snapshot))
	}

	if _, ok := snapshot["key1"]; !ok {
		t.Error("snapshot missing key1")
	}

	// Modifying snapshot shouldn't affect original
	delete(snapshot, "key1")
	if ttl.IsExpired("key1") {
		t.Error("deleting from snapshot should not affect TTLManager")
	}
}
