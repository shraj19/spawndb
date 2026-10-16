package commands

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shraj19/spawndb/internal/core"
	"github.com/shraj19/spawndb/internal/resp"
	"github.com/shraj19/spawndb/internal/storage"
	"github.com/shraj19/spawndb/internal/utilities"
)

// --- Stream command factories (use DataStore) ---

// MakeXADD creates an XADD command handler with injected DataStore.
func MakeXADD(db storage.DataStore) core.CommandHandler {
	return func(args []string, client *core.Client) string {
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

		// Replicate with RESOLVED ID (not "*") so replica has identical stream
		// Build: XADD streamKey resolvedID field1 value1 field2 value2 ...
		replicateArgs := make([]string, 0, len(args))
		replicateArgs = append(replicateArgs, "XADD", streamKey, id) // Use resolved ID
		for i := 3; i < len(args); i++ {
			replicateArgs = append(replicateArgs, args[i])
		}
		propagateToReplicas(replicateArgs)

		return resp.BulkString(id)
	}
}

// MakeXRANGE creates an XRANGE command handler with injected DataStore.
func MakeXRANGE(db storage.DataStore) core.CommandHandler {
	return func(args []string, client *core.Client) string {
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
func MakeXREAD(db storage.DataStore) core.CommandHandler {
	return func(args []string, client *core.Client) string {
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

// --- XREAD parsing ---

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
