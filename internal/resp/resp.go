package resp

import (
	"bufio"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
)

func ParseRESP(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, fmt.Errorf("empty command")
	}

	if strings.HasPrefix(line, "*") {
		return parseArray(r, line)
	}

	return strings.Fields(line), nil
}

func parseArray(r *bufio.Reader, firstLine string) ([]string, error) {
	count, err := strconv.Atoi(firstLine[1:])
	if err != nil {
		return nil, fmt.Errorf("invalid array count: %v", err)
	}
	if count < 0 {
		return nil, fmt.Errorf("negative array count not allowed")
	}
	if count == 0 {
		return []string{}, nil
	}

	args := make([]string, 0, count)
	for i := 0; i < count; i++ {
		arg, err := parseBulkString(r)
		if err != nil {
			return nil, fmt.Errorf("failed to parse element %d: %v", i, err)
		}
		args = append(args, arg)
	}
	return args, nil
}

func parseBulkString(r *bufio.Reader) (string, error) {
	lenLine, err := r.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read bulk string length: %v", err)
	}
	lenLine = strings.TrimSpace(lenLine)
	if !strings.HasPrefix(lenLine, "$") {
		return "", fmt.Errorf("expected bulk string marker '$', got: %s", lenLine)
	}

	length, err := strconv.Atoi(lenLine[1:])
	if err != nil {
		return "", fmt.Errorf("invalid bulk string length: %v", err)
	}
	if length == -1 {
		return "", nil
	}
	if length < 0 {
		return "", fmt.Errorf("invalid bulk string length: %d", length)
	}

	data := make([]byte, length+2)
	n, err := io.ReadFull(r, data)
	if err != nil {
		return "", fmt.Errorf("failed to read bulk string data (expected %d, got %d): %v", length+2, n, err)
	}
	if data[length] != '\r' || data[length+1] != '\n' {
		return "", fmt.Errorf("bulk string missing CRLF terminator")
	}
	return string(data[:length]), nil
}

func EncodeCommand(args []string) ([]byte, error) {
	elems := make([]any, len(args))
	for i, s := range args {
		elems[i] = s
	}
	return Encode(elems)
}

func Encode(v any) ([]byte, error) {
	var b strings.Builder
	if err := encodeValue(&b, v); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

func encodeValue(b *strings.Builder, v any) error {
	switch x := v.(type) {
	case nil:
		b.WriteString("$-1\r\n")
		return nil
	case bool:
		if x {
			b.WriteString("#t\r\n")
		} else {
			b.WriteString("#f\r\n")
		}
		return nil
	case string:
		return encodeBulkString(b, x)
	case int:
		return encodeInteger(b, int64(x))
	case int8:
		return encodeInteger(b, int64(x))
	case int16:
		return encodeInteger(b, int64(x))
	case int32:
		return encodeInteger(b, int64(x))
	case int64:
		return encodeInteger(b, x)
	case uint:
		return encodeInteger(b, int64(x))
	case uint8:
		return encodeInteger(b, int64(x))
	case uint16:
		return encodeInteger(b, int64(x))
	case uint32:
		return encodeInteger(b, int64(x))
	case uint64:
		if x > ^uint64(0)>>1 {
			return fmt.Errorf("uint64 out of int64 range")
		}
		return encodeInteger(b, int64(x))
	case []any:
		return encodeArray(b, x)
	default:
		rv := reflect.ValueOf(v)
		if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
			elems := make([]any, rv.Len())
			for i := 0; i < rv.Len(); i++ {
				elems[i] = rv.Index(i).Interface()
			}
			return encodeArray(b, elems)
		}
		return fmt.Errorf("unsupported type %T", v)
	}
}

func encodeBulkString(b *strings.Builder, s string) error {
	b.WriteString("$")
	b.WriteString(strconv.Itoa(len(s)))
	b.WriteString("\r\n")
	b.WriteString(s)
	b.WriteString("\r\n")
	return nil
}

func encodeInteger(b *strings.Builder, i int64) error {
	b.WriteString(":")
	b.WriteString(strconv.FormatInt(i, 10))
	b.WriteString("\r\n")
	return nil
}

func encodeArray(b *strings.Builder, arr []any) error {
	b.WriteString("*")
	b.WriteString(strconv.Itoa(len(arr)))
	b.WriteString("\r\n")
	for _, e := range arr {
		if err := encodeValue(b, e); err != nil {
			return err
		}
	}
	return nil
}

// --- Response helpers for command handlers ---

// SimpleString returns a RESP simple string (e.g., +OK\r\n)
func SimpleString(s string) string {
	return "+" + s + "\r\n"
}

// OK returns +OK\r\n
func OK() string {
	return "+OK\r\n"
}

// PONG returns +PONG\r\n
func PONG() string {
	return "+PONG\r\n"
}

// Error returns a RESP error (e.g., -ERR message\r\n)
func Error(msg string) string {
	return "-ERR " + msg + "\r\n"
}

// BulkString returns a RESP bulk string
func BulkString(s string) string {
	return "$" + strconv.Itoa(len(s)) + "\r\n" + s + "\r\n"
}

// NullBulkString returns RESP null bulk string ($-1\r\n)
func NullBulkString() string {
	return "$-1\r\n"
}

// Integer returns a RESP integer
func Integer(n int64) string {
	return ":" + strconv.FormatInt(n, 10) + "\r\n"
}

// NullArray returns RESP null array (*-1\r\n)
func NullArray() string {
	return "*-1\r\n"
}

// EmptyArray returns RESP empty array (*0\r\n)
func EmptyArray() string {
	return "*0\r\n"
}

// Array encodes a slice as RESP array and returns as string
func Array(items []string) string {
	var b strings.Builder
	// Pre-calculate approximate size to avoid reallocations
	// Each item: $len\r\n + data + \r\n (roughly len + 10)
	b.Grow(len(items)*20 + 10)
	b.WriteString("*")
	b.WriteString(strconv.Itoa(len(items)))
	b.WriteString("\r\n")
	for _, item := range items {
		b.WriteString("$")
		b.WriteString(strconv.Itoa(len(item)))
		b.WriteString("\r\n")
		b.WriteString(item)
		b.WriteString("\r\n")
	}
	return b.String()
}


// --- Non-blocking parsing for pipelining ---

// TryParse attempts to parse a complete RESP command from buf.
// Returns (args, bytesConsumed, true) if complete command found.
// Returns (nil, 0, false) if incomplete (need more data).
//
// Note: This re-parses from the start each time. We accept this because:
// - Parsing is CPU-cheap (nanoseconds) vs network I/O (milliseconds)
// - TCP fragmentation mid-command is rare in practice
// - Optimizing with position tracking adds complexity for minimal gain
// If profiling shows parsing as bottleneck, consider tracking parse position.
func TryParse(buf []byte) ([]string, int, bool) {
	if len(buf) == 0 {
		return nil, 0, false
	}

	// Find first line ending
	pos := findCRLF(buf, 0)
	if pos == -1 {
		return nil, 0, false
	}

	firstLine := string(buf[:pos])

	// Handle array format (*N\r\n)
	if len(firstLine) > 0 && firstLine[0] == '*' {
		count, err := strconv.Atoi(firstLine[1:])
		if err != nil || count < 0 {
			return nil, 0, false
		}
		if count == 0 {
			return []string{}, pos + 2, true
		}

		args := make([]string, 0, count)
		offset := pos + 2 // Skip past *N\r\n

		for range count {
			arg, consumed, ok := tryParseBulkString(buf, offset)
			if !ok {
				return nil, 0, false
			}
			args = append(args, arg)
			offset += consumed
		}

		return args, offset, true
	}

	// Handle inline command (space-separated)
	return strings.Fields(firstLine), pos + 2, true
}

// tryParseBulkString attempts to parse a bulk string starting at offset.
// Returns (string, bytesConsumed, true) if complete.
func tryParseBulkString(buf []byte, offset int) (string, int, bool) {
	if offset >= len(buf) {
		return "", 0, false
	}

	// Find length line
	pos := findCRLF(buf, offset)
	if pos == -1 {
		return "", 0, false
	}

	lenLine := string(buf[offset:pos])
	if len(lenLine) == 0 || lenLine[0] != '$' {
		return "", 0, false
	}

	length, err := strconv.Atoi(lenLine[1:])
	if err != nil {
		return "", 0, false
	}

	// Null bulk string
	if length == -1 {
		return "", pos + 2 - offset, true
	}

	if length < 0 {
		return "", 0, false
	}

	// Check if we have enough data: $len\r\n + data + \r\n
	dataStart := pos + 2
	dataEnd := dataStart + length
	if dataEnd+2 > len(buf) {
		return "", 0, false // Need more data
	}

	// Verify CRLF after data
	if buf[dataEnd] != '\r' || buf[dataEnd+1] != '\n' {
		return "", 0, false
	}

	return string(buf[dataStart:dataEnd]), dataEnd + 2 - offset, true
}

// findCRLF finds the position of \r\n starting from offset.
// Returns -1 if not found.
func findCRLF(buf []byte, offset int) int {
	for i := offset; i < len(buf)-1; i++ {
		if buf[i] == '\r' && buf[i+1] == '\n' {
			return i
		}
	}
	return -1
}
