package persistence

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/shraj19/spawndb/internal/storage"
)

const (
	header  = "spawndb"
	version = byte(2) // v2: added type markers, lists, streams
)

// Type markers for serialization
const (
	typeString byte = 0x00
	typeList   byte = 0x01
	typeStream byte = 0x02
)

// Save writes the database state to disk at the given path.
// Uses atomic write: writes to temp file, then renames.
// Skips expired keys.
func Save(path string, db storage.DataStore) error {
	dir := filepath.Dir(path)

	file, err := os.CreateTemp(dir, "spawndb_*.tmp")
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

	// Write all entries
	var writeErr error

	db.Strings().ForEach(func(key, value string, expiry time.Time, hasTTL bool) {
		if writeErr != nil {
			return
		}
		writeErr = saveString(file, key, value, expiry, hasTTL)
	})
	if writeErr != nil {
		return writeErr
	}

	db.Lists().ForEach(func(key string, elements []string, expiry time.Time, hasTTL bool) {
		if writeErr != nil {
			return
		}
		writeErr = saveList(file, key, elements, expiry, hasTTL)
	})
	if writeErr != nil {
		return writeErr
	}

	db.Streams().ForEach(func(key string, entries []storage.StreamEntry, expiry time.Time, hasTTL bool) {
		if writeErr != nil {
			return
		}
		writeErr = saveStream(file, key, entries, expiry, hasTTL)
	})
	if writeErr != nil {
		return writeErr
	}

	if err := file.Close(); err != nil {
		return err
	}

	return os.Rename(file.Name(), path)
}

// saveString writes a single string entry to file.
func saveString(file *os.File, key, value string, expiry time.Time, hasTTL bool) error {
	if _, err := file.Write([]byte{typeString}); err != nil {
		return err
	}
	if err := writeString(file, key); err != nil {
		return err
	}
	if err := writeString(file, value); err != nil {
		return err
	}
	return writeExpiry(file, expiry, hasTTL)
}

// saveList writes a single list entry to file.
func saveList(file *os.File, key string, elements []string, expiry time.Time, hasTTL bool) error {
	if _, err := file.Write([]byte{typeList}); err != nil {
		return err
	}
	if err := writeString(file, key); err != nil {
		return err
	}
	if err := binary.Write(file, binary.BigEndian, uint32(len(elements))); err != nil {
		return err
	}
	for _, elem := range elements {
		if err := writeString(file, elem); err != nil {
			return err
		}
	}
	return writeExpiry(file, expiry, hasTTL)
}

// saveStream writes a single stream entry to file.
func saveStream(file *os.File, key string, entries []storage.StreamEntry, expiry time.Time, hasTTL bool) error {
	if _, err := file.Write([]byte{typeStream}); err != nil {
		return err
	}
	if err := writeString(file, key); err != nil {
		return err
	}
	if err := binary.Write(file, binary.BigEndian, uint32(len(entries))); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := writeString(file, entry.ID); err != nil {
			return err
		}
		if err := binary.Write(file, binary.BigEndian, uint32(len(entry.Fields))); err != nil {
			return err
		}
		for k, v := range entry.Fields {
			if err := writeString(file, k); err != nil {
				return err
			}
			if err := writeString(file, v); err != nil {
				return err
			}
		}
	}
	return writeExpiry(file, expiry, hasTTL)
}

// Load reads database state from disk and populates the DataStore.
// Skips keys that have already expired.
func Load(path string, db storage.DataStore) error {
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

	return loadEntries(file, db)
}

// loadEntries reads all typed entries from file.
func loadEntries(file *os.File, db storage.DataStore) error {
	typeBuf := make([]byte, 1)

	for {
		if _, err := io.ReadFull(file, typeBuf); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		var err error
		switch typeBuf[0] {
		case typeString:
			err = loadString(file, db)
		case typeList:
			err = loadList(file, db)
		case typeStream:
			err = loadStream(file, db)
		default:
			return errors.New("unknown type marker")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// loadString reads and loads a single string entry.
func loadString(file *os.File, db storage.DataStore) error {
	key, err := readString(file)
	if err != nil {
		return err
	}

	value, err := readString(file)
	if err != nil {
		return err
	}

	expiry, hasTTL, err := readExpiry(file)
	if err != nil {
		return err
	}

	// Skip expired
	if hasTTL && time.Now().After(expiry) {
		return nil
	}

	db.Strings().Load(key, value, expiry, hasTTL)
	return nil
}

// loadList reads and loads a single list entry.
func loadList(file *os.File, db storage.DataStore) error {
	key, err := readString(file)
	if err != nil {
		return err
	}

	var count uint32
	if err := binary.Read(file, binary.BigEndian, &count); err != nil {
		return err
	}

	elements := make([]string, count)
	for i := uint32(0); i < count; i++ {
		elem, err := readString(file)
		if err != nil {
			return err
		}
		elements[i] = elem
	}

	expiry, hasTTL, err := readExpiry(file)
	if err != nil {
		return err
	}

	// Skip expired
	if hasTTL && time.Now().After(expiry) {
		return nil
	}

	db.Lists().Load(key, elements, expiry, hasTTL)
	return nil
}

// loadStream reads and loads a single stream entry.
func loadStream(file *os.File, db storage.DataStore) error {
	key, err := readString(file)
	if err != nil {
		return err
	}

	var entryCount uint32
	if err := binary.Read(file, binary.BigEndian, &entryCount); err != nil {
		return err
	}

	entries := make([]storage.StreamEntry, entryCount)
	for i := uint32(0); i < entryCount; i++ {
		id, err := readString(file)
		if err != nil {
			return err
		}

		var fieldCount uint32
		if err := binary.Read(file, binary.BigEndian, &fieldCount); err != nil {
			return err
		}

		fields := make(map[string]string, fieldCount)
		for j := uint32(0); j < fieldCount; j++ {
			k, err := readString(file)
			if err != nil {
				return err
			}
			v, err := readString(file)
			if err != nil {
				return err
			}
			fields[k] = v
		}

		entries[i] = storage.StreamEntry{ID: id, Fields: fields}
	}

	expiry, hasTTL, err := readExpiry(file)
	if err != nil {
		return err
	}

	// Skip expired
	if hasTTL && time.Now().After(expiry) {
		return nil
	}

	db.Streams().Load(key, entries, expiry, hasTTL)
	return nil
}

// --- Low-level I/O helpers ---

// writeString writes a length-prefixed string.
func writeString(file *os.File, s string) error {
	if err := binary.Write(file, binary.BigEndian, uint32(len(s))); err != nil {
		return err
	}
	_, err := file.Write([]byte(s))
	return err
}

// readString reads a length-prefixed string.
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

// writeExpiry writes expiry timestamp (0 if no TTL).
func writeExpiry(file *os.File, expiry time.Time, hasTTL bool) error {
	var expiryUnix int64
	if hasTTL {
		expiryUnix = expiry.Unix()
	}
	return binary.Write(file, binary.BigEndian, expiryUnix)
}

// readExpiry reads expiry timestamp and returns (expiry, hasTTL, error).
func readExpiry(file *os.File) (time.Time, bool, error) {
	var expiryUnix int64
	if err := binary.Read(file, binary.BigEndian, &expiryUnix); err != nil {
		return time.Time{}, false, err
	}
	if expiryUnix == 0 {
		return time.Time{}, false, nil
	}
	return time.Unix(expiryUnix, 0), true, nil
}
