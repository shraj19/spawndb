package resp

import (
	"bufio"
	"strings"
	"testing"
)

func TestParseRESPInlineCommand(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("PING\r\n"))

	args, err := ParseRESP(reader)
	if err != nil {
		t.Fatalf("ParseRESP returned error: %v", err)
	}

	if len(args) != 1 || args[0] != "PING" {
		t.Fatalf("expected parsed inline command to be [PING], got %v", args)
	}
}

func TestParseRESPArrayCommand(t *testing.T) {
	payload := "*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n"
	reader := bufio.NewReader(strings.NewReader(payload))

	args, err := ParseRESP(reader)
	if err != nil {
		t.Fatalf("ParseRESP returned error: %v", err)
	}

	expected := []string{"SET", "key", "value"}
	if len(args) != len(expected) {
		t.Fatalf("expected %d arguments, got %d", len(expected), len(args))
	}

	for i, want := range expected {
		if args[i] != want {
			t.Fatalf("expected argument %d to be %q, got %q", i, want, args[i])
		}
	}
}



// --- TryParse tests (non-blocking parsing for pipelining) ---

func TestTryParse_CompleteCommand(t *testing.T) {
	buf := []byte("*3\r\n$3\r\nSET\r\n$3\r\nfoo\r\n$3\r\nbar\r\n")

	args, consumed, complete := TryParse(buf)
	if !complete {
		t.Fatal("expected complete command")
	}
	if consumed != len(buf) {
		t.Errorf("expected consumed=%d, got %d", len(buf), consumed)
	}
	if len(args) != 3 || args[0] != "SET" || args[1] != "foo" || args[2] != "bar" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestTryParse_IncompleteCommand(t *testing.T) {
	// Partial command - missing last bulk string
	buf := []byte("*3\r\n$3\r\nSET\r\n$3\r\nfoo\r\n$3\r\nba")

	args, consumed, complete := TryParse(buf)
	if complete {
		t.Fatal("expected incomplete")
	}
	if args != nil || consumed != 0 {
		t.Errorf("expected nil/0, got %v/%d", args, consumed)
	}
}

func TestTryParse_MultipleCommands(t *testing.T) {
	buf := []byte("*2\r\n$3\r\nGET\r\n$3\r\nfoo\r\n*2\r\n$3\r\nGET\r\n$3\r\nbar\r\n")

	// Parse first command
	args1, consumed1, ok := TryParse(buf)
	if !ok || len(args1) != 2 || args1[0] != "GET" || args1[1] != "foo" {
		t.Fatalf("first command failed: args=%v, ok=%v", args1, ok)
	}

	// Parse second command from remaining buffer
	buf = buf[consumed1:]
	args2, consumed2, ok := TryParse(buf)
	if !ok || len(args2) != 2 || args2[0] != "GET" || args2[1] != "bar" {
		t.Fatalf("second command failed: args=%v, ok=%v", args2, ok)
	}

	// Nothing left
	buf = buf[consumed2:]
	if len(buf) != 0 {
		t.Errorf("expected empty buffer, got %d bytes", len(buf))
	}
}

func TestTryParse_InlineCommand(t *testing.T) {
	buf := []byte("PING\r\n")

	args, consumed, complete := TryParse(buf)
	if !complete {
		t.Fatal("expected complete")
	}
	if consumed != len(buf) {
		t.Errorf("expected consumed=%d, got %d", len(buf), consumed)
	}
	if len(args) != 1 || args[0] != "PING" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestTryParse_EmptyBuffer(t *testing.T) {
	args, consumed, complete := TryParse([]byte{})
	if complete || args != nil || consumed != 0 {
		t.Errorf("expected incomplete for empty buffer")
	}
}

func TestTryParse_PartialCRLF(t *testing.T) {
	// Has \r but not \n
	buf := []byte("*2\r")

	args, consumed, complete := TryParse(buf)
	if complete {
		t.Fatal("expected incomplete")
	}
	if args != nil || consumed != 0 {
		t.Errorf("expected nil/0, got %v/%d", args, consumed)
	}
}

func TestTryParse_EmptyArray(t *testing.T) {
	buf := []byte("*0\r\n")

	args, consumed, complete := TryParse(buf)
	if !complete {
		t.Fatal("expected complete")
	}
	if len(args) != 0 {
		t.Errorf("expected empty args, got %v", args)
	}
	if consumed != 4 {
		t.Errorf("expected consumed=4, got %d", consumed)
	}
}
