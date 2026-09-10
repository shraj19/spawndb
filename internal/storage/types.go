package storage

import (
	"errors"
)

// --- Data Types ---

// DataType identifies the type of value stored at a key.
type DataType uint8

const (
	TypeString DataType = iota
	TypeList
	TypeStream
)

func (t DataType) String() string {
	switch t {
	case TypeString:
		return "string"
	case TypeList:
		return "list"
	case TypeStream:
		return "stream"
	default:
		return "unknown"
	}
}

// --- Errors ---

var (
	ErrWrongType = errors.New("WRONGTYPE Operation against a key holding the wrong kind of value")
	ErrNotFound  = errors.New("key not found")
	ErrNotInt    = errors.New("value is not an integer or out of range")
)

// Note: TTLResult, StreamEntry, StreamWaiter, Client are defined in
// storage.go and database.go. They will be consolidated here after
// migration is complete.
