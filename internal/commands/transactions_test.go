package commands

import (
	"testing"

	"github.com/shraj19/flashdb/internal/storage"
)

func TestMULTIEXECQueueAndRunCommands(t *testing.T) {
	client := &storage.Client{}

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
	client := &storage.Client{}

	MULTI([]string{"MULTI"}, client)
	response := DISCARD([]string{"DISCARD"}, client)
	if response != "+OK\r\n" {
		t.Fatalf("expected DISCARD response +OK, got %q", response)
	}

	if client.InTransaction {
		t.Fatalf("expected transaction state to be cleared")
	}
}
