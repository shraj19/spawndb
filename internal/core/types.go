// Package core contains shared types used across multiple packages
// to avoid circular imports.
package core

import (
	"net"
	"strings"
)

// Client represents a connected client with transaction state.
type Client struct {
	Conn           net.Conn
	InTransaction  bool
	QueuedCommands [][]string
}

// CommandHandler is the function signature for a Redis command.
type CommandHandler func(args []string, client *Client) string

// CommandMap holds registered command handlers.
var CommandMap map[string]CommandHandler

// InitCommands initializes the command map.
func InitCommands(commands map[string]CommandHandler) {
	CommandMap = make(map[string]CommandHandler)
	for name, handler := range commands {
		CommandMap[strings.ToUpper(name)] = handler
	}
}
