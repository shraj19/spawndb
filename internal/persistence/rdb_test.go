// Package persistence handles saving and loading FlashDB state to disk.
package persistence

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shraj19/flashdb/internal/storage"
)

func resetStorage() {
	storage.DB.Clear()
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
				"project": "flashdb",
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
			resetStorage()
			testFile := fmt.Sprintf("test_%s.rdb", strings.ReplaceAll(tc.name, " ", "_"))
			defer os.Remove(testFile)

			// Set keys
			for k, v := range tc.keys {
				storage.DB.Set(k, v)
			}

			// Save
			err := Save(testFile)
			if err != nil {
				t.Fatalf("Save failed: %v", err)
			}

			// Clear and reload
			resetStorage()
			err = Load(testFile)
			if err != nil {
				t.Fatalf("Load failed: %v", err)
			}

			// Verify all keys restored correctly
			for k, expected := range tc.keys {
				got, ok := storage.DB.Get(k)
				if !ok {
					t.Errorf("key %q: not found", k)
				} else if got != expected {
					t.Errorf("key %q: expected %q, got %q", k, expected, got)
				}
			}

			// Verify no extra keys loaded
			if len(storage.DB.Keys()) != len(tc.keys) {
				t.Errorf("expected %d keys, got %d", len(tc.keys), len(storage.DB.Keys()))
			}
		})
	}
}

// TestSaveAndLoadWithTTL verifies TTL metadata is preserved for future expiry and absent for permanent keys.
func TestSaveAndLoadWithTTL(t *testing.T) {
	resetStorage()
	testFile := "test_ttl.rdb"
	defer os.Remove(testFile)

	// Setup: three keys with different TTL behaviors
	storage.DB.SetWithTTL("future", "expires_later", 1*time.Hour)
	storage.DB.SetWithTTL("expired", "should_not_load", -1*time.Hour)
	storage.DB.Set("permanent", "forever")

	// Save and reload
	if err := Save(testFile); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	resetStorage()
	if err := Load(testFile); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Check: future TTL preserved
	val, ok := storage.DB.Get("future")
	if !ok || val != "expires_later" {
		t.Error("future TTL: key should exist with correct value")
	}
	ttlResult := storage.DB.TTL("future")
	if !ttlResult.HasTTL {
		t.Error("future TTL: should have TTL")
	}

	// Check: expired TTL not loaded
	if storage.DB.Exists("expired") {
		t.Error("expired TTL: should not be loaded")
	}

	// Check: no TTL key exists and still has no TTL
	val, ok = storage.DB.Get("permanent")
	if !ok || val != "forever" {
		t.Error("no TTL: key should exist with correct value")
	}
	ttlResult = storage.DB.TTL("permanent")
	if ttlResult.HasTTL {
		t.Error("no TTL: should not have TTL")
	}

	// Verify total count (only 2 keys: future + permanent)
	if len(storage.DB.Keys()) != 2 {
		t.Errorf("expected 2 keys, got %d", len(storage.DB.Keys()))
	}
}

// TestLoadNonExistentFile verifies Load returns error when file doesn't exist.
func TestLoadNonExistentFile(t *testing.T) {
	resetStorage()

	err := Load("does_not_exist.rdb")
	if err == nil {
		t.Errorf("expected error for non-existent file")
	}

	if len(storage.DB.Keys()) != 0 {
		t.Errorf("storage should remain empty, got %d keys", len(storage.DB.Keys()))
	}
}

// TestSaveEmptyDatabase verifies saving and loading an empty database works without error.
func TestSaveEmptyDatabase(t *testing.T) {
	resetStorage()
	testFile := "test_empty.rdb"
	defer os.Remove(testFile)

	err := Save(testFile)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	err = Load(testFile)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(storage.DB.Keys()) != 0 {
		t.Errorf("storage should be empty, got %d keys", len(storage.DB.Keys()))
	}
}

// TestSaveAtomicWrite verifies Save uses temp file and cleans up after atomic rename.
func TestSaveAtomicWrite(t *testing.T) {
	resetStorage()
	testFile := "test_atomic.rdb"
	defer os.Remove(testFile)

	storage.DB.Set("key", "value")

	err := Save(testFile)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Temp file should be cleaned up
	if _, err := os.Stat(testFile + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file should not exist after save")
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
			resetStorage()
			testFile := fmt.Sprintf("test_count_%d.rdb", count)
			defer os.Remove(testFile)

			// Add keys
			for i := 0; i < count; i++ {
				storage.DB.Set(fmt.Sprintf("key%d", i), fmt.Sprintf("value%d", i))
			}

			err := Save(testFile)
			if err != nil {
				t.Fatalf("Save failed: %v", err)
			}

			resetStorage()

			err = Load(testFile)
			if err != nil {
				t.Fatalf("Load failed: %v", err)
			}

			if len(storage.DB.Keys()) != count {
				t.Errorf("expected %d keys, got %d", count, len(storage.DB.Keys()))
			}
		})
	}
}

// TestFileFormatValidation verifies Load rejects corrupted or invalid files.
func TestFileFormatValidation(t *testing.T) {
	resetStorage()

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

			resetStorage()
			err = Load(testFile)

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
	resetStorage()
	testFile := "test_large.rdb"
	defer os.Remove(testFile)

	// 1MB value
	largeValue := make([]byte, 1024*1024)
	for i := range largeValue {
		largeValue[i] = byte(i % 256)
	}

	storage.DB.Set("large", string(largeValue))

	err := Save(testFile)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	resetStorage()

	err = Load(testFile)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	got, ok := storage.DB.Get("large")
	if !ok || got != string(largeValue) {
		t.Errorf("large value corrupted after save/load")
	}
}
