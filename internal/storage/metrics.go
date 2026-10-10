package storage

import (
	"log/slog"
	"sync/atomic"
	"time"
)

var (
	// Atomic counters for metrics
	ConnectedClients int64
	TotalCommands    int64
)

// IncrementClients increments the connected clients counter
func IncrementClients() {
	atomic.AddInt64(&ConnectedClients, 1)
}

// DecrementClients decrements the connected clients counter
func DecrementClients() {
	atomic.AddInt64(&ConnectedClients, -1)
}

// IncrementCommands increments the total commands counter
func IncrementCommands() {
	atomic.AddInt64(&TotalCommands, 1)
}

// GetConnectedClients returns the current number of connected clients
func GetConnectedClients() int64 {
	return atomic.LoadInt64(&ConnectedClients)
}

// GetTotalCommands returns the total number of commands executed
func GetTotalCommands() int64 {
	return atomic.LoadInt64(&TotalCommands)
}

// StartMetricsLogger logs metrics as structured JSON every 10 seconds.
// JSON lines on stdout are picked up by the Docker log driver / promtail
// and shipped to Loki.
func StartMetricsLogger() {
	ticker := time.NewTicker(10 * time.Second)
	go func() {
		for range ticker.C {
			clients := GetConnectedClients()
			commands := GetTotalCommands()
			slog.Info("metrics snapshot",
				"connected_clients", clients,
				"total_commands", commands,
			)
		}
	}()
}
