package storage

import "testing"

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
