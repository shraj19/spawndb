package commands

import (
	"strconv"
	"time"

	"github.com/shraj19/spawndb/internal/core"
	"github.com/shraj19/spawndb/internal/resp"
	"github.com/shraj19/spawndb/internal/storage"
)

// --- Key-level command factories (work on any data type) ---

// MakeDEL creates a DEL command handler with injected DataStore.
// DEL key [key ...] — deletes keys of any type, returns count deleted.
func MakeDEL(db storage.DataStore) core.CommandHandler {
	return func(args []string, client *core.Client) string {
		if len(args) < 2 {
			return resp.Error("wrong number of arguments for 'del'")
		}

		count := 0
		for _, key := range args[1:] {
			if db.Delete(key) {
				count++
			}
		}

		// Replicate (even if count=0, replica needs to try same keys)
		if count > 0 {
			propagateToReplicas(args)
		}

		return resp.Integer(int64(count))
	}
}

// MakeEXPIRE creates an EXPIRE command handler with injected DataStore.
// EXPIRE key seconds — sets TTL on any key type.
// Returns 1 if TTL was set, 0 if key doesn't exist.
func MakeEXPIRE(db storage.DataStore) core.CommandHandler {
	return func(args []string, client *core.Client) string {
		if len(args) != 3 {
			return resp.Error("wrong number of arguments for 'expire'")
		}

		key := args[1]
		seconds, err := strconv.Atoi(args[2])
		if err != nil {
			return resp.Error("value is not an integer or out of range")
		}

		ttl := time.Duration(seconds) * time.Second
		if db.SetTTL(key, ttl) {
			propagateToReplicas(args)
			return resp.Integer(1)
		}
		return resp.Integer(0)
	}
}

// MakeTTL creates a TTL command handler with injected DataStore.
// TTL key — returns remaining TTL in seconds.
// Returns -2 if key doesn't exist, -1 if key has no TTL.
func MakeTTL(db storage.DataStore) core.CommandHandler {
	return func(args []string, client *core.Client) string {
		if len(args) != 2 {
			return resp.Error("wrong number of arguments for 'ttl'")
		}

		key := args[1]
		ttl, _ := db.GetTTL(key)
		return resp.Integer(ttl)
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
