package persistence

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/shraj19/flashdb/internal/storage"
)

const (
	header  = "FLASHDB"
	version = byte(1)
)

// Save writes the current database state to disk at the given path.
// Uses atomic write: writes to temp file, then renames.
// Skips expired keys.
func Save(path string) error {
	dir := filepath.Dir(path)

	file, err := os.CreateTemp(dir, "flashdb_*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())

	// Write header
	if _, err := file.Write([]byte(header)); err != nil {
		return err
	}
	if _, err := file.Write([]byte{version}); err != nil {
		return err
	}

	// Write entries using Database iterator
	var writeErr error
	storage.DB.ForEachString(func(key, value string, expiry time.Time, hasTTL bool) {
		if writeErr != nil {
			return
		}

		if err := writeString(file, key); err != nil {
			writeErr = err
			return
		}
		if err := writeString(file, value); err != nil {
			writeErr = err
			return
		}

		// Write expiry (0 if no TTL)
		var expiryUnix int64
		if hasTTL {
			expiryUnix = expiry.Unix()
		}
		if err := binary.Write(file, binary.BigEndian, expiryUnix); err != nil {
			writeErr = err
			return
		}
	})

	if writeErr != nil {
		return writeErr
	}

	if err := file.Close(); err != nil {
		return err
	}

	return os.Rename(file.Name(), path)
}

// Load reads database state from disk and populates storage.
// Skips keys that have already expired.
func Load(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	// Validate header
	buf := make([]byte, 8)
	if _, err := io.ReadFull(file, buf); err != nil {
		return err
	}
	if string(buf[:7]) != header {
		return errors.New("invalid file format")
	}
	if buf[7] != version {
		return errors.New("unsupported version")
	}

	// Read entries
	for {
		key, err := readString(file)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		value, err := readString(file)
		if err != nil {
			return err
		}

		var expiryUnix int64
		if err := binary.Read(file, binary.BigEndian, &expiryUnix); err != nil {
			return err
		}

		// Skip expired keys
		if expiryUnix != 0 && time.Now().After(time.Unix(expiryUnix, 0)) {
			continue
		}

		// Load into database
		hasTTL := expiryUnix != 0
		var expiry time.Time
		if hasTTL {
			expiry = time.Unix(expiryUnix, 0)
		}
		storage.DB.LoadString(key, value, expiry, hasTTL)
	}

	return nil
}

// writeString writes a length-prefixed string
func writeString(file *os.File, s string) error {
	if err := binary.Write(file, binary.BigEndian, uint32(len(s))); err != nil {
		return err
	}
	_, err := file.Write([]byte(s))
	return err
}

// readString reads a length-prefixed string
func readString(file *os.File) (string, error) {
	var length uint32
	if err := binary.Read(file, binary.BigEndian, &length); err != nil {
		return "", err
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(file, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}
