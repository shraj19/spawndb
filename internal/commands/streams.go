package commands

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shraj19/flashdb/internal/resp"
	"github.com/shraj19/flashdb/internal/storage"
	"github.com/shraj19/flashdb/internal/utilities"
)

// --- Stream command factories (use DataStore) ---

// MakeXADD creates an XADD command handler with injected DataStore.
func MakeXADD(db storage.DataStore) storage.CommandHandler {
	return func(args []string, client *storage.Client) string {
		if len(args) < 5 || len(args)%2 != 1 {
			return resp.Error("wrong number of arguments for 'xadd'")
		}

		streamKey := args[1]
		inputID := args[2]

		// Build fields map
		fields := make(map[string]string)
		for i := 3; i < len(args); i += 2 {
			fields[args[i]] = args[i+1]
		}

		id, err := db.Streams().XAdd(streamKey, inputID, fields)
		if err == storage.ErrWrongType {
			return resp.Error("WRONGTYPE Operation against a key holding the wrong kind of value")
		}
		if err != nil {
			return resp.Error(err.Error())
		}

		return resp.BulkString(id)
	}
}

// MakeXRANGE creates an XRANGE command handler with injected DataStore.
func MakeXRANGE(db storage.DataStore) storage.CommandHandler {
	return func(args []string, client *storage.Client) string {
		if len(args) != 4 {
			return resp.Error("wrong number of arguments for 'xrange'")
		}

		streamKey, startID, endID := args[1], args[2], args[3]

		entries, err := db.Streams().XRange(streamKey, startID, endID)
		if err == storage.ErrWrongType {
			return resp.Error("WRONGTYPE Operation against a key holding the wrong kind of value")
		}
		if err != nil {
			return resp.Error(err.Error())
		}

		if len(entries) == 0 {
			return resp.EmptyArray()
		}

		var b strings.Builder
		encodeStreamEntries(&b, entries)
		return b.String()
	}
}

// MakeXREAD creates an XREAD command handler with injected DataStore.
func MakeXREAD(db storage.DataStore) storage.CommandHandler {
	return func(args []string, client *storage.Client) string {
		parsed, err := parseXREADArgs(args)
		if err != nil {
			return resp.Error(err.Error())
		}

		// Non-blocking read
		if parsed.blockTime < 0 {
			result := db.Streams().XRead(parsed.streamKeys, parsed.streamIDs)
			return encodeXREADResult(result)
		}

		// Blocking read
		timeout := time.Duration(parsed.blockTime) * time.Millisecond
		result, err := db.Streams().XReadBlock(parsed.streamKeys, parsed.streamIDs, timeout)
		if err != nil {
			return resp.Error(err.Error())
		}

		if result == nil {
			// Timeout - write directly
			client.Conn.Write([]byte(resp.NullArray()))
			return ""
		}

		// Got data - write directly
		client.Conn.Write([]byte(encodeXREADResult(result)))
		return ""
	}
}

// encodeXREADResult encodes map[string][]StreamEntry as RESP array.
func encodeXREADResult(result map[string][]storage.StreamEntry) string {
	if len(result) == 0 {
		return resp.NullArray()
	}

	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(result))
	for key, entries := range result {
		encodeStreamResult(&b, key, entries)
	}
	return b.String()
}

// --- Errors (legacy) ---

var (
	errInvalidIDFormat = errors.New("invalid stream ID format")
	errIDTooSmall      = errors.New("The ID specified in XADD is equal or smaller than the target stream top item")
	errIDMustBeGreater = errors.New("The ID specified in XADD must be greater than 0-0")
)

// --- ID helpers ---

// resolveStreamID resolves a stream entry ID from user input.
// inputID can be:
//   - "*" → auto-generate timestamp-sequence
//   - "<ts>-*" → use given timestamp, auto-generate sequence
//   - "<ts>-<seq>" → explicit ID (validated against lastID)
func resolveStreamID(inputID string, lastID string) (string, error) {
	if inputID == "*" {
		ts := time.Now().UnixMilli()
		if lastID == "" {
			return fmt.Sprintf("%d-0", ts), nil
		}
		lastTs, lastSeq := parseID(lastID)
		if ts > lastTs {
			return fmt.Sprintf("%d-0", ts), nil
		}
		return fmt.Sprintf("%d-%d", lastTs, lastSeq+1), nil
	}

	if strings.HasSuffix(inputID, "-*") {
		tsStr := strings.TrimSuffix(inputID, "-*")
		ts, err := strconv.ParseInt(tsStr, 10, 64)
		if err != nil || ts < 0 {
			return "", errInvalidIDFormat
		}

		if lastID == "" {
			if ts == 0 {
				return "0-1", nil
			}
			return fmt.Sprintf("%d-0", ts), nil
		}

		lastTs, lastSeq := parseID(lastID)
		if ts < lastTs {
			return "", errIDTooSmall
		}
		if ts == lastTs {
			return fmt.Sprintf("%d-%d", ts, lastSeq+1), nil
		}
		return fmt.Sprintf("%d-0", ts), nil
	}

	// Explicit ID
	parts := strings.Split(inputID, "-")
	if len(parts) != 2 {
		return "", errInvalidIDFormat
	}
	ts, err1 := strconv.ParseInt(parts[0], 10, 64)
	seq, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 != nil || err2 != nil || ts < 0 || seq < 0 {
		return "", errInvalidIDFormat
	}

	if ts == 0 && seq == 0 {
		return "", errIDMustBeGreater
	}

	if lastID != "" && utilities.CompareIDs(inputID, lastID) <= 0 {
		return "", errIDTooSmall
	}

	return inputID, nil
}

// parseID extracts timestamp and sequence from a valid ID string.
func parseID(id string) (ts int64, seq int64) {
	parts := strings.Split(id, "-")
	ts, _ = strconv.ParseInt(parts[0], 10, 64)
	seq, _ = strconv.ParseInt(parts[1], 10, 64)
	return
}

// --- Stream helpers ---

// getOrCreateStream returns the stream for key, creating if needed.
// Caller must hold storage.Mu lock.
func getOrCreateStream(streams map[string]*storage.Stream, key string) (*storage.Stream, bool) {
	stream, exists := streams[key]
	if !exists {
		stream = &storage.Stream{
			Entries: []storage.StreamEntry{},
			Index:   make(map[string]int),
		}
		streams[key] = stream
	}
	return stream, exists
}

// getLastID returns the last entry ID or empty string.
func getLastID(stream *storage.Stream) string {
	if len(stream.Entries) > 0 {
		return stream.Entries[len(stream.Entries)-1].ID
	}
	return ""
}

// --- Encoding helpers ---

func encodeStreamEntry(b *strings.Builder, entry storage.StreamEntry) {
	b.WriteString("*2\r\n")
	b.WriteString(resp.BulkString(entry.ID))
	fmt.Fprintf(b, "*%d\r\n", len(entry.Fields)*2)
	for k, v := range entry.Fields {
		b.WriteString(resp.BulkString(k))
		b.WriteString(resp.BulkString(v))
	}
}

func encodeStreamEntries(b *strings.Builder, entries []storage.StreamEntry) {
	fmt.Fprintf(b, "*%d\r\n", len(entries))
	for _, entry := range entries {
		encodeStreamEntry(b, entry)
	}
}

func encodeStreamResult(b *strings.Builder, streamKey string, entries []storage.StreamEntry) {
	b.WriteString("*2\r\n")
	b.WriteString(resp.BulkString(streamKey))
	encodeStreamEntries(b, entries)
}

// --- Search helpers ---

func searchEntryRange(entries []storage.StreamEntry, startID, endID string) (int, int) {
	n := len(entries)
	if n == 0 {
		return -1, -1
	}

	lo, hi := 0, n
	for lo < hi {
		mid := (lo + hi) / 2
		if utilities.CompareIDs(entries[mid].ID, startID) < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	startIdx := lo

	lo, hi = 0, n
	for lo < hi {
		mid := (lo + hi) / 2
		if utilities.CompareIDs(entries[mid].ID, endID) <= 0 {
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

func searchEntryAfter(entries []storage.StreamEntry, targetID string) int {
	lo, hi := 0, len(entries)
	for lo < hi {
		mid := (lo + hi) / 2
		if utilities.CompareIDs(entries[mid].ID, targetID) <= 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// --- Waiter helpers ---

// notifyWaiters wakes up waiters for streamKey if newID > their lastID.
// Caller must hold storage.Mu lock.
func notifyWaiters(streamKey, newID string) {
	waiters, ok := storage.StreamWaiters[streamKey]
	if !ok {
		return
	}

	var remaining []*storage.StreamWaiter
	for _, w := range waiters {
		if utilities.CompareIDs(newID, w.StreamIDs[streamKey]) > 0 {
			close(w.Done)
		} else {
			remaining = append(remaining, w)
		}
	}

	if len(remaining) == 0 {
		delete(storage.StreamWaiters, streamKey)
	} else {
		storage.StreamWaiters[streamKey] = remaining
	}
}

// unregisterWaiter removes waiter from all streamKeys.
// Caller must hold storage.Mu lock.
func unregisterWaiter(waiter *storage.StreamWaiter, streamKeys []string) {
	for _, key := range streamKeys {
		waiters := storage.StreamWaiters[key]
		for i, w := range waiters {
			if w == waiter {
				storage.StreamWaiters[key] = append(waiters[:i], waiters[i+1:]...)
				break
			}
		}
		if len(storage.StreamWaiters[key]) == 0 {
			delete(storage.StreamWaiters, key)
		}
	}
}

// --- XREAD parsing and query ---

type xreadArgs struct {
	blockTime  int // -1 = no block, 0 = infinite, >0 = milliseconds
	streamKeys []string
	streamIDs  []string
}

func parseXREADArgs(args []string) (*xreadArgs, error) {
	if len(args) < 4 {
		return nil, errors.New("wrong number of arguments for 'xread'")
	}

	result := &xreadArgs{blockTime: -1}
	streamsIdx := -1

	for i := 1; i < len(args); i++ {
		switch strings.ToUpper(args[i]) {
		case "BLOCK":
			if i+1 >= len(args) {
				return nil, errors.New("invalid BLOCK time")
			}
			bt, err := strconv.Atoi(args[i+1])
			if err != nil || bt < 0 {
				return nil, errors.New("invalid BLOCK time")
			}
			result.blockTime = bt
			i++
		case "STREAMS":
			streamsIdx = i
		}
		if streamsIdx != -1 {
			break
		}
	}

	if streamsIdx == -1 {
		return nil, errors.New("STREAMS keyword not found")
	}

	start := streamsIdx + 1
	remaining := len(args) - start
	if remaining == 0 || remaining%2 != 0 {
		return nil, errors.New("wrong number of arguments for 'xread'")
	}

	numKeys := remaining / 2
	result.streamKeys = args[start : start+numKeys]
	result.streamIDs = make([]string, numKeys)
	copy(result.streamIDs, args[start+numKeys:])

	return result, nil
}

// resolveDollarIDs replaces "$" with actual last IDs.
// Caller must hold storage.Mu lock.
func resolveDollarIDs(streamKeys, streamIDs []string, streams map[string]*storage.Stream) {
	for i, key := range streamKeys {
		if streamIDs[i] == "$" {
			if stream, ok := streams[key]; ok && len(stream.Entries) > 0 {
				streamIDs[i] = stream.Entries[len(stream.Entries)-1].ID
			} else {
				streamIDs[i] = "0-0"
			}
		}
	}
}

type streamQueryResult struct {
	key     string
	entries []storage.StreamEntry
}

// queryStreams fetches entries after given IDs for each stream.
// Returns results and whether any stream has entries.
// Caller must hold storage.Mu lock.
func queryStreams(streamKeys, streamIDs []string, streams map[string]*storage.Stream) ([]streamQueryResult, bool) {
	results := make([]streamQueryResult, len(streamKeys))
	hasEntry := false

	for i, key := range streamKeys {
		results[i].key = key

		stream, ok := streams[key]
		if !ok || len(stream.Entries) == 0 {
			continue
		}

		startIdx := searchEntryAfter(stream.Entries, streamIDs[i])
		if startIdx < len(stream.Entries) {
			results[i].entries = stream.Entries[startIdx:]
			hasEntry = true
		}
	}

	return results, hasEntry
}

// encodeXREADResponse encodes query results as RESP.
func encodeXREADResponse(results []streamQueryResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(results))
	for _, r := range results {
		encodeStreamResult(&b, r.key, r.entries)
	}
	return b.String()
}

// --- Commands ---

func XADD(args []string, client *storage.Client) string {
	if len(args) < 5 || len(args)%2 != 1 {
		return resp.Error("wrong number of arguments for 'xadd'")
	}

	streamKey := args[1]
	inputID := args[2]

	storage.Mu.Lock()
	defer storage.Mu.Unlock()

	streams := *storage.GetStreams()
	stream, _ := getOrCreateStream(streams, streamKey)
	lastID := getLastID(stream)

	id, err := resolveStreamID(inputID, lastID)
	if err != nil {
		return resp.Error(err.Error())
	}

	// Build entry
	entry := storage.StreamEntry{
		ID:     id,
		Fields: make(map[string]string),
	}
	for i := 3; i < len(args); i += 2 {
		entry.Fields[args[i]] = args[i+1]
	}

	// Add to stream
	stream.Entries = append(stream.Entries, entry)
	stream.Index[id] = len(stream.Entries) - 1

	// Notify blocked readers
	notifyWaiters(streamKey, id)

	return resp.BulkString(id)
}

func XRANGE(args []string, client *storage.Client) string {
	if len(args) != 4 {
		return resp.Error("wrong number of arguments for 'xrange'")
	}

	streamKey, startID, endID := args[1], args[2], args[3]

	storage.Mu.RLock()
	defer storage.Mu.RUnlock()

	streams := *storage.GetStreams()
	stream, ok := streams[streamKey]
	if !ok || len(stream.Entries) == 0 {
		return resp.EmptyArray()
	}

	entries := stream.Entries
	if startID == "-" {
		startID = entries[0].ID
	}
	if endID == "+" {
		endID = entries[len(entries)-1].ID
	}

	startIdx, endIdx := searchEntryRange(entries, startID, endID)
	if startIdx == -1 {
		return resp.EmptyArray()
	}

	var b strings.Builder
	encodeStreamEntries(&b, entries[startIdx:endIdx+1])
	return b.String()
}

func XREAD(args []string, client *storage.Client) string {
	parsed, err := parseXREADArgs(args)
	if err != nil {
		return resp.Error(err.Error())
	}

	storage.Mu.Lock()
	streams := *storage.GetStreams()

	resolveDollarIDs(parsed.streamKeys, parsed.streamIDs, streams)
	results, hasEntry := queryStreams(parsed.streamKeys, parsed.streamIDs, streams)

	// Return immediately if we have entries or not blocking
	if hasEntry || parsed.blockTime < 0 {
		storage.Mu.Unlock()
		return encodeXREADResponse(results)
	}

	// Block for new entries
	waiter := &storage.StreamWaiter{
		Conn:      client.Conn,
		Done:      make(chan struct{}),
		StreamIDs: make(map[string]string),
	}
	for i, key := range parsed.streamKeys {
		waiter.StreamIDs[key] = parsed.streamIDs[i]
		storage.StreamWaiters[key] = append(storage.StreamWaiters[key], waiter)
	}
	storage.Mu.Unlock()

	// Wait for notification or timeout
	var timeout <-chan time.Time
	if parsed.blockTime > 0 {
		timeout = time.After(time.Duration(parsed.blockTime) * time.Millisecond)
	}

	select {
	case <-waiter.Done:
		storage.Mu.Lock()
		unregisterWaiter(waiter, parsed.streamKeys)
		storage.Mu.Unlock()

		// Re-query with resolved IDs
		storage.Mu.RLock()
		streams := *storage.GetStreams()
		results, _ := queryStreams(parsed.streamKeys, parsed.streamIDs, streams)
		storage.Mu.RUnlock()

		client.Conn.Write([]byte(encodeXREADResponse(results)))

	case <-timeout:
		storage.Mu.Lock()
		unregisterWaiter(waiter, parsed.streamKeys)
		storage.Mu.Unlock()

		client.Conn.Write([]byte(resp.NullArray()))
	}

	return ""
}
