package server

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/shraj19/spawndb/internal/commands"
	"github.com/shraj19/spawndb/internal/core"
	"github.com/shraj19/spawndb/internal/storage"
)

// setupTestServer starts a test server on a random port and returns the listener.
// Caller must close the listener when done.
func setupTestServer(t *testing.T) (net.Listener, storage.DataStore) {
	t.Helper()

	db := storage.NewDataStore()

	// Initialize commands for testing
	core.InitCommands(map[string]core.CommandHandler{
		"PING":    commands.PING,
		"ECHO":    commands.ECHO,
		"SET":     commands.MakeSET(db),
		"GET":     commands.MakeGET(db),
		"INCR":    commands.MakeINCR(db),
		"DEL":     commands.MakeDEL(db),
		"LPUSH":   commands.MakeLPUSH(db),
		"LRANGE":  commands.MakeLRANGE(db),
		"MULTI":   commands.MULTI,
		"EXEC":    commands.EXEC,
		"DISCARD": commands.DISCARD,
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return // Listener closed
			}
			go HandleConnection(conn)
		}
	}()

	return listener, db
}

// sendRESP sends a RESP array command
func sendRESP(args ...string) string {
	var sb strings.Builder
	sb.WriteString("*")
	sb.WriteString(intToStr(len(args)))
	sb.WriteString("\r\n")
	for _, arg := range args {
		sb.WriteString("$")
		sb.WriteString(intToStr(len(arg)))
		sb.WriteString("\r\n")
		sb.WriteString(arg)
		sb.WriteString("\r\n")
	}
	return sb.String()
}

// intToStr converts an integer to string without using strconv
func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + intToStr(-n)
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

// readLines reads n lines from the connection
func readLines(r *bufio.Reader, n int) ([]string, error) {
	lines := make([]string, 0, n)
	for i := 0; i < n; i++ {
		line, err := r.ReadString('\n')
		if err != nil {
			return lines, err
		}
		lines = append(lines, strings.TrimRight(line, "\r\n"))
	}
	return lines, nil
}

// ============================================================================
// Pipelining Tests
// ============================================================================

func TestPipelining_MultipleCommands(t *testing.T) {
	listener, _ := setupTestServer(t)
	defer listener.Close()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Send 3 commands in one write (pipelining)
	pipelined := sendRESP("PING") + sendRESP("PING") + sendRESP("PING")
	_, err = conn.Write([]byte(pipelined))
	if err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	// Read 3 responses
	reader := bufio.NewReader(conn)
	for i := 0; i < 3; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("failed to read response %d: %v", i+1, err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line != "+PONG" {
			t.Errorf("response %d: got %q, want +PONG", i+1, line)
		}
	}
}

func TestPipelining_MixedCommands(t *testing.T) {
	listener, _ := setupTestServer(t)
	defer listener.Close()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Pipeline: SET, GET, INCR, GET
	pipelined := sendRESP("SET", "counter", "10") +
		sendRESP("GET", "counter") +
		sendRESP("INCR", "counter") +
		sendRESP("GET", "counter")

	_, err = conn.Write([]byte(pipelined))
	if err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	reader := bufio.NewReader(conn)

	// Response 1: SET -> +OK
	line, _ := reader.ReadString('\n')
	if strings.TrimRight(line, "\r\n") != "+OK" {
		t.Errorf("SET response: got %q, want +OK", line)
	}

	// Response 2: GET -> $2\r\n10\r\n
	line, _ = reader.ReadString('\n')
	if strings.TrimRight(line, "\r\n") != "$2" {
		t.Errorf("GET length: got %q, want $2", line)
	}
	line, _ = reader.ReadString('\n')
	if strings.TrimRight(line, "\r\n") != "10" {
		t.Errorf("GET value: got %q, want 10", line)
	}

	// Response 3: INCR -> :11
	line, _ = reader.ReadString('\n')
	if strings.TrimRight(line, "\r\n") != ":11" {
		t.Errorf("INCR response: got %q, want :11", line)
	}

	// Response 4: GET -> $2\r\n11\r\n
	line, _ = reader.ReadString('\n')
	if strings.TrimRight(line, "\r\n") != "$2" {
		t.Errorf("GET2 length: got %q, want $2", line)
	}
	line, _ = reader.ReadString('\n')
	if strings.TrimRight(line, "\r\n") != "11" {
		t.Errorf("GET2 value: got %q, want 11", line)
	}
}

func TestPipelining_LargeVolume(t *testing.T) {
	listener, _ := setupTestServer(t)
	defer listener.Close()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(10 * time.Second))

	// Send 100 PINGs in one write
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString(sendRESP("PING"))
	}

	_, err = conn.Write([]byte(sb.String()))
	if err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	// Read 100 responses
	reader := bufio.NewReader(conn)
	for i := 0; i < 100; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("failed to read response %d: %v", i+1, err)
		}
		if strings.TrimRight(line, "\r\n") != "+PONG" {
			t.Errorf("response %d: got %q, want +PONG", i+1, line)
		}
	}
}

func TestPipelining_LongKeyNames(t *testing.T) {
	listener, _ := setupTestServer(t)
	defer listener.Close()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Test with a long key name (100+ chars)
	longKey := "this_is_a_very_long_key_name_that_exceeds_typical_lengths_and_tests_our_resp_encoding_function_properly"
	longValue := "this_is_also_a_fairly_long_value_to_make_sure_encoding_handles_multi_digit_lengths_correctly"

	pipelined := sendRESP("SET", longKey, longValue) +
		sendRESP("GET", longKey)

	_, err = conn.Write([]byte(pipelined))
	if err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	reader := bufio.NewReader(conn)

	// SET -> +OK
	line, _ := reader.ReadString('\n')
	if strings.TrimRight(line, "\r\n") != "+OK" {
		t.Errorf("SET: got %q, want +OK", line)
	}

	// GET -> $<len>\r\n<value>\r\n
	line, _ = reader.ReadString('\n')
	expectedLen := "$" + intToStr(len(longValue))
	if strings.TrimRight(line, "\r\n") != expectedLen {
		t.Errorf("GET len: got %q, want %s", line, expectedLen)
	}

	line, _ = reader.ReadString('\n')
	if strings.TrimRight(line, "\r\n") != longValue {
		t.Errorf("GET val: got %q, want %s", line, longValue)
	}
}

func TestPipelining_FragmentedInput(t *testing.T) {
	listener, _ := setupTestServer(t)
	defer listener.Close()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Send PING command in fragments
	cmd := sendRESP("PING") // "*1\r\n$4\r\nPING\r\n"

	// Send first half
	mid := len(cmd) / 2
	conn.Write([]byte(cmd[:mid]))
	time.Sleep(10 * time.Millisecond)

	// Send second half
	conn.Write([]byte(cmd[mid:]))

	// Should still get response
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}
	if strings.TrimRight(line, "\r\n") != "+PONG" {
		t.Errorf("got %q, want +PONG", line)
	}
}

// ============================================================================
// Transaction Tests
// ============================================================================

func TestTransaction_Basic(t *testing.T) {
	listener, _ := setupTestServer(t)
	defer listener.Close()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)

	// MULTI
	conn.Write([]byte(sendRESP("MULTI")))
	line, _ := reader.ReadString('\n')
	if strings.TrimRight(line, "\r\n") != "+OK" {
		t.Errorf("MULTI: got %q, want +OK", line)
	}

	// Queue SET
	conn.Write([]byte(sendRESP("SET", "txkey", "txvalue")))
	line, _ = reader.ReadString('\n')
	if strings.TrimRight(line, "\r\n") != "+QUEUED" {
		t.Errorf("SET in tx: got %q, want +QUEUED", line)
	}

	// Queue GET
	conn.Write([]byte(sendRESP("GET", "txkey")))
	line, _ = reader.ReadString('\n')
	if strings.TrimRight(line, "\r\n") != "+QUEUED" {
		t.Errorf("GET in tx: got %q, want +QUEUED", line)
	}

	// EXEC
	conn.Write([]byte(sendRESP("EXEC")))

	// Response: array of 2 elements
	line, _ = reader.ReadString('\n')
	if strings.TrimRight(line, "\r\n") != "*2" {
		t.Errorf("EXEC array: got %q, want *2", line)
	}

	// First result: +OK (from SET)
	line, _ = reader.ReadString('\n')
	if strings.TrimRight(line, "\r\n") != "+OK" {
		t.Errorf("EXEC[0]: got %q, want +OK", line)
	}

	// Second result: $7\r\ntxvalue\r\n (from GET)
	line, _ = reader.ReadString('\n')
	if strings.TrimRight(line, "\r\n") != "$7" {
		t.Errorf("EXEC[1] len: got %q, want $7", line)
	}
	line, _ = reader.ReadString('\n')
	if strings.TrimRight(line, "\r\n") != "txvalue" {
		t.Errorf("EXEC[1] val: got %q, want txvalue", line)
	}
}

func TestTransaction_Discard(t *testing.T) {
	listener, _ := setupTestServer(t)
	defer listener.Close()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)

	// Send all as pipeline to avoid timing issues
	pipelined := sendRESP("MULTI") +
		sendRESP("SET", "dkey", "val") +
		sendRESP("DISCARD") +
		sendRESP("GET", "dkey")

	_, err = conn.Write([]byte(pipelined))
	if err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	// MULTI -> +OK
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read MULTI response: %v", err)
	}
	if strings.TrimRight(line, "\r\n") != "+OK" {
		t.Errorf("MULTI: got %q, want +OK", line)
	}

	// SET -> +QUEUED
	line, err = reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read SET response: %v", err)
	}
	if strings.TrimRight(line, "\r\n") != "+QUEUED" {
		t.Errorf("SET: got %q, want +QUEUED", line)
	}

	// DISCARD -> +OK
	line, err = reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read DISCARD response: %v", err)
	}
	if strings.TrimRight(line, "\r\n") != "+OK" {
		t.Errorf("DISCARD: got %q, want +OK", line)
	}

	// GET -> $-1 (nil, key doesn't exist)
	line, err = reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read GET response: %v", err)
	}
	if strings.TrimRight(line, "\r\n") != "$-1" {
		t.Errorf("GET after discard: got %q, want $-1 (nil)", line)
	}
}

func TestTransaction_NestedMultiError(t *testing.T) {
	listener, _ := setupTestServer(t)
	defer listener.Close()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)

	// MULTI
	conn.Write([]byte(sendRESP("MULTI")))
	reader.ReadString('\n') // +OK

	// Nested MULTI should error
	conn.Write([]byte(sendRESP("MULTI")))
	line, _ := reader.ReadString('\n')
	if !strings.HasPrefix(line, "-") {
		t.Errorf("nested MULTI: got %q, want error", line)
	}
}

func TestTransaction_Pipelined(t *testing.T) {
	listener, _ := setupTestServer(t)
	defer listener.Close()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Send entire transaction pipelined
	pipelined := sendRESP("MULTI") +
		sendRESP("SET", "pipekey", "pipevalue") +
		sendRESP("GET", "pipekey") +
		sendRESP("EXEC")

	conn.Write([]byte(pipelined))

	reader := bufio.NewReader(conn)

	// MULTI -> +OK
	line, _ := reader.ReadString('\n')
	if strings.TrimRight(line, "\r\n") != "+OK" {
		t.Errorf("MULTI: got %q", line)
	}

	// SET -> +QUEUED
	line, _ = reader.ReadString('\n')
	if strings.TrimRight(line, "\r\n") != "+QUEUED" {
		t.Errorf("SET: got %q", line)
	}

	// GET -> +QUEUED
	line, _ = reader.ReadString('\n')
	if strings.TrimRight(line, "\r\n") != "+QUEUED" {
		t.Errorf("GET: got %q", line)
	}

	// EXEC -> *2
	line, _ = reader.ReadString('\n')
	if strings.TrimRight(line, "\r\n") != "*2" {
		t.Errorf("EXEC: got %q, want *2", line)
	}

	// +OK
	line, _ = reader.ReadString('\n')
	if strings.TrimRight(line, "\r\n") != "+OK" {
		t.Errorf("EXEC[0]: got %q", line)
	}

	// $9\r\npipevalue
	line, _ = reader.ReadString('\n')
	if strings.TrimRight(line, "\r\n") != "$9" {
		t.Errorf("EXEC[1] len: got %q", line)
	}
	line, _ = reader.ReadString('\n')
	if strings.TrimRight(line, "\r\n") != "pipevalue" {
		t.Errorf("EXEC[1] val: got %q", line)
	}
}
