package storage

import (
	"fmt"
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

// StartMetricsLogger prints metrics every 10 seconds
func StartMetricsLogger() {
	ticker := time.NewTicker(10 * time.Second)
	go func() {
		for range ticker.C {
			clients := GetConnectedClients()
			commands := GetTotalCommands()
			fmt.Printf("[INFO] Connected Clients: %d | Total Commands: %d\n",
				clients, commands)
		}
	}()
}
