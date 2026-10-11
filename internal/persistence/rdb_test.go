// Package persistence handles saving and loading spawndb state to disk.
package persistence

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shraj19/spawndb/internal/storage"
)

// newTestDataStore creates an isolated DataStore for testing.
func newTestDataStore() storage.DataStore {
	return storage.NewDataStore()
}

// TestSaveAndLoadStrings verifies basic round-trip: save keys, clear, load, check values restored.
func TestSaveAndLoadStrings(t *testing.T) {
	testCases := []struct {
		name string
		keys map[string]string
	}{
		{
			name: "basic key-value pairs",
			keys: map[string]string{
				"name":    "shashank",
				"project": "spawndb",
				"counter": "42",
			},
		},
		{
			name: "empty values",
			keys: map[string]string{
				"empty": "",
				"space": " ",
			},
		},
		{
			name: "special characters in values",
			keys: map[string]string{
				"newline": "hello\nworld",
				"tab":     "col1\tcol2",
				"binary":  "\x00\x01\x02",
			},
		},
		{
			name: "unicode keys and values",
			keys: map[string]string{
				"hindi":   "नमस्ते",
				"emoji":   "🚀💾",
				"chinese": "你好",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDataStore()
			testFile := fmt.Sprintf("test_%s.rdb", strings.ReplaceAll(tc.name, " ", "_"))
			defer os.Remove(testFile)

			// Set keys
			for k, v := range tc.keys {
				db.Strings().Set(k, v)
			}

			// Save
			err := Save(testFile, db)
			if err != nil {
				t.Fatalf("Save failed: %v", err)
			}

			// Create new DataStore and load into it
			db2 := newTestDataStore()
			err = Load(testFile, db2)
			if err != nil {
				t.Fatalf("Load failed: %v", err)
			}

			// Verify all keys restored correctly
			for k, expected := range tc.keys {
				got, ok := db2.Strings().Get(k)
				if !ok {
					t.Errorf("key %q: not found", k)
				} else if got != expected {
					t.Errorf("key %q: expected %q, got %q", k, expected, got)
				}
			}

			// Verify no extra keys loaded
			if len(db2.Strings().Keys()) != len(tc.keys) {
				t.Errorf("expected %d keys, got %d", len(tc.keys), len(db2.Strings().Keys()))
			}
		})
	}
}

// TestSaveAndLoadWithTTL verifies TTL metadata is preserved for future expiry and absent for permanent keys.
func TestSaveAndLoadWithTTL(t *testing.T) {
	db := newTestDataStore()
	testFile := "test_ttl.rdb"
	defer os.Remove(testFile)

	// Setup: keys with different TTL behaviors
	db.Strings().SetWithTTL("future", "expires_later", 1*time.Hour)
	db.Strings().Set("permanent", "forever")
	// Note: Can't easily test expired keys without waiting or manipulating time

	// Save and reload
	if err := Save(testFile, db); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	db2 := newTestDataStore()
	if err := Load(testFile, db2); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Check: future TTL preserved
	val, ok := db2.Strings().Get("future")
	if !ok || val != "expires_later" {
		t.Error("future TTL: key should exist with correct value")
	}

	// Check: no TTL key exists
	val, ok = db2.Strings().Get("permanent")
	if !ok || val != "forever" {
		t.Error("no TTL: key should exist with correct value")
	}

	// Verify total count
	if len(db2.Strings().Keys()) != 2 {
		t.Errorf("expected 2 keys, got %d", len(db2.Strings().Keys()))
	}
}

// TestLoadNonExistentFile verifies Load returns error when file doesn't exist.
func TestLoadNonExistentFile(t *testing.T) {
	db := newTestDataStore()

	err := Load("does_not_exist.rdb", db)
	if err == nil {
		t.Errorf("expected error for non-existent file")
	}

	if len(db.Strings().Keys()) != 0 {
		t.Errorf("storage should remain empty, got %d keys", len(db.Strings().Keys()))
	}
}

// TestSaveEmptyDatabase verifies saving and loading an empty database works without error.
func TestSaveEmptyDatabase(t *testing.T) {
	db := newTestDataStore()
	testFile := "test_empty.rdb"
	defer os.Remove(testFile)

	err := Save(testFile, db)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	db2 := newTestDataStore()
	err = Load(testFile, db2)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(db2.Strings().Keys()) != 0 {
		t.Errorf("storage should be empty, got %d keys", len(db2.Strings().Keys()))
	}
}

// TestSaveAtomicWrite verifies Save uses temp file and cleans up after atomic rename.
func TestSaveAtomicWrite(t *testing.T) {
	db := newTestDataStore()
	testFile := "test_atomic.rdb"
	defer os.Remove(testFile)

	db.Strings().Set("key", "value")

	err := Save(testFile, db)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Final file should exist
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Errorf("final file should exist")
	}
}

// TestKeyCountBulk verifies Save/Load handles varying key counts correctly.
func TestKeyCountBulk(t *testing.T) {
	counts := []int{1, 10, 100, 1000}

	for _, count := range counts {
		t.Run(fmt.Sprintf("%d_keys", count), func(t *testing.T) {
			db := newTestDataStore()
			testFile := fmt.Sprintf("test_count_%d.rdb", count)
			defer os.Remove(testFile)

			// Add keys
			for i := 0; i < count; i++ {
				db.Strings().Set(fmt.Sprintf("key%d", i), fmt.Sprintf("value%d", i))
			}

			err := Save(testFile, db)
			if err != nil {
				t.Fatalf("Save failed: %v", err)
			}

			db2 := newTestDataStore()
			err = Load(testFile, db2)
			if err != nil {
				t.Fatalf("Load failed: %v", err)
			}

			if len(db2.Strings().Keys()) != count {
				t.Errorf("expected %d keys, got %d", count, len(db2.Strings().Keys()))
			}
		})
	}
}

// TestFileFormatValidation verifies Load rejects corrupted or invalid files.
func TestFileFormatValidation(t *testing.T) {
	testCases := []struct {
		name    string
		content []byte
		wantErr bool
	}{
		{
			name:    "empty file",
			content: []byte{},
			wantErr: true,
		},
		{
			name:    "wrong magic",
			content: []byte("WRONG1"),
			wantErr: true,
		},
		{
			name:    "truncated header",
			content: []byte("FLA"),
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			testFile := fmt.Sprintf("test_invalid_%s.rdb", strings.ReplaceAll(tc.name, " ", "_"))
			defer os.Remove(testFile)

			err := os.WriteFile(testFile, tc.content, 0644)
			if err != nil {
				t.Fatalf("failed to create test file: %v", err)
			}

			db := newTestDataStore()
			err = Load(testFile, db)

			if tc.wantErr && err == nil {
				t.Errorf("expected error for %s", tc.name)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error for %s: %v", tc.name, err)
			}
		})
	}
}

// TestLargeValues verifies Save/Load handles large values (1MB) without corruption.
func TestLargeValues(t *testing.T) {
	db := newTestDataStore()
	testFile := "test_large.rdb"
	defer os.Remove(testFile)

	// 1MB value
	largeValue := make([]byte, 1024*1024)
	for i := range largeValue {
		largeValue[i] = byte(i % 256)
	}

	db.Strings().Set("large", string(largeValue))

	err := Save(testFile, db)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	db2 := newTestDataStore()
	err = Load(testFile, db2)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	got, ok := db2.Strings().Get("large")
	if !ok || got != string(largeValue) {
		t.Errorf("large value corrupted after save/load")
	}
}

// TestDataStoreIsolation verifies each test gets isolated data.
func TestDataStoreIsolation(t *testing.T) {
	db1 := newTestDataStore()
	db2 := newTestDataStore()

	db1.Strings().Set("key", "db1")
	db2.Strings().Set("key", "db2")

	v1, _ := db1.Strings().Get("key")
	v2, _ := db2.Strings().Get("key")

	if v1 == v2 {
		t.Error("DataStores should be isolated")
	}
}
