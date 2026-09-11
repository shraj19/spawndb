package storage

import (
	"sync"
	"testing"
)

func TestDataStore_CompositionRoot(t *testing.T) {
	db := NewDataStore()

	// All stores should be wired
	if db.Strings() == nil {
		t.Error("Strings() should not be nil")
	}
	if db.Lists() == nil {
		t.Error("Lists() should not be nil")
	}
	if db.Streams() == nil {
		t.Error("Streams() should not be nil")
	}
	if db.Registry() == nil {
		t.Error("Registry() should not be nil")
	}
}

func TestDataStore_TypeSafety(t *testing.T) {
	db := NewDataStore()

	// Create a string
	db.Strings().Set("mykey", "value")

	// Try to use as list — should fail
	_, err := db.Lists().RPush("mykey", "item")
	if err != ErrWrongType {
		t.Errorf("expected ErrWrongType, got %v", err)
	}

	// Try to use as stream — should fail
	_, err = db.Streams().XAdd("mykey", "*", map[string]string{"f": "v"})
	if err != ErrWrongType {
		t.Errorf("expected ErrWrongType, got %v", err)
	}
}

func TestDataStore_Type(t *testing.T) {
	db := NewDataStore()

	// Non-existent
	_, exists := db.Type("nokey")
	if exists {
		t.Error("should not exist")
	}

	// String
	db.Strings().Set("str", "value")
	typ, exists := db.Type("str")
	if !exists || typ != TypeString {
		t.Errorf("expected TypeString, got %v", typ)
	}

	// List
	db.Lists().RPush("list", "item")
	typ, exists = db.Type("list")
	if !exists || typ != TypeList {
		t.Errorf("expected TypeList, got %v", typ)
	}

	// Stream
	db.Streams().XAdd("stream", "1-0", map[string]string{"f": "v"})
	typ, exists = db.Type("stream")
	if !exists || typ != TypeStream {
		t.Errorf("expected TypeStream, got %v", typ)
	}
}

func TestDataStore_Exists(t *testing.T) {
	db := NewDataStore()

	if db.Exists("nokey") {
		t.Error("should not exist")
	}

	db.Strings().Set("key", "value")
	if !db.Exists("key") {
		t.Error("should exist")
	}
}

func TestDataStore_Delete(t *testing.T) {
	db := NewDataStore()

	// Delete non-existent
	if db.Delete("nokey") {
		t.Error("should return false for non-existent")
	}

	// Delete string
	db.Strings().Set("str", "value")
	if !db.Delete("str") {
		t.Error("should return true for existing string")
	}
	if db.Exists("str") {
		t.Error("string should be gone")
	}

	// Delete list
	db.Lists().RPush("list", "item")
	if !db.Delete("list") {
		t.Error("should return true for existing list")
	}
	if db.Exists("list") {
		t.Error("list should be gone")
	}

	// Delete stream
	db.Streams().XAdd("stream", "1-0", map[string]string{"f": "v"})
	if !db.Delete("stream") {
		t.Error("should return true for existing stream")
	}
	if db.Exists("stream") {
		t.Error("stream should be gone")
	}
}

func TestDataStore_CrossStoreInteraction(t *testing.T) {
	db := NewDataStore()

	// Create different types
	db.Strings().Set("s1", "value")
	db.Lists().RPush("l1", "item")
	db.Streams().XAdd("st1", "1-0", map[string]string{"f": "v"})

	// All should exist
	if !db.Exists("s1") || !db.Exists("l1") || !db.Exists("st1") {
		t.Error("all keys should exist")
	}

	// Delete one doesn't affect others
	db.Delete("s1")
	if db.Exists("s1") {
		t.Error("s1 should be deleted")
	}
	if !db.Exists("l1") || !db.Exists("st1") {
		t.Error("l1 and st1 should still exist")
	}

	// After delete, can create same key as different type
	db.Lists().RPush("s1", "now-a-list")
	typ, _ := db.Type("s1")
	if typ != TypeList {
		t.Errorf("s1 should now be TypeList, got %v", typ)
	}
}



// --- TOCTOU Race Condition Tests ---
// These tests verify that concurrent operations on the same key
// don't cause type/data divergence due to Time-Of-Check-Time-Of-Use races.

// TestDataStore_TOCTOU_SetVsRPush tests the critical race condition:
// Two goroutines concurrently try to create the SAME key as different types.
// Without atomicity, both can pass Acquire() before either writes data.
//
// Expected: Exactly ONE should succeed, the other gets ErrWrongType.
// Bug symptom: Both succeed, causing type confusion.
func TestDataStore_TOCTOU_SetVsRPush(t *testing.T) {
	// Run multiple iterations to increase chance of hitting the race
	for iter := 0; iter < 100; iter++ {
		db := NewDataStore()
		key := "contested_key"

		// Synchronization
		ready := make(chan struct{})
		done := make(chan error, 2)

		// Goroutine 1: SET (string) - using atomic db.Set()
		go func() {
			<-ready // Wait for start signal
			err := db.Set(key, "string_value")
			done <- err
		}()

		// Goroutine 2: RPUSH (list) - using atomic db.RPush()
		go func() {
			<-ready // Wait for start signal
			_, err := db.RPush(key, "list_item")
			done <- err
		}()

		// Start both simultaneously
		close(ready)

		// Collect results
		err1 := <-done
		err2 := <-done

		// Exactly one should succeed, one should fail with ErrWrongType
		successes := 0
		wrongTypeErrors := 0

		if err1 == nil {
			successes++
		} else if err1 == ErrWrongType {
			wrongTypeErrors++
		}

		if err2 == nil {
			successes++
		} else if err2 == ErrWrongType {
			wrongTypeErrors++
		}

		// CORRECT behavior: 1 success, 1 ErrWrongType
		// BUG behavior: 2 successes (both passed Acquire before either wrote data)
		if successes != 1 || wrongTypeErrors != 1 {
			t.Errorf("iteration %d: expected 1 success + 1 ErrWrongType, got %d successes, %d wrongType errors (err1=%v, err2=%v)",
				iter, successes, wrongTypeErrors, err1, err2)
		}

		// Verify consistency: type in registry should match actual data location
		typ, exists := db.Type(key)
		if !exists {
			t.Errorf("iteration %d: key should exist after one operation succeeded", iter)
			continue
		}

		switch typ {
		case TypeString:
			// Data should be in string store only
			val, ok := db.Get(key)
			if !ok || val != "string_value" {
				t.Errorf("iteration %d: registry says string but string store has no data", iter)
			}
			// List store should be empty
			llen, _ := db.LLen(key)
			if llen != 0 {
				t.Errorf("iteration %d: registry says string but list store has data (len=%d)", iter, llen)
			}
		case TypeList:
			// Data should be in list store only
			llen, _ := db.LLen(key)
			if llen != 1 {
				t.Errorf("iteration %d: registry says list but list store has wrong len (got %d, want 1)", iter, llen)
			}
			// String store should NOT have this key
			// (Note: we can't easily check this without exposing internals)
		default:
			t.Errorf("iteration %d: unexpected type %v", iter, typ)
		}
	}
}

// TestDataStore_TOCTOU_DataIntegrity verifies that after concurrent operations,
// the data is consistent (no phantom data, no missing data).
func TestDataStore_TOCTOU_DataIntegrity(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		db := NewDataStore()
		key := "key"

		ready := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)

		// Try to create as string - using atomic db.Set()
		go func() {
			defer wg.Done()
			<-ready
			db.Set(key, "value")
		}()

		// Try to create as list - using atomic db.RPush()
		go func() {
			defer wg.Done()
			<-ready
			db.RPush(key, "item")
		}()

		close(ready)
		wg.Wait()

		// Now check: the key MUST be in exactly one store (matching registry type)
		typ, exists := db.Type(key)
		if !exists {
			// This is actually a potential bug — if both failed, key doesn't exist
			// But that shouldn't happen — at least one should succeed
			t.Logf("iteration %d: key doesn't exist (possible race failure)", iter)
			continue
		}

		hasStringData := false
		if val, ok := db.Get(key); ok && val == "value" {
			hasStringData = true
		}

		hasListData := false
		if llen, _ := db.LLen(key); llen > 0 {
			hasListData = true
		}

		// Verify exactly one store has data, matching the type
		if typ == TypeString && !hasStringData {
			t.Errorf("iteration %d: type=string but no string data", iter)
		}
		if typ == TypeList && !hasListData {
			t.Errorf("iteration %d: type=list but no list data", iter)
		}
		if hasStringData && hasListData {
			t.Errorf("iteration %d: CRITICAL - data in BOTH stores! Type=%v", iter, typ)
		}
	}
}

// TestDataStore_TOCTOU_DeleteDuringSet tests the race between Delete and Set.
// If Delete runs between Set's Acquire and data write, the registry entry
// is removed but data still gets written → orphaned data.
func TestDataStore_TOCTOU_DeleteDuringSet(t *testing.T) {
	for iter := 0; iter < 100; iter++ {
		db := NewDataStore()
		key := "key"

		// Pre-create the key using atomic method
		db.Set(key, "original")

		ready := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)

		// Goroutine 1: Delete - using atomic db.Delete()
		go func() {
			defer wg.Done()
			<-ready
			db.Delete(key)
		}()

		// Goroutine 2: Set (overwrite) - using atomic db.Set()
		go func() {
			defer wg.Done()
			<-ready
			db.Set(key, "updated")
		}()

		close(ready)
		wg.Wait()

		// After both complete, state should be consistent:
		// Either key exists with "updated" value, or key doesn't exist
		typ, exists := db.Type(key)
		val, hasData := db.Get(key)

		if exists && hasData {
			// Key exists — should have data
			if val != "updated" && val != "original" {
				t.Errorf("iteration %d: unexpected value %q", iter, val)
			}
		} else if !exists && hasData {
			// BUG: Registry says no key, but data store has data (orphan)
			t.Errorf("iteration %d: ORPHAN DATA - registry empty but data=%q exists", iter, val)
		} else if exists && !hasData {
			// BUG: Registry says key exists, but no data (phantom)
			t.Errorf("iteration %d: PHANTOM KEY - registry has type=%v but no data", iter, typ)
		}
		// !exists && !hasData is fine — delete won the race
	}
}
