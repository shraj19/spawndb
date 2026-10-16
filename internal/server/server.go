package server

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof" // Register pprof handlers on default mux
	"os"
	"strings"
	"time"

	"bufio"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/shraj19/spawndb/internal/commands"
	"github.com/shraj19/spawndb/internal/core"
	"github.com/shraj19/spawndb/internal/metrics"
	"github.com/shraj19/spawndb/internal/replication"
	"github.com/shraj19/spawndb/internal/resp"
	"github.com/shraj19/spawndb/internal/storage"
)

// logger is a structured JSON logger. JSON lines on stdout are picked up by
// the Docker log driver / promtail and shipped to Loki.
var logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
	Level: slog.LevelInfo,
}))

// HandleConnection handles one client connection with pipelining support.
//
// Pipelining: Client can send multiple commands without waiting for responses.
// We read all available data, parse complete commands, execute them, buffer
// responses, and flush after processing each batch.
//
// Implementation note: We use an accumulation buffer and re-parse from start
// on each iteration. This is simple and correct. Re-parsing is CPU-cheap
// (nanoseconds) compared to network I/O (milliseconds). If profiling shows
// parsing as bottleneck, consider tracking parse position instead.
func HandleConnection(conn net.Conn) {
	defer conn.Close()
	storage.IncrementClients()
	metrics.ConnectedClients.Inc()
	defer func() {
		storage.DecrementClients()
		metrics.ConnectedClients.Dec()
	}()

	// Use Client for per-connection state
	client := &core.Client{Conn: conn}

	// Pipelining buffers
	readBuf := make([]byte, 4096)
	var accumulated []byte
	writer := bufio.NewWriter(conn)

	for {
		// Read whatever is available from TCP
		n, err := conn.Read(readBuf)
		if err != nil {
			if err != io.EOF {
				metrics.ErrorsTotal.WithLabelValues("read").Inc()
				logger.Error("read error", "err", err.Error(), "remote", conn.RemoteAddr().String())
			}
			return
		}

		accumulated = append(accumulated, readBuf[:n]...)

		// Parse and execute all complete commands
		for {
			args, consumed, complete := resp.TryParse(accumulated)
			if !complete {
				break // Need more data
			}

			accumulated = accumulated[consumed:]

			if len(args) == 0 {
				continue
			}

			response := executeCommand(args, client)
			if response != "" {
				writer.WriteString(response)
			}
		}

		// Flush all buffered responses after processing batch
		if err := writer.Flush(); err != nil {
			metrics.ErrorsTotal.WithLabelValues("write").Inc()
			logger.Error("write error", "err", err.Error(), "remote", conn.RemoteAddr().String())
			return
		}
	}
}

// executeCommand runs a single command and returns the RESP response.
func executeCommand(args []string, client *core.Client) string {
	storage.IncrementCommands()
	cmdName := strings.ToUpper(args[0])

	handler, ok := core.CommandMap[cmdName]
	if !ok {
		metrics.ErrorsTotal.WithLabelValues("unknown_command").Inc()
		metrics.CommandsTotal.WithLabelValues("unknown", "error").Inc()
		return resp.Error("unknown command '" + args[0] + "'")
	}

	// Check if client is in transaction
	if client.InTransaction && cmdName != "EXEC" && cmdName != "DISCARD" {
		if cmdName == "MULTI" {
			return resp.Error("MULTI calls can not be nested")
		}

		// Queue the command
		client.QueuedCommands = append(client.QueuedCommands, args)
		return resp.SimpleString("QUEUED")
	}

	// Execute the command, timing it for metrics
	start := time.Now()
	response := handler(args, client)
	isError := strings.HasPrefix(response, "-")
	metrics.ObserveCommand(cmdName, start, isError)

	return response
}

// startMetricsEndpoint serves Prometheus metrics and pprof on the given address.
// It runs in its own goroutine on a port separate from the RESP server so
// scraping never competes with client traffic on the data port.
//
// Endpoints:
//   - /metrics  - Prometheus metrics
//   - /health   - Health check
//   - /debug/pprof/* - Go profiling (CPU, heap, mutex, block, goroutine)
func startMetricsEndpoint(addr string) {
	// Use default mux which has pprof handlers registered via import
	mux := http.DefaultServeMux
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("metrics endpoint listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("metrics endpoint failed", "err", err.Error())
		}
	}()
}

// startKeysSampler periodically samples the total key count into the gauge.
func startKeysSampler(db storage.DataStore) {
	ticker := time.NewTicker(5 * time.Second)
	go func() {
		for range ticker.C {
			metrics.KeysTotal.Set(float64(db.KeyCount()))
		}
	}()
}

// StartServer starts the Redis-like server.
func StartServer() {
	// Register Prometheus metrics against the default registry.
	metrics.Register(prometheus.DefaultRegisterer)

	// Create the DataStore - this is the composition root
	db := storage.NewDataStore()

	// Initialize commands with DataStore-based handlers
	core.InitCommands(map[string]core.CommandHandler{
		// General commands (stateless)
		"PING": commands.PING,
		"ECHO": commands.ECHO,
		"INFO": commands.INFO,

		// Key commands (work on any type)
		"DEL":    commands.MakeDEL(db),
		"TYPE":   commands.MakeTYPE(db),
		"EXPIRE": commands.MakeEXPIRE(db),
		"TTL":    commands.MakeTTL(db),

		// String commands
		"SET":  commands.MakeSET(db),
		"GET":  commands.MakeGET(db),
		"INCR": commands.MakeINCR(db),

		// Transaction commands
		"MULTI":   commands.MULTI,
		"DISCARD": commands.DISCARD,
		"EXEC":    commands.EXEC,

		// List commands
		"LPUSH":  commands.MakeLPUSH(db),
		"RPUSH":  commands.MakeRPUSH(db),
		"LPOP":   commands.MakeLPOP(db),
		"BLPOP":  commands.MakeBLPOP(db),
		"LRANGE": commands.MakeLRANGE(db),
		"LLEN":   commands.MakeLLEN(db),

		// Stream commands
		"XADD":   commands.MakeXADD(db),
		"XREAD":  commands.MakeXREAD(db),
		"XRANGE": commands.MakeXRANGE(db),

		// Replication commands
		"REPLCONF": commands.REPLCONF,
		"PSYNC":    commands.PSYNC,
		"WAIT":     commands.WAIT,
	})

	port := "6379"
	metricsPort := "9121"
	replicaHost := ""
	replicaPort := ""
	for i := 1; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--port":
			if i+1 < len(os.Args) {
				port = os.Args[i+1]
				i++
			}
		case "--metrics-port":
			if i+1 < len(os.Args) {
				metricsPort = os.Args[i+1]
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
	startKeysSampler(db)
	startMetricsEndpoint("0.0.0.0:" + metricsPort)

	addr := "0.0.0.0:" + port

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Error("failed to bind", "port", port, "err", err.Error())
		return
	}
	defer listener.Close()

	logger.Info("server listening", "addr", addr, "metrics_addr", "0.0.0.0:"+metricsPort)

	for {
		conn, err := listener.Accept()
		if err != nil {
			logger.Error("error accepting connection", "err", err.Error())
			continue
		}

		go HandleConnection(conn)
	}
}
