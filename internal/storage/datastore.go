package storage

// dataStore implements DataStore interface.
// This is the composition root — assembles all stores with shared dependencies.
type dataStore struct {
	strings  StringStore
	lists    ListStore
	streams  StreamStore
	registry Registry
	ttl      TTLManager
}

// NewDataStore creates a fully-wired DataStore.
// This is the single composition root for storage.
func NewDataStore() DataStore {
	ttl := NewTTLManager()
	registry := NewRegistry(ttl)

	return &dataStore{
		strings:  NewStringStore(registry, ttl),
		lists:    NewListStore(registry),
		streams:  NewStreamStore(registry),
		registry: registry,
		ttl:      ttl,
	}
}

func (d *dataStore) Strings() StringStore {
	return d.strings
}

func (d *dataStore) Lists() ListStore {
	return d.lists
}

func (d *dataStore) Streams() StreamStore {
	return d.streams
}

func (d *dataStore) Registry() Registry {
	return d.registry
}

// Delete removes a key regardless of type.
func (d *dataStore) Delete(key string) bool {
	typ, exists := d.registry.Type(key)
	if !exists {
		return false
	}

	switch typ {
	case TypeString:
		return d.strings.Delete(key)
	case TypeList:
		return d.lists.Delete(key)
	case TypeStream:
		return d.streams.Delete(key)
	default:
		return false
	}
}

// Type returns the type of a key.
func (d *dataStore) Type(key string) (DataType, bool) {
	return d.registry.Type(key)
}

// Exists returns true if key exists (any type).
func (d *dataStore) Exists(key string) bool {
	_, exists := d.registry.Type(key)
	return exists
}
