package replication

import (
	"bufio"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shraj19/flashdb/internal/commands"
	"github.com/shraj19/flashdb/internal/resp"
	"github.com/shraj19/flashdb/internal/storage"
)

func TestStartReplicaAppliesPropagatedSET(t *testing.T) {
	storage.DB.Clear()
	storage.Store = make(map[string]string)
	storage.Expiry = make(map[string]time.Time)
	storage.Lists = make(map[string]any)
	storage.Streams = make(map[string]*storage.Stream)
	storage.StreamWaiters = make(map[string][]*storage.StreamWaiter)
	storage.Waiters = make(map[string][]chan [2]string)
	storage.Mu = sync.RWMutex{}
	storage.Replication = &storage.ReplicationState{
		Role:     "master",
		ReplID:   "test-repl-id",
		Offset:   0,
		Replicas: make(map[string]*storage.ReplicaConnection),
	}
	storage.InitCommands(map[string]storage.CommandHandler{
		"PING":     commands.PING,
		"SET":      commands.SETvalue,
		"GET":      commands.GETvalue,
		"REPLCONF": commands.REPLCONF,
		"PSYNC":    commands.PSYNC,
		"WAIT":     commands.WAIT,
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
		v, ok := storage.DB.Get("replicated")
		if ok && v == "value" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	v, ok := storage.DB.Get("replicated")
	if !ok || v != "value" {
		t.Fatalf("expected replica to apply replicated SET, got key=%q value=%q present=%v", "replicated", v, ok)
	}

	<-masterDone
}
