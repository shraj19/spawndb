package commands

import (
	"strconv"
	"strings"
	"time"

	"github.com/shraj19/flashdb/internal/resp"
	"github.com/shraj19/flashdb/internal/storage"
)

// --- String command factories (use DataStore) ---

// MakeSET creates a SET command handler with injected DataStore.
func MakeSET(db storage.DataStore) storage.CommandHandler {
	return func(args []string, client *storage.Client) string {
		if len(args) < 3 {
			return resp.Error("wrong number of arguments for 'set'")
		}

		key, value := args[1], args[2]

		// Parse options
		var ttl time.Duration
		hasTTL := false

		if len(args) >= 5 {
			switch strings.ToUpper(args[3]) {
			case "EX":
				seconds, err := strconv.Atoi(args[4])
				if err != nil {
					return resp.Error("invalid expire time")
				}
				ttl = time.Duration(seconds) * time.Second
				hasTTL = true
			case "PX":
				ms, err := strconv.Atoi(args[4])
				if err != nil {
					return resp.Error("invalid expire time")
				}
				ttl = time.Duration(ms) * time.Millisecond
				hasTTL = true
			default:
				return resp.Error("syntax error")
			}
		}

		// Store the value using DataStore
		var err error
		if hasTTL {
			err = db.Strings().SetWithTTL(key, value, ttl)
		} else {
			err = db.Strings().Set(key, value)
		}

		if err != nil {
			return resp.Error(err.Error())
		}

		// Replicate to replicas
		propagateToReplicas(args)

		return resp.OK()
	}
}

// MakeGET creates a GET command handler with injected DataStore.
func MakeGET(db storage.DataStore) storage.CommandHandler {
	return func(args []string, client *storage.Client) string {
		if len(args) < 2 {
			return resp.Error("wrong number of arguments for 'get'")
		}

		key := args[1]
		value, ok := db.Strings().Get(key)

		if !ok {
			return resp.NullBulkString()
		}
		return resp.BulkString(value)
	}
}

// MakeTYPE creates a TYPE command handler with injected DataStore.
func MakeTYPE(db storage.DataStore) storage.CommandHandler {
	return func(args []string, client *storage.Client) string {
		if len(args) != 2 {
			return resp.Error("wrong number of arguments for 'type'")
		}

		key := args[1]
		typ, exists := db.Type(key)

		if !exists {
			return resp.SimpleString("none")
		}

		return resp.SimpleString(typ.String())
	}
}

// MakeINCR creates an INCR command handler with injected DataStore.
func MakeINCR(db storage.DataStore) storage.CommandHandler {
	return func(args []string, client *storage.Client) string {
		if len(args) < 2 {
			return resp.Error("wrong number of arguments for 'incr'")
		}

		key := args[1]
		num, err := db.Strings().IncrBy(key, 1)

		if err == storage.ErrWrongType {
			return resp.Error("WRONGTYPE Operation against a key holding the wrong kind of value")
		}
		if err == storage.ErrNotInt {
			return resp.Error("value is not an integer or out of range")
		}
		if err != nil {
			return resp.Error(err.Error())
		}

		// Replicate
		propagateToReplicas(args)

		return resp.Integer(num)
	}
}

// propagateToReplicas sends command to all connected replicas.
func propagateToReplicas(args []string) {
	encoded, err := resp.EncodeCommand(args)
	if err != nil {
		return
	}

	replication := storage.EnsureReplication()
	replication.IncreaseOffset(int64(len(encoded)))
	replication.Mu.RLock()
	for _, rep := range replication.Replicas {
		if rep != nil && rep.Connected && rep.Conn != nil {
			_, _ = rep.Conn.Write(encoded)
		}
	}
	replication.Mu.RUnlock()
}

// --- Legacy handlers (for backward compatibility during migration) ---

func SETvalue(args []string, client *storage.Client) string {
	if len(args) < 3 {
		return resp.Error("wrong number of arguments for 'set'")
	}

	key, value := args[1], args[2]

	// Parse options
	var ttl time.Duration
	hasTTL := false

	if len(args) >= 5 {
		switch strings.ToUpper(args[3]) {
		case "EX":
			// EX option specifies the expiration time in seconds
			seconds, err := strconv.Atoi(args[4])
			if err != nil {
				return resp.Error("invalid expire time")
			}
			ttl = time.Duration(seconds) * time.Second
			hasTTL = true
		case "PX":
			// PX option specifies the expiration time in milliseconds
			ms, err := strconv.Atoi(args[4])
			if err != nil {
				return resp.Error("invalid expire time")
			}
			ttl = time.Duration(ms) * time.Millisecond
			hasTTL = true
		default:
			return resp.Error("syntax error")
		}
	}

	// Store the value
	if hasTTL {
		storage.DB.SetWithTTL(key, value, ttl)
	} else {
		storage.DB.Set(key, value)
	}

	// Replicate to replicas
	propagateToReplicas(args)
	return resp.OK()
}

func GETvalue(args []string, client *storage.Client) string {
	if len(args) < 2 {
		return resp.Error("wrong number of arguments for 'get'")
	}

	key := args[1]
	value, ok := storage.DB.Get(key)

	if !ok {
		return resp.NullBulkString()
	}
	return resp.BulkString(value)
}

func TYPE(args []string, client *storage.Client) string {
	if len(args) != 2 {
		return resp.Error("wrong number of arguments for 'type'")
	}

	key := args[1]

	// Check string type
	if storage.DB.Exists(key) {
		return resp.SimpleString("string")
	}

	// Check stream type (still using old global for now)
	storage.Mu.RLock()
	_, isStream := storage.Streams[key]
	storage.Mu.RUnlock()
	if isStream {
		return resp.SimpleString("stream")
	}

	// Check list type
	storage.Mu.RLock()
	_, isList := storage.Lists[key]
	storage.Mu.RUnlock()
	if isList {
		return resp.SimpleString("list")
	}

	return resp.SimpleString("none")
}

func INCR(args []string, client *storage.Client) string {
	if len(args) < 2 {
		return resp.Error("wrong number of arguments for 'incr'")
	}

	key := args[1]

	// Need to do atomic read-modify-write, so use lock directly
	// TODO: Add IncrBy method to Database for cleaner atomicity
	storage.DB.Lock()
	defer storage.DB.Unlock()

	val, exists := storage.DB.GetUnlocked(key)
	if !exists {
		val = "0"
	}

	num, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return resp.Error("value is not an integer or out of range")
	}

	num++
	storage.DB.SetUnlocked(key, strconv.FormatInt(num, 10))

	// Replicate
	propagateToReplicas(args)

	return resp.Integer(num)
}
