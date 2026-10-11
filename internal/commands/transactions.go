package commands

import (
	"fmt"
	"strings"
	"sync"

	"github.com/shraj19/spawndb/internal/core"
	"github.com/shraj19/spawndb/internal/resp"
)

// execLock ensures MULTI/EXEC transactions are atomic and isolated.
// Only one EXEC can run at a time, preventing command interleaving.
// Matches Redis single-threaded semantics.
var execLock sync.Mutex

func MULTI(args []string, client *core.Client) string {
	if len(args) != 1 {
		return resp.Error("wrong number of arguments for 'multi'")
	}

	if client.InTransaction {
		return resp.Error("MULTI calls can not be nested")
	}

	client.InTransaction = true
	client.QueuedCommands = [][]string{}
	return resp.OK()
}

func EXEC(args []string, client *core.Client) string {
	if !client.InTransaction {
		return resp.Error("EXEC without MULTI")
	}

	// Defer resetting the transaction state.
	// This ensures it's cleaned up whether the transaction succeeds or fails.
	defer func() {
		client.InTransaction = false
		client.QueuedCommands = nil
	}()

	// Phase 1: Pre-check commands for errors without executing them.
	// Redis aborts the transaction if there are queue-time errors (like syntax errors).
	// While we don't have a separate pre-check function, we can check for obvious errors.
	for _, cmdArgs := range client.QueuedCommands {
		cmdName := strings.ToUpper(cmdArgs[0])
		if cmdName == "DISCARD" || cmdName == "EXEC" || cmdName == "MULTI" {
			return resp.Error("EXEC aborted on invalid command in transaction")
		}
		_, ok := core.CommandMap[cmdName]
		if !ok {
			return resp.Error("EXEC aborted on invalid command in transaction")
		}
	}

	// Phase 2: Execute all commands atomically under global lock.
	// This ensures no other client's commands interleave during transaction.
	execLock.Lock()
	defer execLock.Unlock()

	responses := []string{}
	for _, cmdArgs := range client.QueuedCommands {
		cmdName := strings.ToUpper(cmdArgs[0])
		handler := core.CommandMap[cmdName]
		// We can assume 'ok' is true because of the pre-check loop above.
		response := handler(cmdArgs, client)
		responses = append(responses, response)
	}

	var respBuilder strings.Builder
	respBuilder.WriteString(fmt.Sprintf("*%d\r\n", len(responses)))
	for _, r := range responses {
		respBuilder.WriteString(r)
	}
	return respBuilder.String()
}

func DISCARD(args []string, client *core.Client) string {
	if !client.InTransaction {
		return resp.Error("DISCARD without MULTI")
	}

	// Reset transaction state
	client.InTransaction = false
	client.QueuedCommands = nil

	return resp.OK()
}
