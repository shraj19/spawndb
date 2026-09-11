package storage

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Stream errors
var (
	ErrInvalidIDFormat = errors.New("invalid stream ID format")
	ErrIDTooSmall      = errors.New("The ID specified in XADD is equal or smaller than the target stream top item")
	ErrIDMustBeGreater = errors.New("The ID specified in XADD must be greater than 0-0")
)

// Note: Stream and StreamEntry types are defined in storage.go
// They will be consolidated here after migration is complete.

// streamStore implements StreamStore interface.
type streamStore struct {
	data     map[string]*Stream
	waiters  map[string][]*StreamWaiter
	registry Registry
	mu       sync.RWMutex
}

// NewStreamStore creates a new stream store.
func NewStreamStore(registry Registry) StreamStore {
	return &streamStore{
		data:     make(map[string]*Stream),
		waiters:  make(map[string][]*StreamWaiter),
		registry: registry,
	}
}

func (s *streamStore) XAdd(key, inputID string, fields map[string]string) (string, error) {
	// Register type
	if err := s.registry.Acquire(key, TypeStream); err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	stream := s.getOrCreateStream(key)
	lastID := s.getLastID(stream)

	id, err := s.resolveStreamID(inputID, lastID)
	if err != nil {
		return "", err
	}

	entry := StreamEntry{
		ID:     id,
		Fields: fields,
	}

	stream.Entries = append(stream.Entries, entry)
	stream.Index[id] = len(stream.Entries) - 1

	// Notify blocked readers
	s.notifyWaiters(key, id)

	return id, nil
}

func (s *streamStore) XRange(key, startID, endID string) ([]StreamEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stream, ok := s.data[key]
	if !ok || len(stream.Entries) == 0 {
		return []StreamEntry{}, nil
	}

	entries := stream.Entries

	// Handle special IDs
	if startID == "-" {
		startID = entries[0].ID
	}
	if endID == "+" {
		endID = entries[len(entries)-1].ID
	}

	startIdx, endIdx := s.searchEntryRange(entries, startID, endID)
	if startIdx == -1 {
		return []StreamEntry{}, nil
	}

	// Return deep copy
	result := make([]StreamEntry, endIdx-startIdx+1)
	for i, entry := range entries[startIdx : endIdx+1] {
		result[i] = StreamEntry{
			ID:     entry.ID,
			Fields: make(map[string]string, len(entry.Fields)),
		}
		for k, v := range entry.Fields {
			result[i].Fields[k] = v
		}
	}
	return result, nil
}

func (s *streamStore) XRead(keys, ids []string) map[string][]StreamEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string][]StreamEntry)

	for i, key := range keys {
		stream, ok := s.data[key]
		if !ok || len(stream.Entries) == 0 {
			continue
		}

		targetID := ids[i]
		startIdx := s.searchEntryAfter(stream.Entries, targetID)
		if startIdx < len(stream.Entries) {
			// Copy entries
			entries := make([]StreamEntry, len(stream.Entries)-startIdx)
			copy(entries, stream.Entries[startIdx:])
			result[key] = entries
		}
	}

	return result
}

func (s *streamStore) XReadBlock(keys, ids []string, timeout time.Duration) (map[string][]StreamEntry, error) {
	s.mu.Lock()

	// Resolve "$" IDs to actual last IDs (must be under lock for atomicity)
	resolvedIDs := s.resolveReadIDs(keys, ids)

	// Check for existing data
	result, hasData := s.queryStreams(keys, resolvedIDs)

	if hasData {
		s.mu.Unlock()
		return result, nil
	}

	// No data — register waiter on all keys
	waiter := &StreamWaiter{
		Done:      make(chan struct{}),
		StreamIDs: make(map[string]string),
	}
	for i, key := range keys {
		waiter.StreamIDs[key] = resolvedIDs[i]
		s.waiters[key] = append(s.waiters[key], waiter)
	}
	s.mu.Unlock()

	// Wait for notification or timeout
	var timeoutCh <-chan time.Time
	if timeout > 0 {
		timeoutCh = time.After(timeout)
	}

	select {
	case <-waiter.Done:
		// Woken up — re-query
		s.mu.Lock()
		s.unregisterWaiter(waiter, keys)
		s.mu.Unlock()

		return s.XRead(keys, resolvedIDs), nil

	case <-timeoutCh:
		// Timeout
		s.mu.Lock()
		s.unregisterWaiter(waiter, keys)
		s.mu.Unlock()

		return nil, nil
	}
}

func (s *streamStore) RegisterWaiter(waiter *StreamWaiter) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for key := range waiter.StreamIDs {
		s.waiters[key] = append(s.waiters[key], waiter)
	}
}

func (s *streamStore) UnregisterWaiter(waiter *StreamWaiter) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for key := range waiter.StreamIDs {
		s.removeWaiterFromKey(key, waiter)
	}
}

func (s *streamStore) Delete(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, existed := s.data[key]
	delete(s.data, key)

	if existed {
		s.registry.Release(key)
	}

	return existed
}

// --- Internal helpers ---

func (s *streamStore) getOrCreateStream(key string) *Stream {
	stream, exists := s.data[key]
	if !exists {
		stream = &Stream{
			Entries: []StreamEntry{},
			Index:   make(map[string]int),
		}
		s.data[key] = stream
	}
	return stream
}

func (s *streamStore) getLastID(stream *Stream) string {
	if len(stream.Entries) > 0 {
		return stream.Entries[len(stream.Entries)-1].ID
	}
	return ""
}

func (s *streamStore) resolveStreamID(inputID string, lastID string) (string, error) {
	if inputID == "*" {
		ts := time.Now().UnixMilli()
		if lastID == "" {
			return fmt.Sprintf("%d-0", ts), nil
		}
		lastTs, lastSeq := s.parseID(lastID)
		if ts > lastTs {
			return fmt.Sprintf("%d-0", ts), nil
		}
		return fmt.Sprintf("%d-%d", lastTs, lastSeq+1), nil
	}

	if strings.HasSuffix(inputID, "-*") {
		tsStr := strings.TrimSuffix(inputID, "-*")
		ts, err := strconv.ParseInt(tsStr, 10, 64)
		if err != nil || ts < 0 {
			return "", ErrInvalidIDFormat
		}

		if lastID == "" {
			if ts == 0 {
				return "0-1", nil
			}
			return fmt.Sprintf("%d-0", ts), nil
		}

		lastTs, lastSeq := s.parseID(lastID)
		if ts < lastTs {
			return "", ErrIDTooSmall
		}
		if ts == lastTs {
			return fmt.Sprintf("%d-%d", ts, lastSeq+1), nil
		}
		return fmt.Sprintf("%d-0", ts), nil
	}

	// Explicit ID
	parts := strings.Split(inputID, "-")
	if len(parts) != 2 {
		return "", ErrInvalidIDFormat
	}
	ts, err1 := strconv.ParseInt(parts[0], 10, 64)
	seq, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 != nil || err2 != nil || ts < 0 || seq < 0 {
		return "", ErrInvalidIDFormat
	}

	if ts == 0 && seq == 0 {
		return "", ErrIDMustBeGreater
	}

	if lastID != "" && s.compareIDs(inputID, lastID) <= 0 {
		return "", ErrIDTooSmall
	}

	return inputID, nil
}

func (s *streamStore) parseID(id string) (ts int64, seq int64) {
	parts := strings.Split(id, "-")
	ts, _ = strconv.ParseInt(parts[0], 10, 64)
	seq, _ = strconv.ParseInt(parts[1], 10, 64)
	return
}

func (s *streamStore) compareIDs(a, b string) int {
	aTs, aSeq := s.parseID(a)
	bTs, bSeq := s.parseID(b)

	if aTs != bTs {
		if aTs < bTs {
			return -1
		}
		return 1
	}
	if aSeq != bSeq {
		if aSeq < bSeq {
			return -1
		}
		return 1
	}
	return 0
}

func (s *streamStore) searchEntryRange(entries []StreamEntry, startID, endID string) (int, int) {
	n := len(entries)
	if n == 0 {
		return -1, -1
	}

	// Binary search for start
	lo, hi := 0, n
	for lo < hi {
		mid := (lo + hi) / 2
		if s.compareIDs(entries[mid].ID, startID) < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	startIdx := lo

	// Binary search for end
	lo, hi = 0, n
	for lo < hi {
		mid := (lo + hi) / 2
		if s.compareIDs(entries[mid].ID, endID) <= 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	endIdx := lo - 1

	if startIdx > endIdx || startIdx >= n {
		return -1, -1
	}
	return startIdx, endIdx
}

func (s *streamStore) searchEntryAfter(entries []StreamEntry, targetID string) int {
	lo, hi := 0, len(entries)
	for lo < hi {
		mid := (lo + hi) / 2
		if s.compareIDs(entries[mid].ID, targetID) <= 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// resolveReadIDs replaces "$" with each stream's last entry ID.
// "$" means "only entries added after this call".
// Caller must hold lock.
func (s *streamStore) resolveReadIDs(keys, ids []string) []string {
	resolved := make([]string, len(ids))
	for i, key := range keys {
		if ids[i] != "$" {
			resolved[i] = ids[i]
			continue
		}
		if stream, ok := s.data[key]; ok && len(stream.Entries) > 0 {
			resolved[i] = stream.Entries[len(stream.Entries)-1].ID
		} else {
			resolved[i] = "0-0"
		}
	}
	return resolved
}

// queryStreams fetches entries after given IDs for each stream.
// Returns results map and whether any stream has entries.
// Caller must hold at least RLock.
func (s *streamStore) queryStreams(keys, ids []string) (map[string][]StreamEntry, bool) {
	result := make(map[string][]StreamEntry)
	hasData := false

	for i, key := range keys {
		stream, ok := s.data[key]
		if !ok || len(stream.Entries) == 0 {
			continue
		}

		startIdx := s.searchEntryAfter(stream.Entries, ids[i])
		if startIdx < len(stream.Entries) {
			entries := make([]StreamEntry, len(stream.Entries)-startIdx)
			copy(entries, stream.Entries[startIdx:])
			result[key] = entries
			hasData = true
		}
	}

	return result, hasData
}

func (s *streamStore) notifyWaiters(streamKey, newID string) {
	waiters, ok := s.waiters[streamKey]
	if !ok {
		return
	}

	var remaining []*StreamWaiter
	for _, w := range waiters {
		if s.compareIDs(newID, w.StreamIDs[streamKey]) > 0 {
			close(w.Done)
		} else {
			remaining = append(remaining, w)
		}
	}

	if len(remaining) == 0 {
		delete(s.waiters, streamKey)
	} else {
		s.waiters[streamKey] = remaining
	}
}

func (s *streamStore) unregisterWaiter(waiter *StreamWaiter, keys []string) {
	for _, key := range keys {
		s.removeWaiterFromKey(key, waiter)
	}
}

func (s *streamStore) removeWaiterFromKey(key string, waiter *StreamWaiter) {
	waiters := s.waiters[key]
	for i, w := range waiters {
		if w == waiter {
			s.waiters[key] = append(waiters[:i], waiters[i+1:]...)
			break
		}
	}
	if len(s.waiters[key]) == 0 {
		delete(s.waiters, key)
	}
}
