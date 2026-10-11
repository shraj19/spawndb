package commands

import (
	"strconv"
	"time"

	"github.com/shraj19/spawndb/internal/core"
	"github.com/shraj19/spawndb/internal/resp"
	"github.com/shraj19/spawndb/internal/storage"
)

// --- List command factories (use DataStore) ---

// MakeRPUSH creates an RPUSH command handler with injected DataStore.
func MakeRPUSH(db storage.DataStore) core.CommandHandler {
	return func(args []string, client *core.Client) string {
		if len(args) < 3 {
			return resp.Error("wrong number of arguments for 'rpush'")
		}

		key := args[1]
		values := args[2:]

		n, err := db.Lists().RPush(key, values...)
		if err == storage.ErrWrongType {
			return resp.Error("WRONGTYPE Operation against a key holding the wrong kind of value")
		}
		if err != nil {
			return resp.Error(err.Error())
		}

		return resp.Integer(int64(n))
	}
}

// MakeLPUSH creates an LPUSH command handler with injected DataStore.
func MakeLPUSH(db storage.DataStore) core.CommandHandler {
	return func(args []string, client *core.Client) string {
		if len(args) < 3 {
			return resp.Error("wrong number of arguments for 'lpush'")
		}

		key := args[1]
		values := args[2:]

		n, err := db.Lists().LPush(key, values...)
		if err == storage.ErrWrongType {
			return resp.Error("WRONGTYPE Operation against a key holding the wrong kind of value")
		}
		if err != nil {
			return resp.Error(err.Error())
		}

		return resp.Integer(int64(n))
	}
}

// MakeLRANGE creates an LRANGE command handler with injected DataStore.
func MakeLRANGE(db storage.DataStore) core.CommandHandler {
	return func(args []string, client *core.Client) string {
		if len(args) != 4 {
			return resp.Error("wrong number of arguments for 'lrange'")
		}

		key := args[1]
		start, err1 := strconv.Atoi(args[2])
		stop, err2 := strconv.Atoi(args[3])
		if err1 != nil || err2 != nil {
			return resp.Error("start or stop is not an integer")
		}

		items, err := db.Lists().LRange(key, start, stop)
		if err == storage.ErrWrongType {
			return resp.Error("WRONGTYPE Operation against a key holding the wrong kind of value")
		}
		if err != nil {
			return resp.Error(err.Error())
		}

		if len(items) == 0 {
			return resp.EmptyArray()
		}
		return resp.Array(items)
	}
}

// MakeLLEN creates an LLEN command handler with injected DataStore.
func MakeLLEN(db storage.DataStore) core.CommandHandler {
	return func(args []string, client *core.Client) string {
		if len(args) < 2 {
			return resp.Error("wrong number of arguments for 'llen'")
		}

		n, err := db.Lists().LLen(args[1])
		if err == storage.ErrWrongType {
			return resp.Error("WRONGTYPE Operation against a key holding the wrong kind of value")
		}
		if err != nil {
			return resp.Error(err.Error())
		}

		return resp.Integer(int64(n))
	}
}

// MakeLPOP creates an LPOP command handler with injected DataStore.
func MakeLPOP(db storage.DataStore) core.CommandHandler {
	return func(args []string, client *core.Client) string {
		if len(args) < 2 {
			return resp.Error("wrong number of arguments for 'lpop'")
		}

		count := 1
		if len(args) >= 3 {
			n, err := strconv.Atoi(args[2])
			if err != nil || n < 0 {
				return resp.Error("count is not a valid integer")
			}
			count = n
		}

		items, err := db.Lists().LPop(args[1], count)
		if err == storage.ErrWrongType {
			return resp.Error("WRONGTYPE Operation against a key holding the wrong kind of value")
		}
		if err != nil {
			return resp.Error(err.Error())
		}

		if len(items) == 0 {
			if count > 1 {
				return resp.EmptyArray()
			}
			return resp.NullBulkString()
		}

		if count == 1 {
			return resp.BulkString(items[0])
		}
		return resp.Array(items)
	}
}

// MakeBLPOP creates a BLPOP command handler with injected DataStore.
func MakeBLPOP(db storage.DataStore) core.CommandHandler {
	return func(args []string, client *core.Client) string {
		if len(args) < 3 {
			return resp.Error("wrong number of arguments for 'blpop'")
		}

		// Parse keys and timeout (last arg is timeout)
		keys := args[1 : len(args)-1]
		timeoutSec, err := strconv.ParseFloat(args[len(args)-1], 64)
		if err != nil {
			return resp.Error("timeout is not a float")
		}

		timeout := time.Duration(timeoutSec * float64(time.Second))

		key, value, ok := db.Lists().BLPop(keys, timeout)
		if !ok {
			// Timeout - write directly and return empty
			client.Conn.Write([]byte(resp.NullArray()))
			return ""
		}

		// Got data - write directly and return empty
		client.Conn.Write([]byte(resp.Array([]string{key, value})))
		return ""
	}
}
