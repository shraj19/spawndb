package commands

import (
	"strconv"
	"strings"
	"time"

	"github.com/shraj19/flashdb/internal/core"
	"github.com/shraj19/flashdb/internal/replication"
	"github.com/shraj19/flashdb/internal/resp"
	"github.com/shraj19/flashdb/internal/storage"
)

// --- String command factories (use DataStore) ---

// MakeSET creates a SET command handler with injected DataStore.
func MakeSET(db storage.DataStore) core.CommandHandler {
	return func(args []string, client *core.Client) string {
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
func MakeGET(db storage.DataStore) core.CommandHandler {
	return func(args []string, client *core.Client) string {
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
func MakeTYPE(db storage.DataStore) core.CommandHandler {
	return func(args []string, client *core.Client) string {
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
func MakeINCR(db storage.DataStore) core.CommandHandler {
	return func(args []string, client *core.Client) string {
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

	repl := replication.EnsureState()
	repl.IncreaseOffset(int64(len(encoded)))
	repl.Mu.RLock()
	for _, rep := range repl.Replicas {
		if rep != nil && rep.Connected && rep.Conn != nil {
			_, _ = rep.Conn.Write(encoded)
		}
	}
	repl.Mu.RUnlock()
}
