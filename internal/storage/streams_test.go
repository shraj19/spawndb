package storage

import (
	"strings"
	"testing"
	"time"
)

func newStreamStoreWithDeps() (StreamStore, Registry) {
	ttl := NewTTLManager()
	reg := NewRegistry(ttl)
	store := NewStreamStore(reg)
	return store, reg
}

// --- XAdd Tests ---

func TestStreamStore_XAdd_AutoID(t *testing.T) {
	store, _ := newStreamStoreWithDeps()

	id, err := store.XAdd("stream1", "*", map[string]string{"field": "value"})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Should be timestamp-0 format
	if !strings.Contains(id, "-") {
		t.Errorf("expected ts-seq format, got %s", id)
	}

	parts := strings.Split(id, "-")
	if parts[1] != "0" {
		t.Errorf("expected seq 0 for first entry, got %s", parts[1])
	}
}

func TestStreamStore_XAdd_ExplicitID(t *testing.T) {
	store, _ := newStreamStoreWithDeps()

	id, err := store.XAdd("stream1", "1000-0", map[string]string{"f": "v"})
	if err != nil || id != "1000-0" {
		t.Errorf("expected 1000-0, got %s, err=%v", id, err)
	}

	// Second entry must be greater
	id, err = store.XAdd("stream1", "1000-1", map[string]string{"f": "v"})
	if err != nil || id != "1000-1" {
		t.Errorf("expected 1000-1, got %s, err=%v", id, err)
	}

	// Equal ID should fail
	_, err = store.XAdd("stream1", "1000-1", map[string]string{"f": "v"})
	if err != ErrIDTooSmall {
		t.Errorf("expected ErrIDTooSmall, got %v", err)
	}

	// Smaller ID should fail
	_, err = store.XAdd("stream1", "999-0", map[string]string{"f": "v"})
	if err != ErrIDTooSmall {
		t.Errorf("expected ErrIDTooSmall, got %v", err)
	}
}

func TestStreamStore_XAdd_PartialAutoID(t *testing.T) {
	store, _ := newStreamStoreWithDeps()

	// First entry: 1000-*  → 1000-0
	id, err := store.XAdd("stream1", "1000-*", map[string]string{"f": "v"})
	if err != nil || id != "1000-0" {
		t.Errorf("expected 1000-0, got %s, err=%v", id, err)
	}

	// Same timestamp: 1000-* → 1000-1
	id, err = store.XAdd("stream1", "1000-*", map[string]string{"f": "v"})
	if err != nil || id != "1000-1" {
		t.Errorf("expected 1000-1, got %s, err=%v", id, err)
	}

	// Higher timestamp: 2000-* → 2000-0
	id, err = store.XAdd("stream1", "2000-*", map[string]string{"f": "v"})
	if err != nil || id != "2000-0" {
		t.Errorf("expected 2000-0, got %s, err=%v", id, err)
	}

	// Lower timestamp should fail
	_, err = store.XAdd("stream1", "500-*", map[string]string{"f": "v"})
	if err != ErrIDTooSmall {
		t.Errorf("expected ErrIDTooSmall, got %v", err)
	}
}

func TestStreamStore_XAdd_ZeroID(t *testing.T) {
	store, _ := newStreamStoreWithDeps()

	// 0-0 is never valid
	_, err := store.XAdd("stream1", "0-0", map[string]string{"f": "v"})
	if err != ErrIDMustBeGreater {
		t.Errorf("expected ErrIDMustBeGreater, got %v", err)
	}

	// 0-1 is valid
	id, err := store.XAdd("stream1", "0-1", map[string]string{"f": "v"})
	if err != nil || id != "0-1" {
		t.Errorf("expected 0-1, got %s, err=%v", id, err)
	}

	// 0-* on empty stream should give 0-1
	store2, _ := newStreamStoreWithDeps()
	id, err = store2.XAdd("stream2", "0-*", map[string]string{"f": "v"})
	if err != nil || id != "0-1" {
		t.Errorf("expected 0-1 for 0-* on empty stream, got %s, err=%v", id, err)
	}
}

func TestStreamStore_XAdd_InvalidID(t *testing.T) {
	store, _ := newStreamStoreWithDeps()

	tests := []struct {
		id string
	}{
		{"invalid"},
		{"abc-def"},
		{"-1-0"},
		{"1"},
		{"1-2-3"},
	}

	for _, tt := range tests {
		_, err := store.XAdd("stream1", tt.id, map[string]string{"f": "v"})
		if err != ErrInvalidIDFormat {
			t.Errorf("id=%s: expected ErrInvalidIDFormat, got %v", tt.id, err)
		}
	}
}

func TestStreamStore_XAdd_WrongType(t *testing.T) {
	ttl := NewTTLManager()
	reg := NewRegistry(ttl)
	strings := NewStringStore(reg, ttl)
	streams := NewStreamStore(reg)

	// Create a string
	strings.Set("mykey", "value")

	// Try to add to stream — should fail
	_, err := streams.XAdd("mykey", "*", map[string]string{"f": "v"})
	if err != ErrWrongType {
		t.Errorf("expected ErrWrongType, got %v", err)
	}
}

// --- XRange Tests ---

func TestStreamStore_XRange(t *testing.T) {
	store, _ := newStreamStoreWithDeps()

	// Add entries
	store.XAdd("stream1", "1-0", map[string]string{"a": "1"})
	store.XAdd("stream1", "2-0", map[string]string{"b": "2"})
	store.XAdd("stream1", "3-0", map[string]string{"c": "3"})
	store.XAdd("stream1", "4-0", map[string]string{"d": "4"})
	store.XAdd("stream1", "5-0", map[string]string{"e": "5"})

	tests := []struct {
		name    string
		start   string
		end     string
		wantLen int
	}{
		{"full range with -/+", "-", "+", 5},
		{"exact range", "2-0", "4-0", 3},
		{"single entry", "3-0", "3-0", 1},
		{"no match", "10-0", "20-0", 0},
		{"partial overlap", "0-0", "2-0", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries, err := store.XRange("stream1", tt.start, tt.end)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if len(entries) != tt.wantLen {
				t.Errorf("expected %d entries, got %d", tt.wantLen, len(entries))
			}
		})
	}
}

func TestStreamStore_XRange_NonExistent(t *testing.T) {
	store, _ := newStreamStoreWithDeps()

	entries, err := store.XRange("nostream", "-", "+")
	if err != nil || len(entries) != 0 {
		t.Errorf("expected empty slice, got %v, err=%v", entries, err)
	}
}

func TestStreamStore_XRange_ReturnsCopy(t *testing.T) {
	store, _ := newStreamStoreWithDeps()

	store.XAdd("stream1", "1-0", map[string]string{"f": "original"})

	entries, _ := store.XRange("stream1", "-", "+")
	entries[0].Fields["f"] = "MODIFIED"

	// Original should be unchanged
	original, _ := store.XRange("stream1", "-", "+")
	if original[0].Fields["f"] != "original" {
		t.Error("XRange should return a copy")
	}
}

// --- XRead Tests ---

func TestStreamStore_XRead(t *testing.T) {
	store, _ := newStreamStoreWithDeps()

	store.XAdd("stream1", "1-0", map[string]string{"a": "1"})
	store.XAdd("stream1", "2-0", map[string]string{"b": "2"})
	store.XAdd("stream1", "3-0", map[string]string{"c": "3"})

	// Read after 1-0
	result := store.XRead([]string{"stream1"}, []string{"1-0"})
	if len(result["stream1"]) != 2 {
		t.Errorf("expected 2 entries after 1-0, got %d", len(result["stream1"]))
	}

	// Read after 0-0 (all)
	result = store.XRead([]string{"stream1"}, []string{"0-0"})
	if len(result["stream1"]) != 3 {
		t.Errorf("expected 3 entries after 0-0, got %d", len(result["stream1"]))
	}

	// Read after last (none)
	result = store.XRead([]string{"stream1"}, []string{"3-0"})
	if len(result["stream1"]) != 0 {
		t.Errorf("expected 0 entries after 3-0, got %d", len(result["stream1"]))
	}
}

func TestStreamStore_XRead_MultipleStreams(t *testing.T) {
	store, _ := newStreamStoreWithDeps()

	store.XAdd("s1", "1-0", map[string]string{"f": "v"})
	store.XAdd("s2", "1-0", map[string]string{"f": "v"})
	store.XAdd("s2", "2-0", map[string]string{"f": "v"})

	result := store.XRead([]string{"s1", "s2"}, []string{"0-0", "1-0"})

	if len(result["s1"]) != 1 {
		t.Errorf("expected 1 entry for s1, got %d", len(result["s1"]))
	}
	if len(result["s2"]) != 1 {
		t.Errorf("expected 1 entry for s2 (after 1-0), got %d", len(result["s2"]))
	}
}

// --- XReadBlock Tests ---

func TestStreamStore_XReadBlock_Immediate(t *testing.T) {
	store, _ := newStreamStoreWithDeps()

	store.XAdd("stream1", "1-0", map[string]string{"f": "v"})

	result, err := store.XReadBlock([]string{"stream1"}, []string{"0-0"}, 1*time.Second)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(result["stream1"]) != 1 {
		t.Errorf("expected 1 entry, got %d", len(result["stream1"]))
	}
}

func TestStreamStore_XReadBlock_DollarID(t *testing.T) {
	store, _ := newStreamStoreWithDeps()

	store.XAdd("stream1", "1-0", map[string]string{"f": "v"})

	done := make(chan struct{})

	go func() {
		// $ means "only new entries after this point"
		result, _ := store.XReadBlock([]string{"stream1"}, []string{"$"}, 5*time.Second)
		if len(result["stream1"]) != 1 || result["stream1"][0].ID != "2-0" {
			t.Errorf("expected entry 2-0, got %v", result)
		}
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)

	// Add new entry
	store.XAdd("stream1", "2-0", map[string]string{"f": "v"})

	select {
	case <-done:
		// success
	case <-time.After(1 * time.Second):
		t.Error("XReadBlock did not unblock")
	}
}

func TestStreamStore_XReadBlock_Blocking(t *testing.T) {
	store, _ := newStreamStoreWithDeps()

	done := make(chan struct{})

	go func() {
		result, _ := store.XReadBlock([]string{"stream1"}, []string{"0-0"}, 5*time.Second)
		if len(result["stream1"]) != 1 {
			t.Errorf("expected 1 entry, got %d", len(result["stream1"]))
		}
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)

	store.XAdd("stream1", "1-0", map[string]string{"f": "v"})

	select {
	case <-done:
		// success
	case <-time.After(1 * time.Second):
		t.Error("XReadBlock did not unblock")
	}
}

func TestStreamStore_XReadBlock_MultiStream(t *testing.T) {
	store, _ := newStreamStoreWithDeps()

	done := make(chan struct{})

	go func() {
		result, _ := store.XReadBlock([]string{"s1", "s2"}, []string{"0-0", "0-0"}, 5*time.Second)
		// Should get data from s2
		if len(result["s2"]) != 1 {
			t.Errorf("expected 1 entry for s2, got %v", result)
		}
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)

	// Add to s2 (not s1)
	store.XAdd("s2", "1-0", map[string]string{"f": "v"})

	select {
	case <-done:
		// success
	case <-time.After(1 * time.Second):
		t.Error("XReadBlock did not unblock on second stream")
	}
}

func TestStreamStore_XReadBlock_Timeout(t *testing.T) {
	store, _ := newStreamStoreWithDeps()

	start := time.Now()
	result, err := store.XReadBlock([]string{"empty"}, []string{"0-0"}, 100*time.Millisecond)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result on timeout, got %v", result)
	}
	if elapsed < 90*time.Millisecond {
		t.Errorf("timeout too fast: %v", elapsed)
	}
}

// --- Delete Tests ---

func TestStreamStore_Delete(t *testing.T) {
	store, reg := newStreamStoreWithDeps()

	store.XAdd("stream1", "1-0", map[string]string{"f": "v"})

	if !store.Delete("stream1") {
		t.Error("should return true for existing stream")
	}

	if store.Delete("stream1") {
		t.Error("should return false for non-existent stream")
	}

	if _, exists := reg.Type("stream1"); exists {
		t.Error("should be removed from registry")
	}
}
