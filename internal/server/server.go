package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"github.com/shraj19/flashdb/internal/commands"
	"github.com/shraj19/flashdb/internal/replication"
	"github.com/shraj19/flashdb/internal/resp"
	"github.com/shraj19/flashdb/internal/storage"
)

// HandleConnection handles one client connection
func HandleConnection(conn net.Conn) {
	defer conn.Close()
	storage.IncrementClients()
	defer storage.DecrementClients()
	// Use storage.Client for per-connection state
	client := &storage.Client{Conn: conn}
	reader := bufio.NewReader(conn)

	for {
		args, err := resp.ParseRESP(reader)
		if err == io.EOF {
			return
		}

		if err != nil {
			fmt.Println("Parse error:", err)
			return
		}

		if len(args) == 0 {
			continue
		}

		storage.IncrementCommands()
		cmdName := strings.ToUpper(args[0])

		handler, ok := storage.CommandMap[cmdName]
		if !ok {
			client.Conn.Write([]byte(resp.Error(fmt.Sprintf("unknown command '%s'", args[0]))))
			continue
		}

		// Check if client is in transaction
		if client.InTransaction && cmdName != "EXEC" && cmdName != "DISCARD" {
			// Commands like MULTI are not allowed inside a transaction
			if cmdName == "MULTI" {
				client.Conn.Write([]byte(resp.Error("MULTI calls can not be nested")))
				continue
			}

			// Queue the valid command
			client.QueuedCommands = append(client.QueuedCommands, args)
			client.Conn.Write([]byte(resp.SimpleString("QUEUED")))
			continue
		}

		// Execute the command normally
		// The handler now returns a response string, which we write back.
		// For commands that manage their own connection writing (like blocking commands),
		// they can return an empty string.
		response := handler(args, client)
		if response != "" {
			client.Conn.Write([]byte(response))
		}
	}
}

// StartServer starts the Redis-like server
func StartServer() {
	// Start any background tasks like key expiry
	storage.InitCommands(map[string]storage.CommandHandler{
		"PING":     commands.PING,
		"ECHO":     commands.ECHO,
		"INFO":     commands.INFO,
		"SET":      commands.SETvalue,
		"GET":      commands.GETvalue,
		"TYPE":     commands.TYPE,
		"INCR":     commands.INCR,
		"MULTI":    commands.MULTI,
		"DISCARD":  commands.DISCARD,
		"EXEC":     commands.EXEC,
		"LPUSH":    commands.LPUSH,
		"RPUSH":    commands.RPUSH,
		"LPOP":     commands.LPOP,
		"BLPOP":    commands.BLPOP,
		"LRANGE":   commands.LRANGE,
		"LLEN":     commands.LLEN,
		"XADD":     commands.XADD,
		"XREAD":    commands.XREAD,
		"XRANGE":   commands.XRANGE,
		"REPLCONF": commands.REPLCONF,
		"PSYNC":    commands.PSYNC,
		"WAIT":     commands.WAIT,
	})
	storage.StartActiveExpiry()

	port := "6379"
	replicaHost := ""
	replicaPort := ""
	for i := 1; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--port":
			if i+1 < len(os.Args) {
				port = os.Args[i+1]
				i++
			}
		case "--replicaof":
			if i+2 < len(os.Args) {
				replicaHost = os.Args[i+1]
				replicaPort = os.Args[i+2]
				i += 2
			}
		}
	}
	if replicaHost != "" && replicaPort != "" {
		// Start replication in a separate goroutine so that it doesn't block the main server loop
		go func() {
			storage.Replication.Mu.Lock()
			storage.Replication.Role = "slave"
			storage.Replication.Mu.Unlock()
		}()
		go replication.StartReplica(replicaHost, replicaPort, port)
	}
	storage.StartMetricsLogger()

	addr := "0.0.0.0:" + port


	listener, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Printf("Failed to bind to port %s: %v\n", port, err)
		return
	}
	defer listener.Close()

	fmt.Printf("Server listening on %s\n", addr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("Error accepting connection:", err)
			continue
		}

		go HandleConnection(conn)
	}
}
