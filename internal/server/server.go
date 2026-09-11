package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"github.com/shraj19/flashdb/internal/commands"
	"github.com/shraj19/flashdb/internal/core"
	"github.com/shraj19/flashdb/internal/replication"
	"github.com/shraj19/flashdb/internal/resp"
	"github.com/shraj19/flashdb/internal/storage"
)

// HandleConnection handles one client connection
func HandleConnection(conn net.Conn) {
	defer conn.Close()
	storage.IncrementClients()
	defer storage.DecrementClients()
	// Use Client for per-connection state
	client := &core.Client{Conn: conn}
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

		handler, ok := core.CommandMap[cmdName]
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
		response := handler(args, client)
		if response != "" {
			client.Conn.Write([]byte(response))
		}
	}
}

// StartServer starts the Redis-like server
func StartServer() {
	// Create the DataStore - this is the composition root
	db := storage.NewDataStore()

	// Initialize commands with DataStore-based handlers
	core.InitCommands(map[string]core.CommandHandler{
		// General commands (stateless)
		"PING":     commands.PING,
		"ECHO":     commands.ECHO,
		"INFO":     commands.INFO,

		// String commands (use DataStore)
		"SET":  commands.MakeSET(db),
		"GET":  commands.MakeGET(db),
		"TYPE": commands.MakeTYPE(db),
		"INCR": commands.MakeINCR(db),

		// Transaction commands
		"MULTI":   commands.MULTI,
		"DISCARD": commands.DISCARD,
		"EXEC":    commands.EXEC,

		// List commands (use DataStore)
		"LPUSH":  commands.MakeLPUSH(db),
		"RPUSH":  commands.MakeRPUSH(db),
		"LPOP":   commands.MakeLPOP(db),
		"BLPOP":  commands.MakeBLPOP(db),
		"LRANGE": commands.MakeLRANGE(db),
		"LLEN":   commands.MakeLLEN(db),

		// Stream commands (use DataStore)
		"XADD":   commands.MakeXADD(db),
		"XREAD":  commands.MakeXREAD(db),
		"XRANGE": commands.MakeXRANGE(db),

		// Replication commands
		"REPLCONF": commands.REPLCONF,
		"PSYNC":    commands.PSYNC,
		"WAIT":     commands.WAIT,
	})

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
		go func() {
			replication.State.Mu.Lock()
			replication.State.Role = "slave"
			replication.State.Mu.Unlock()
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
