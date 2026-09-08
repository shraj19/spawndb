package commands

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shraj19/flashdb/internal/resp"
	"github.com/shraj19/flashdb/internal/storage"
)

func SETvalue(args []string, client *storage.Client) string {
	if len(args) < 3 {
		return "-ERR wrong number of arguments for 'set'\r\n"
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
				return "-ERR invalid expire time\r\n"
			}
			ttl = time.Duration(seconds) * time.Second
			hasTTL = true
		case "PX":
			ms, err := strconv.Atoi(args[4])
			if err != nil {
				return "-ERR invalid expire time\r\n"
			}
			ttl = time.Duration(ms) * time.Millisecond
			hasTTL = true
		default:
			return "-ERR syntax error\r\n"
		}
	}

	// Store the value
	if hasTTL {
		storage.DB.SetWithTTL(key, value, ttl)
	} else {
		storage.DB.Set(key, value)
	}

	// Replicate to replicas
	if encoded, err := resp.EncodeCommand(args); err == nil {
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
	return "+OK\r\n"
}

func GETvalue(args []string, client *storage.Client) string {
	if len(args) < 2 {
		return "-ERR wrong number of arguments for 'get'\r\n"
	}

	key := args[1]
	value, ok := storage.DB.Get(key)

	if !ok {
		return "$-1\r\n"
	}
	return fmt.Sprintf("$%d\r\n%s\r\n", len(value), value)
}

func TYPE(args []string, client *storage.Client) string {
	if len(args) != 2 {
		return "-ERR wrong number of arguments for 'type'\r\n"
	}

	key := args[1]

	// Check string type
	if storage.DB.Exists(key) {
		return "+string\r\n"
	}

	// Check stream type (still using old global for now)
	storage.Mu.RLock()
	_, isStream := storage.Streams[key]
	storage.Mu.RUnlock()
	if isStream {
		return "+stream\r\n"
	}

	// Check list type
	storage.Mu.RLock()
	_, isList := storage.Lists[key]
	storage.Mu.RUnlock()
	if isList {
		return "+list\r\n"
	}

	return "+none\r\n"
}

func INCR(args []string, client *storage.Client) string {
	if len(args) < 2 {
		return "-ERR wrong number of arguments for 'incr'\r\n"
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
		return "-ERR value is not an integer or out of range\r\n"
	}

	num++
	storage.DB.SetUnlocked(key, strconv.FormatInt(num, 10))

	// Replicate
	if encoded, err := resp.EncodeCommand(args); err == nil {
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

	return fmt.Sprintf(":%d\r\n", num)
}
