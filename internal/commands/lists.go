package commands

import (
	"strconv"
	"time"

	"github.com/shraj19/flashdb/internal/resp"
	"github.com/shraj19/flashdb/internal/storage"
)

// --- List command factories (use DataStore) ---

// MakeRPUSH creates an RPUSH command handler with injected DataStore.
func MakeRPUSH(db storage.DataStore) storage.CommandHandler {
	return func(args []string, client *storage.Client) string {
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
func MakeLPUSH(db storage.DataStore) storage.CommandHandler {
	return func(args []string, client *storage.Client) string {
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
func MakeLRANGE(db storage.DataStore) storage.CommandHandler {
	return func(args []string, client *storage.Client) string {
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
func MakeLLEN(db storage.DataStore) storage.CommandHandler {
	return func(args []string, client *storage.Client) string {
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
func MakeLPOP(db storage.DataStore) storage.CommandHandler {
	return func(args []string, client *storage.Client) string {
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
func MakeBLPOP(db storage.DataStore) storage.CommandHandler {
	return func(args []string, client *storage.Client) string {
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

// --- Legacy handlers (for backward compatibility during migration) ---

func RPUSH(args []string, client *storage.Client) string {
	if len(args) < 3 {
		return resp.Error("wrong number of arguments for 'rpush'")
	}

	storage.Mu.Lock()
	defer storage.Mu.Unlock()

	key := args[1]
	list, _ := storage.Lists[key].([]string)

	// Values to append
	values := args[2:]

	// First, wake up blocked waiters (FIFO order)
	delivered := 0
	for len(storage.Waiters[key]) > 0 && len(values) > 0 {
		ch := storage.Waiters[key][0]
		storage.Waiters[key] = storage.Waiters[key][1:]

		v := values[0]
		values = values[1:]
		delivered++
		ch <- [2]string{key, v}
	}

	// Whatever remains, append to list
	if len(values) > 0 {
		list = append(list, values...)
	}

	storage.Lists[key] = list

	return resp.Integer(int64(len(list) + delivered))
}

func LPUSH(args []string, client *storage.Client) string {
	if len(args) < 3 {
		return resp.Error("wrong number of arguments for 'lpush'")
	}

	storage.Mu.Lock()
	defer storage.Mu.Unlock()
	key := args[1]
	list, _ := storage.Lists[key].([]string)

	// Values to prepend
	values := args[2:]
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}

	// Wake earliest waiters first
	delivered := 0
	for len(storage.Waiters[key]) > 0 && len(values) > 0 {
		ch := storage.Waiters[key][0]
		storage.Waiters[key] = storage.Waiters[key][1:]

		v := values[0]
		values = values[1:]
		delivered++
		ch <- [2]string{key, v}
	}

	// Prepend remaining values
	if len(values) > 0 {
		list = append(values, list...)
	}

	storage.Lists[key] = list
	return resp.Integer(int64(len(list) + delivered))
}

func LRANGE(args []string, client *storage.Client) string {
	if len(args) != 4 {
		return resp.Error("wrong number of arguments for 'lrange'")
	}
	key := args[1]

	start, err1 := strconv.Atoi(args[2])
	stop, err2 := strconv.Atoi(args[3])
	if err1 != nil || err2 != nil {
		return resp.Error("start or stop is not an integer")
	}

	storage.Mu.RLock()
	defer storage.Mu.RUnlock()

	val, ok := storage.Lists[key]
	if !ok {
		return resp.EmptyArray()
	}

	list, ok := val.([]string)
	if !ok {
		return resp.EmptyArray()
	}

	if start < 0 {
		start = len(list) + start
	}
	if stop < 0 {
		stop = len(list) + stop
	}

	if start < 0 {
		start = 0
	}
	if stop < 0 {
		stop = 0
	}

	if stop >= len(list) {
		stop = len(list) - 1
	}

	if start > stop || start >= len(list) {
		return resp.EmptyArray()
	}

	return resp.Array(list[start : stop+1])
}

func LLEN(args []string, client *storage.Client) string {
	if len(args) < 2 {
		return resp.Error("wrong number of arguments for 'llen'")
	}

	storage.Mu.RLock()
	defer storage.Mu.RUnlock()

	val, ok := storage.Lists[args[1]]
	if !ok {
		return resp.Integer(0)
	}

	list, ok := val.([]string)
	if !ok {
		return resp.Error("wrong type for key")
	}

	return resp.Integer(int64(len(list)))
}

func LPOP(args []string, client *storage.Client) string {
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

	storage.Mu.Lock()
	defer storage.Mu.Unlock()

	val, ok := storage.Lists[args[1]]
	if !ok {
		if count > 1 {
			return resp.EmptyArray()
		} else {
			return resp.NullBulkString()
		}
	}

	list, ok := val.([]string)
	if !ok {
		return resp.Error("wrong type for key")
	}

	if len(list) == 0 {
		delete(storage.Lists, args[1])
		if count > 1 {
			return resp.EmptyArray()
		} else {
			return resp.NullBulkString()
		}
	}

	if count > len(list) {
		count = len(list)
	}

	poppedElements := list[:count]

	// Format response
	var result string
	if count == 1 {
		result = resp.BulkString(poppedElements[0])
	} else {
		result = resp.Array(poppedElements)
	}

	// Trim list
	list = list[count:]
	if len(list) == 0 {
		delete(storage.Lists, args[1])
	} else {
		storage.Lists[args[1]] = list
	}
	return result
}

func BLPOP(args []string, client *storage.Client) string {
	if len(args) < 3 {
		return resp.Error("wrong number of arguments for 'blpop'")
	}

	key := args[1]
	timeoutSec, err := strconv.ParseFloat(args[2], 64)
	if err != nil {
		return resp.Error("timeout is not a float")
	}

	storage.Mu.Lock()

	val, ok := storage.Lists[key]
	if ok {
		list, ok := val.([]string)
		if ok && len(list) > 0 {
			elem := list[0]
			list = list[1:]
			storage.Lists[key] = list
			storage.Mu.Unlock()
			return resp.Array([]string{key, elem})
		}
	}

	// no element available in list, block with timeout
	waitCh := make(chan [2]string, 1)
	storage.Waiters[key] = append(storage.Waiters[key], waitCh)
	storage.Mu.Unlock()

	var timeout <-chan time.Time
	if timeoutSec > 0 {
		timeout = time.After(time.Duration(timeoutSec * float64(time.Second)))
	}

	select {
	case kv := <-waitCh:
		client.Conn.Write([]byte(resp.Array([]string{kv[0], kv[1]})))
	case <-timeout:
		client.Conn.Write([]byte(resp.NullArray()))
	}

	storage.Mu.Lock()
	ws := storage.Waiters[key]
	for i, w := range ws {
		if w == waitCh {
			storage.Waiters[key] = append(ws[:i], ws[i+1:]...)
			break
		}
	}
	if len(storage.Waiters[key]) == 0 {
		delete(storage.Waiters, key)
	}
	storage.Mu.Unlock()
	return "" // Response is sent asynchronously
}
