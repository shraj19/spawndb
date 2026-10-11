package commands

import (
	"testing"

	"github.com/shraj19/spawndb/internal/core"
)

func TestMULTIEXECQueueAndRunCommands(t *testing.T) {
	client := &core.Client{}

	response := MULTI([]string{"MULTI"}, client)
	if response != "+OK\r\n" {
		t.Fatalf("expected MULTI response +OK, got %q", response)
	}

	response = EXEC([]string{"EXEC"}, client)
	if response != "*0\r\n" {
		t.Fatalf("expected empty EXEC result for empty transaction, got %q", response)
	}
}

func TestDISCARDResetsTransactionState(t *testing.T) {
	client := &core.Client{}

	MULTI([]string{"MULTI"}, client)
	response := DISCARD([]string{"DISCARD"}, client)
	if response != "+OK\r\n" {
		t.Fatalf("expected DISCARD response +OK, got %q", response)
	}

	if client.InTransaction {
		t.Fatalf("expected transaction state to be cleared")
	}
}

// TestEXEC_Atomicity verifies that EXEC runs all commands atomically.
// Without execLock, other clients' commands could interleave.
func TestEXEC_Atomicity(t *testing.T) {
	// Setup: create DataStore and command handlers
	db := newTestDataStore()
	setCmd := MakeSET(db)
	getCmd := MakeGET(db)
	incrCmd := MakeINCR(db)

	// Register commands in global map for EXEC to find
	core.InitCommands(map[string]core.CommandHandler{
		"SET":  setCmd,
		"GET":  getCmd,
		"INCR": incrCmd,
	})

	// Set initial value
	setCmd([]string{"SET", "counter", "0"}, &core.Client{})

	const numTransactions = 50
	done := make(chan bool, numTransactions*2)

	// Each transaction: INCR counter, INCR counter (adds 2)
	// Plus concurrent single INCRs (adds 1 each)
	// Total expected: 50*2 + 50 = 150

	for i := 0; i < numTransactions; i++ {
		// Transaction client
		go func() {
			client := &core.Client{}
			MULTI([]string{"MULTI"}, client)
			client.QueuedCommands = append(client.QueuedCommands, []string{"INCR", "counter"})
			client.QueuedCommands = append(client.QueuedCommands, []string{"INCR", "counter"})
			EXEC([]string{"EXEC"}, client)
			done <- true
		}()

		// Concurrent non-transaction INCR
		go func() {
			incrCmd([]string{"INCR", "counter"}, &core.Client{})
			done <- true
		}()
	}

	// Wait for all
	for i := 0; i < numTransactions*2; i++ {
		<-done
	}

	// Final value should be: 50 transactions * 2 INCRs + 50 single INCRs = 150
	result := getCmd([]string{"GET", "counter"}, &core.Client{})

	// Extract value from bulk string response "$3\r\n150\r\n"
	expected := "$3\r\n150\r\n"
	if result != expected {
		t.Errorf("expected counter=%s, got %s (atomicity violation?)", expected, result)
	}
}
