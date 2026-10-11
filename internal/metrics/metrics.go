// Package metrics defines Prometheus metrics for spawndb.
//
// It mirrors the pattern used in the Relay project: metrics are declared as
// package-level vars under a single namespace and registered via Register(reg),
// so production can pass prometheus.DefaultRegisterer and tests can pass a
// fresh prometheus.NewRegistry() for isolation.
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// ObserveCommand records the duration and success/error of a single command
// dispatch. Call it once per executed command from the server dispatch loop.
//
// Usage:
//
//	start := time.Now()
//	resp := handler(args, client)
//	metrics.ObserveCommand(cmdName, start, err)
func ObserveCommand(command string, start time.Time, isError bool) {
	status := "ok"
	if isError {
		status = "error"
	}
	CommandDuration.WithLabelValues(command).Observe(time.Since(start).Seconds())
	CommandsTotal.WithLabelValues(command, status).Inc()
}

// Command layer

var (
	// CommandsTotal counts all executed commands, labeled by command name and
	// status (ok or error).
	CommandsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "spawndb",
			Name:      "commands_total",
			Help:      "Total number of commands processed",
		},
		[]string{"command", "status"},
	)

	// CommandDuration measures per-command processing latency in seconds.
	// Buckets span 0.1ms to ~3s to capture both in-memory fast paths and
	// slower blocking commands.
	CommandDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "spawndb",
			Name:      "command_duration_seconds",
			Help:      "Command processing latency distributions in seconds",
			Buckets:   prometheus.ExponentialBuckets(0.0001, 2, 15), // 0.1ms to ~3s
		},
		[]string{"command"},
	)

	// ErrorsTotal counts error conditions by type (parse, unknown_command,
	// wrongtype, etc.), independent of per-command status for easy alerting.
	ErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "spawndb",
			Name:      "errors_total",
			Help:      "Total errors by type",
		},
		[]string{"type"},
	)
)

// Connection / data gauges

var (
	// ConnectedClients tracks the number of currently connected clients.
	ConnectedClients = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "spawndb",
			Name:      "connected_clients",
			Help:      "Number of currently connected clients",
		},
	)

	// KeysTotal tracks the total number of keys currently stored across all
	// types. Updated periodically by a background sampler.
	KeysTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "spawndb",
			Name:      "keys_total",
			Help:      "Total number of keys currently stored",
		},
	)
)

// Register registers all spawndb metrics with the provided registerer.
//
// In production, pass prometheus.DefaultRegisterer. In tests, pass a fresh
// prometheus.NewRegistry() for isolated metric collection.
func Register(reg prometheus.Registerer) {
	reg.MustRegister(
		CommandsTotal,
		CommandDuration,
		ErrorsTotal,
		ConnectedClients,
		KeysTotal,
	)
}
