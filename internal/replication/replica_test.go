package replication

import (
	"bufio"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shraj19/spawndb/internal/core"
	"github.com/shraj19/spawndb/internal/resp"
	"github.com/shraj19/spawndb/internal/storage"
)

// TestStartReplicaAppliesPropagatedSET tests that replicated SET commands
// are applied to the local DataStore.
//
// Note: This test creates minimal command handlers inline to avoid importing
// the commands package (which would create an import cycle).
func TestStartReplicaAppliesPropagatedSET(t *testing.T) {
	// Reset replication state
	State = &ReplicationState{
		Role:     "master",
		ReplID:   "test-repl-id",
		Offset:   0,
		Replicas: make(map[string]*ReplicaConnection),
	}
	
	// Create DataStore
	db := storage.NewDataStore()
	
	// Create minimal handlers inline (avoids import cycle)
	core.InitCommands(map[string]core.CommandHandler{
		"PING": func(args []string, client *core.Client) string {
			return "+PONG\r\n"
		},
		"SET": func(args []string, client *core.Client) string {
			if len(args) < 3 {
				return "-ERR wrong number of arguments\r\n"
			}
			db.Strings().Set(args[1], args[2])
			return "+OK\r\n"
		},
		"GET": func(args []string, client *core.Client) string {
			if len(args) < 2 {
				return "-ERR wrong number of arguments\r\n"
			}
			val, ok := db.Strings().Get(args[1])
			if !ok {
				return "$-1\r\n"
			}
			return resp.BulkString(val)
		},
		"REPLCONF": func(args []string, client *core.Client) string {
			return "+OK\r\n"
		},
		"PSYNC": func(args []string, client *core.Client) string {
			return ""
		},
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start master listener: %v", err)
	}
	defer listener.Close()

	masterDone := make(chan struct{})
	go func() {
		defer close(masterDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)

		args, err := resp.ParseRESP(reader)
		if err != nil || len(args) == 0 || strings.ToUpper(args[0]) != "PING" {
			return
		}
		_, _ = conn.Write([]byte("+PONG\r\n"))

		args, err = resp.ParseRESP(reader)
		if err != nil || len(args) < 3 || strings.ToUpper(args[0]) != "REPLCONF" {
			return
		}
		_, _ = conn.Write([]byte("+OK\r\n"))

		args, err = resp.ParseRESP(reader)
		if err != nil || len(args) < 3 || strings.ToUpper(args[0]) != "REPLCONF" {
			return
		}
		_, _ = conn.Write([]byte("+OK\r\n"))

		args, err = resp.ParseRESP(reader)
		if err != nil || len(args) < 3 || strings.ToUpper(args[0]) != "PSYNC" {
			return
		}
		_, _ = conn.Write([]byte("+FULLRESYNC test-repl-id 0\r\n$0\r\n"))

		encoded, err := resp.EncodeCommand([]string{"SET", "replicated", "value"})
		if err != nil {
			return
		}
		_, _ = conn.Write(encoded)
		<-time.After(200 * time.Millisecond)
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	go StartReplica("127.0.0.1", strconv.Itoa(port), "6380")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		v, ok := db.Strings().Get("replicated")
		if ok && v == "value" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	v, ok := db.Strings().Get("replicated")
	if !ok || v != "value" {
		t.Fatalf("expected replica to apply replicated SET, got key=%q value=%q present=%v", "replicated", v, ok)
	}

	<-masterDone
}
