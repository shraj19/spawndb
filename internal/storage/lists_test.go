package storage

import (
	"testing"
	"time"
)

func newListStoreWithDeps() (ListStore, Registry) {
	ttl := NewTTLManager()
	reg := NewRegistry(ttl)
	store := NewListStore(reg)
	return store, reg
}

func TestListStore_RPush(t *testing.T) {
	store, _ := newListStoreWithDeps()

	n, err := store.RPush("list1", "a", "b", "c")
	if err != nil || n != 3 {
		t.Errorf("expected 3, got %d, err=%v", n, err)
	}

	items, _ := store.LRange("list1", 0, -1)
	if len(items) != 3 || items[0] != "a" || items[2] != "c" {
		t.Errorf("wrong content: %v", items)
	}
}

func TestListStore_LPush(t *testing.T) {
	store, _ := newListStoreWithDeps()

	store.RPush("list1", "c")
	n, err := store.LPush("list1", "a", "b")
	if err != nil || n != 3 {
		t.Errorf("expected 3, got %d, err=%v", n, err)
	}

	// LPUSH "a", "b" → b pushed last, ends up at head
	items, _ := store.LRange("list1", 0, -1)
	if items[0] != "b" || items[1] != "a" || items[2] != "c" {
		t.Errorf("wrong order: %v (expected [b, a, c])", items)
	}
}

func TestListStore_LRange(t *testing.T) {
	store, _ := newListStoreWithDeps()
	store.RPush("list1", "a", "b", "c", "d", "e")

	tests := []struct {
		name  string
		start int
		stop  int
		want  []string
	}{
		{"full range", 0, -1, []string{"a", "b", "c", "d", "e"}},
		{"first 3", 0, 2, []string{"a", "b", "c"}},
		{"last 2", -2, -1, []string{"d", "e"}},
		{"middle", 1, 3, []string{"b", "c", "d"}},
		{"single", 2, 2, []string{"c"}},
		{"out of bounds", 0, 100, []string{"a", "b", "c", "d", "e"}},
		{"empty (start > stop)", 3, 1, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := store.LRange("list1", tt.start, tt.stop)
			if len(got) != len(tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got %v, want %v", got, tt.want)
					return
				}
			}
		})
	}
}

func TestListStore_LRange_NonExistent(t *testing.T) {
	store, _ := newListStoreWithDeps()

	items, err := store.LRange("nolist", 0, -1)
	if err != nil || len(items) != 0 {
		t.Errorf("expected empty slice, got %v, err=%v", items, err)
	}
}

func TestListStore_LRange_ReturnsCopy(t *testing.T) {
	store, _ := newListStoreWithDeps()
	store.RPush("list1", "a", "b", "c")

	items, _ := store.LRange("list1", 0, -1)
	items[0] = "MODIFIED"

	// Original should be unchanged
	original, _ := store.LRange("list1", 0, -1)
	if original[0] != "a" {
		t.Error("LRange should return a copy, not expose internal slice")
	}
}

func TestListStore_LLen(t *testing.T) {
	store, _ := newListStoreWithDeps()

	// Empty
	n, _ := store.LLen("nolist")
	if n != 0 {
		t.Errorf("expected 0 for non-existent list, got %d", n)
	}

	store.RPush("list1", "a", "b", "c")
	n, _ = store.LLen("list1")
	if n != 3 {
		t.Errorf("expected 3, got %d", n)
	}
}

func TestListStore_LPop(t *testing.T) {
	store, reg := newListStoreWithDeps()
	store.RPush("list1", "a", "b", "c")

	// Pop 1
	items, _ := store.LPop("list1", 1)
	if len(items) != 1 || items[0] != "a" {
		t.Errorf("expected [a], got %v", items)
	}

	// Pop 2
	items, _ = store.LPop("list1", 2)
	if len(items) != 2 || items[0] != "b" || items[1] != "c" {
		t.Errorf("expected [b, c], got %v", items)
	}

	// List should be empty and removed from registry
	n, _ := store.LLen("list1")
	if n != 0 {
		t.Errorf("expected 0, got %d", n)
	}
	if _, exists := reg.Type("list1"); exists {
		t.Error("empty list should be removed from registry")
	}
}

func TestListStore_LPop_MoreThanExists(t *testing.T) {
	store, _ := newListStoreWithDeps()
	store.RPush("list1", "a", "b")

	items, _ := store.LPop("list1", 10)
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}
}

func TestListStore_LPop_NonExistent(t *testing.T) {
	store, _ := newListStoreWithDeps()

	items, err := store.LPop("nolist", 1)
	if err != nil || len(items) != 0 {
		t.Errorf("expected empty slice, got %v, err=%v", items, err)
	}
}

func TestListStore_Delete(t *testing.T) {
	store, reg := newListStoreWithDeps()
	store.RPush("list1", "a", "b")

	if !store.Delete("list1") {
		t.Error("should return true for existing list")
	}

	if store.Delete("list1") {
		t.Error("should return false for non-existent list")
	}

	if _, exists := reg.Type("list1"); exists {
		t.Error("should be removed from registry")
	}
}

func TestListStore_WrongType(t *testing.T) {
	ttl := NewTTLManager()
	reg := NewRegistry(ttl)
	strings := NewStringStore(reg, ttl)
	lists := NewListStore(reg)

	// Create a string
	strings.Set("mykey", "value")

	// Try to push to it as list — should fail
	_, err := lists.RPush("mykey", "item")
	if err != ErrWrongType {
		t.Errorf("expected ErrWrongType, got %v", err)
	}

	_, err = lists.LPush("mykey", "item")
	if err != ErrWrongType {
		t.Errorf("expected ErrWrongType, got %v", err)
	}
}

// --- BLPop Tests ---

func TestListStore_BLPop_Immediate(t *testing.T) {
	store, _ := newListStoreWithDeps()
	store.RPush("list1", "item1")

	key, val, ok := store.BLPop([]string{"list1"}, 1*time.Second)
	if !ok || key != "list1" || val != "item1" {
		t.Errorf("expected (list1, item1, true), got (%s, %s, %v)", key, val, ok)
	}
}

func TestListStore_BLPop_Priority(t *testing.T) {
	store, _ := newListStoreWithDeps()

	// Data in low priority list only
	store.RPush("low", "low_item")

	// BLPop checks high first, but high is empty, so returns low
	key, val, ok := store.BLPop([]string{"high", "medium", "low"}, 1*time.Second)
	if !ok || key != "low" || val != "low_item" {
		t.Errorf("expected (low, low_item, true), got (%s, %s, %v)", key, val, ok)
	}
}

func TestListStore_BLPop_PriorityOrder(t *testing.T) {
	store, _ := newListStoreWithDeps()

	// Data in both high and low
	store.RPush("high", "high_item")
	store.RPush("low", "low_item")

	// Should return high (first in list)
	key, val, ok := store.BLPop([]string{"high", "low"}, 1*time.Second)
	if !ok || key != "high" || val != "high_item" {
		t.Errorf("expected (high, high_item, true), got (%s, %s, %v)", key, val, ok)
	}
}

func TestListStore_BLPop_Blocking(t *testing.T) {
	store, _ := newListStoreWithDeps()

	done := make(chan struct{})

	// Consumer blocks
	go func() {
		key, val, ok := store.BLPop([]string{"queue"}, 5*time.Second)
		if !ok || key != "queue" || val != "job1" {
			t.Errorf("expected (queue, job1, true), got (%s, %s, %v)", key, val, ok)
		}
		close(done)
	}()

	// Give consumer time to block
	time.Sleep(50 * time.Millisecond)

	// Producer pushes
	store.RPush("queue", "job1")

	// Wait for consumer
	select {
	case <-done:
		// success
	case <-time.After(1 * time.Second):
		t.Error("BLPop did not unblock")
	}
}

func TestListStore_BLPop_MultiKeyBlocking(t *testing.T) {
	store, _ := newListStoreWithDeps()

	done := make(chan struct{})

	// Consumer waits on multiple keys
	go func() {
		key, val, ok := store.BLPop([]string{"high", "medium", "low"}, 5*time.Second)
		if !ok || key != "low" || val != "item" {
			t.Errorf("expected (low, item, true), got (%s, %s, %v)", key, val, ok)
		}
		close(done)
	}()

	// Give consumer time to register waiters on all keys
	time.Sleep(50 * time.Millisecond)

	// Push to LOW (not first key) — this tests the bug fix
	store.RPush("low", "item")

	// Should still unblock
	select {
	case <-done:
		// success — multi-key blocking works!
	case <-time.After(1 * time.Second):
		t.Error("BLPop did not unblock on non-first key")
	}
}

func TestListStore_BLPop_Timeout(t *testing.T) {
	store, _ := newListStoreWithDeps()

	start := time.Now()
	_, _, ok := store.BLPop([]string{"empty"}, 100*time.Millisecond)
	elapsed := time.Since(start)

	if ok {
		t.Error("should timeout and return false")
	}

	if elapsed < 90*time.Millisecond {
		t.Errorf("timeout too fast: %v", elapsed)
	}
}

func TestListStore_BLPop_WaiterCleanupOnTimeout(t *testing.T) {
	store, _ := newListStoreWithDeps()

	// Timeout immediately
	store.BLPop([]string{"key1", "key2"}, 1*time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	// Push should not wake a ghost waiter
	n, _ := store.RPush("key1", "item")
	if n != 1 {
		t.Errorf("expected 1 (item in list), got %d", n)
	}

	// Item should be in the list, not consumed by stale waiter
	items, _ := store.LRange("key1", 0, -1)
	if len(items) != 1 || items[0] != "item" {
		t.Errorf("expected [item], got %v", items)
	}
}
