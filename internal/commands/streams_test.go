package commands

import (
	"strings"
	"testing"

	"github.com/shraj19/spawndb/internal/storage"
)

// --- ID Helper Tests ---

func TestResolveStreamID(t *testing.T) {
	tests := []struct {
		name    string
		inputID string
		lastID  string
		want    string // exact match, empty if using check
		wantErr error
		check   func(t *testing.T, got string) // for dynamic IDs
	}{
		// Explicit IDs
		{
			name:    "explicit valid ID, empty stream",
			inputID: "1-0",
			lastID:  "",
			want:    "1-0",
		},
		{
			name:    "explicit valid ID, greater than last",
			inputID: "5-0",
			lastID:  "3-0",
			want:    "5-0",
		},
		{
			name:    "explicit ID equal to last",
			inputID: "3-0",
			lastID:  "3-0",
			wantErr: errIDTooSmall,
		},
		{
			name:    "explicit ID less than last",
			inputID: "2-0",
			lastID:  "3-0",
			wantErr: errIDTooSmall,
		},
		{
			name:    "0-0 always invalid",
			inputID: "0-0",
			lastID:  "",
			wantErr: errIDMustBeGreater,
		},
		{
			name:    "0-1 is valid",
			inputID: "0-1",
			lastID:  "",
			want:    "0-1",
		},
		{
			name:    "invalid format no dash",
			inputID: "12345",
			lastID:  "",
			wantErr: errInvalidIDFormat,
		},
		{
			name:    "invalid format non-numeric",
			inputID: "abc-def",
			lastID:  "",
			wantErr: errInvalidIDFormat,
		},
		{
			name:    "negative timestamp",
			inputID: "-1-0",
			lastID:  "",
			wantErr: errInvalidIDFormat,
		},

		// Auto-generate (*)
		{
			name:    "auto-generate star, empty stream",
			inputID: "*",
			lastID:  "",
			check: func(t *testing.T, got string) {
				if !strings.Contains(got, "-") {
					t.Errorf("expected ts-seq format, got %s", got)
				}
				parts := strings.Split(got, "-")
				if parts[1] != "0" {
					t.Errorf("expected seq 0 for new stream, got %s", parts[1])
				}
			},
		},
		{
			name:    "auto-generate star, existing stream",
			inputID: "*",
			lastID:  "1000-5",
			check: func(t *testing.T, got string) {
				if !strings.Contains(got, "-") {
					t.Errorf("expected ts-seq format, got %s", got)
				}
			},
		},

		// Partial auto-generate (<ts>-*)
		{
			name:    "partial auto, empty stream",
			inputID: "1000-*",
			lastID:  "",
			want:    "1000-0",
		},
		{
			name:    "partial auto, ts=0 empty stream",
			inputID: "0-*",
			lastID:  "",
			want:    "0-1", // 0-0 is never valid
		},
		{
			name:    "partial auto, same ts as last",
			inputID: "1000-*",
			lastID:  "1000-5",
			want:    "1000-6",
		},
		{
			name:    "partial auto, greater ts than last",
			inputID: "2000-*",
			lastID:  "1000-5",
			want:    "2000-0",
		},
		{
			name:    "partial auto, less ts than last",
			inputID: "500-*",
			lastID:  "1000-5",
			wantErr: errIDTooSmall,
		},
		{
			name:    "partial auto, invalid ts",
			inputID: "abc-*",
			lastID:  "",
			wantErr: errInvalidIDFormat,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveStreamID(tt.inputID, tt.lastID)

			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Errorf("want err %v, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if tt.check != nil {
				tt.check(t, got)
			} else if got != tt.want {
				t.Errorf("want %s, got %s", tt.want, got)
			}
		})
	}
}

func TestParseID(t *testing.T) {
	tests := []struct {
		id      string
		wantTs  int64
		wantSeq int64
	}{
		{"0-0", 0, 0},
		{"1-0", 1, 0},
		{"1000-5", 1000, 5},
		{"1726000000000-123", 1726000000000, 123},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			ts, seq := parseID(tt.id)
			if ts != tt.wantTs || seq != tt.wantSeq {
				t.Errorf("parseID(%s) = (%d, %d), want (%d, %d)",
					tt.id, ts, seq, tt.wantTs, tt.wantSeq)
			}
		})
	}
}

// --- Search Helper Tests ---

func makeEntries(ids ...string) []storage.StreamEntry {
	entries := make([]storage.StreamEntry, len(ids))
	for i, id := range ids {
		entries[i] = storage.StreamEntry{ID: id, Fields: map[string]string{}}
	}
	return entries
}

func TestSearchEntryRange(t *testing.T) {
	tests := []struct {
		name      string
		entries   []storage.StreamEntry
		startID   string
		endID     string
		wantStart int
		wantEnd   int
	}{
		{
			name:      "empty entries",
			entries:   makeEntries(),
			startID:   "0-0",
			endID:     "10-0",
			wantStart: -1,
			wantEnd:   -1,
		},
		{
			name:      "single entry, in range",
			entries:   makeEntries("5-0"),
			startID:   "0-0",
			endID:     "10-0",
			wantStart: 0,
			wantEnd:   0,
		},
		{
			name:      "single entry, before range",
			entries:   makeEntries("5-0"),
			startID:   "10-0",
			endID:     "20-0",
			wantStart: -1,
			wantEnd:   -1,
		},
		{
			name:      "single entry, after range",
			entries:   makeEntries("50-0"),
			startID:   "10-0",
			endID:     "20-0",
			wantStart: -1,
			wantEnd:   -1,
		},
		{
			name:      "multiple entries, full range",
			entries:   makeEntries("1-0", "2-0", "3-0", "4-0", "5-0"),
			startID:   "1-0",
			endID:     "5-0",
			wantStart: 0,
			wantEnd:   4,
		},
		{
			name:      "multiple entries, partial range",
			entries:   makeEntries("1-0", "2-0", "3-0", "4-0", "5-0"),
			startID:   "2-0",
			endID:     "4-0",
			wantStart: 1,
			wantEnd:   3,
		},
		{
			name:      "multiple entries, range with gaps",
			entries:   makeEntries("1-0", "3-0", "5-0", "7-0", "9-0"),
			startID:   "2-0",
			endID:     "8-0",
			wantStart: 1, // 3-0
			wantEnd:   3, // 7-0
		},
		{
			name:      "exact match single",
			entries:   makeEntries("1-0", "3-0", "5-0"),
			startID:   "3-0",
			endID:     "3-0",
			wantStart: 1,
			wantEnd:   1,
		},
		{
			name:      "no entries in range",
			entries:   makeEntries("1-0", "2-0", "8-0", "9-0"),
			startID:   "4-0",
			endID:     "6-0",
			wantStart: -1,
			wantEnd:   -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end := searchEntryRange(tt.entries, tt.startID, tt.endID)
			if start != tt.wantStart || end != tt.wantEnd {
				t.Errorf("searchEntryRange() = (%d, %d), want (%d, %d)",
					start, end, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

func TestSearchEntryAfter(t *testing.T) {
	tests := []struct {
		name     string
		entries  []storage.StreamEntry
		targetID string
		wantIdx  int
	}{
		{
			name:     "empty entries",
			entries:  makeEntries(),
			targetID: "5-0",
			wantIdx:  0,
		},
		{
			name:     "all entries after target",
			entries:  makeEntries("10-0", "20-0", "30-0"),
			targetID: "5-0",
			wantIdx:  0,
		},
		{
			name:     "all entries before or equal target",
			entries:  makeEntries("1-0", "2-0", "3-0"),
			targetID: "5-0",
			wantIdx:  3,
		},
		{
			name:     "target in middle",
			entries:  makeEntries("1-0", "3-0", "5-0", "7-0", "9-0"),
			targetID: "5-0",
			wantIdx:  3, // First entry > 5-0 is 7-0 at index 3
		},
		{
			name:     "target between entries",
			entries:  makeEntries("1-0", "3-0", "7-0", "9-0"),
			targetID: "5-0",
			wantIdx:  2, // First entry > 5-0 is 7-0 at index 2
		},
		{
			name:     "target equals first",
			entries:  makeEntries("5-0", "6-0", "7-0"),
			targetID: "5-0",
			wantIdx:  1,
		},
		{
			name:     "target equals last",
			entries:  makeEntries("1-0", "3-0", "5-0"),
			targetID: "5-0",
			wantIdx:  3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := searchEntryAfter(tt.entries, tt.targetID)
			if got != tt.wantIdx {
				t.Errorf("searchEntryAfter() = %d, want %d", got, tt.wantIdx)
			}
		})
	}
}

// --- XREAD Parsing Tests ---

func TestParseXREADArgs(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		wantBlockTime int
		wantKeys      []string
		wantIDs       []string
		wantErr       bool
	}{
		{
			name:          "basic single stream",
			args:          []string{"XREAD", "STREAMS", "mystream", "0"},
			wantBlockTime: -1,
			wantKeys:      []string{"mystream"},
			wantIDs:       []string{"0"},
		},
		{
			name:          "multiple streams",
			args:          []string{"XREAD", "STREAMS", "s1", "s2", "s3", "0", "0", "0"},
			wantBlockTime: -1,
			wantKeys:      []string{"s1", "s2", "s3"},
			wantIDs:       []string{"0", "0", "0"},
		},
		{
			name:          "with BLOCK",
			args:          []string{"XREAD", "BLOCK", "1000", "STREAMS", "mystream", "0"},
			wantBlockTime: 1000,
			wantKeys:      []string{"mystream"},
			wantIDs:       []string{"0"},
		},
		{
			name:          "BLOCK 0 (infinite)",
			args:          []string{"XREAD", "BLOCK", "0", "STREAMS", "mystream", "$"},
			wantBlockTime: 0,
			wantKeys:      []string{"mystream"},
			wantIDs:       []string{"$"},
		},
		{
			name:          "with dollar ID",
			args:          []string{"XREAD", "STREAMS", "mystream", "$"},
			wantBlockTime: -1,
			wantKeys:      []string{"mystream"},
			wantIDs:       []string{"$"},
		},
		{
			name:          "mixed IDs",
			args:          []string{"XREAD", "STREAMS", "s1", "s2", "1000-0", "$"},
			wantBlockTime: -1,
			wantKeys:      []string{"s1", "s2"},
			wantIDs:       []string{"1000-0", "$"},
		},
		{
			name:    "missing STREAMS keyword",
			args:    []string{"XREAD", "BLOCK", "1000", "mystream", "0"},
			wantErr: true,
		},
		{
			name:    "too few args",
			args:    []string{"XREAD", "STREAMS"},
			wantErr: true,
		},
		{
			name:    "odd number of keys/IDs",
			args:    []string{"XREAD", "STREAMS", "s1", "s2", "0"},
			wantErr: true,
		},
		{
			name:    "invalid BLOCK time",
			args:    []string{"XREAD", "BLOCK", "abc", "STREAMS", "mystream", "0"},
			wantErr: true,
		},
		{
			name:    "negative BLOCK time",
			args:    []string{"XREAD", "BLOCK", "-1", "STREAMS", "mystream", "0"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseXREADArgs(tt.args)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if got.blockTime != tt.wantBlockTime {
				t.Errorf("blockTime = %d, want %d", got.blockTime, tt.wantBlockTime)
			}

			if !sliceEqual(got.streamKeys, tt.wantKeys) {
				t.Errorf("streamKeys = %v, want %v", got.streamKeys, tt.wantKeys)
			}

			if !sliceEqual(got.streamIDs, tt.wantIDs) {
				t.Errorf("streamIDs = %v, want %v", got.streamIDs, tt.wantIDs)
			}
		})
	}
}

// --- Helpers ---

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
