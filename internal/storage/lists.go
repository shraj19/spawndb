package storage

import (
	"sync"
	"time"
)

// listStore implements ListStore interface.
// Waiters for blocking operations live inside the store.
type listStore struct {
	data     map[string][]string
	waiters  map[string][]chan [2]string // key -> waiting channels
	registry Registry
	mu       sync.RWMutex
}

// NewListStore creates a new list store with dependencies.
func NewListStore(registry Registry) ListStore {
	return &listStore{
		data:     make(map[string][]string),
		waiters:  make(map[string][]chan [2]string),
		registry: registry,
	}
}

func (l *listStore) LPush(key string, values ...string) (int, error) {
	// Register type
	if err := l.registry.Acquire(key, TypeList); err != nil {
		return 0, err
	}

	l.mu.Lock()

	list := l.data[key]

	// Reverse values for correct LPUSH order (last arg ends up at head)
	reversed := make([]string, len(values))
	i := 0
	lenValues := len(values)
	for i < lenValues {
		reversed[lenValues-i-1] = values[i]
		i++
	}

	// Collect waiters to notify (we'll deliver OUTSIDE the lock)
	type delivery struct {
		ch  chan [2]string
		val [2]string
	}
	var toDeliver []delivery

	// Match values to waiters
	for len(l.waiters[key]) > 0 && len(reversed) > 0 {
		ch := l.waiters[key][0]
		l.waiters[key] = l.waiters[key][1:]

		v := reversed[0]
		reversed = reversed[1:]

		// CRITICAL: Remove this waiter from ALL keys it's registered on
		// This prevents double-delivery when the same waiter is on multiple keys
		l.removeWaiterFromAllKeys(ch)

		toDeliver = append(toDeliver, delivery{ch: ch, val: [2]string{key, v}})
	}

	// Prepend remaining values to list
	if len(reversed) > 0 {
		list = append(reversed, list...)
	}

	l.data[key] = list
	finalLen := len(list)

	l.mu.Unlock()

	// Deliver OUTSIDE the lock to prevent deadlock
	for _, d := range toDeliver {
		// Non-blocking send — if channel is somehow already satisfied, value is lost
		// This shouldn't happen with proper waiter removal, but defensive
		select {
		case d.ch <- d.val:
		default:
			// Waiter was already satisfied (race with another key's push)
			// Re-add the value to the list
			l.mu.Lock()
			l.data[key] = append([]string{d.val[1]}, l.data[key]...)
			finalLen = len(l.data[key])
			l.mu.Unlock()
		}
	}

	// Return list length AFTER push (not including delivered items)
	return finalLen, nil
}

func (l *listStore) RPush(key string, values ...string) (int, error) {
	// Register type
	if err := l.registry.Acquire(key, TypeList); err != nil {
		return 0, err
	}

	l.mu.Lock()

	list := l.data[key]

	// Collect waiters to notify (we'll deliver OUTSIDE the lock)
	type delivery struct {
		ch  chan [2]string
		val [2]string
	}
	var toDeliver []delivery

	// Match values to waiters (FIFO order)
	toAppend := values
	for len(l.waiters[key]) > 0 && len(toAppend) > 0 {
		ch := l.waiters[key][0]
		l.waiters[key] = l.waiters[key][1:]

		v := toAppend[0]
		toAppend = toAppend[1:]

		// CRITICAL: Remove this waiter from ALL keys it's registered on
		l.removeWaiterFromAllKeys(ch)

		toDeliver = append(toDeliver, delivery{ch: ch, val: [2]string{key, v}})
	}

	// Append remaining values to list
	if len(toAppend) > 0 {
		list = append(list, toAppend...)
	}

	l.data[key] = list
	finalLen := len(list)

	l.mu.Unlock()

	// Deliver OUTSIDE the lock to prevent deadlock
	for _, d := range toDeliver {
		select {
		case d.ch <- d.val:
		default:
			// Re-add to end of list if delivery failed
			l.mu.Lock()
			l.data[key] = append(l.data[key], d.val[1])
			finalLen = len(l.data[key])
			l.mu.Unlock()
		}
	}

	// Return list length AFTER push (not including delivered items)
	return finalLen, nil
}

func (l *listStore) LRange(key string, start, stop int) ([]string, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	list, ok := l.data[key]
	if !ok {
		return []string{}, nil
	}

	length := len(list)

	// Handle negative indices
	if start < 0 {
		start = length + start
	}
	if stop < 0 {
		stop = length + stop
	}

	// Clamp to bounds
	if start < 0 {
		start = 0
	}
	if stop < 0 {
		stop = 0
	}
	if stop >= length {
		stop = length - 1
	}

	if start > stop || start >= length {
		return []string{}, nil
	}

	// Return copy to avoid exposing internal slice
	result := make([]string, stop-start+1)
	copy(result, list[start:stop+1])
	return result, nil
}

func (l *listStore) LLen(key string) (int, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return len(l.data[key]), nil
}

func (l *listStore) LPop(key string, count int) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	list, ok := l.data[key]
	if !ok || len(list) == 0 {
		return []string{}, nil
	}

	if count > len(list) {
		count = len(list)
	}

	popped := make([]string, count)
	copy(popped, list[:count])

	// Trim list
	list = list[count:]
	if len(list) == 0 {
		delete(l.data, key)
		l.registry.Release(key)
	} else {
		l.data[key] = list
	}

	return popped, nil
}

func (l *listStore) BLPop(keys []string, timeout time.Duration) (key, value string, ok bool) {
	l.mu.Lock()

	// Check all keys for existing data (priority order)
	for _, k := range keys {
		list := l.data[k]
		if len(list) > 0 {
			elem := list[0]
			list = list[1:]
			if len(list) == 0 {
				delete(l.data, k)
				l.registry.Release(k)
			} else {
				l.data[k] = list
			}
			l.mu.Unlock()
			return k, elem, true
		}
	}

	// No data available — register waiter on ALL keys
	waitCh := make(chan [2]string, 1)
	for _, k := range keys {
		l.waiters[k] = append(l.waiters[k], waitCh)
	}
	l.mu.Unlock()

	// Wait with timeout
	var timeoutCh <-chan time.Time
	if timeout > 0 {
		timeoutCh = time.After(timeout)
	}

	select {
	case kv := <-waitCh:
		// Got data — remove from ALL waiter lists
		l.mu.Lock()
		for _, k := range keys {
			l.removeWaiter(k, waitCh)
		}
		l.mu.Unlock()
		return kv[0], kv[1], true
	case <-timeoutCh:
		// Timeout — remove from ALL waiter lists
		l.mu.Lock()
		for _, k := range keys {
			l.removeWaiter(k, waitCh)
		}
		l.mu.Unlock()
		return "", "", false
	}
}

// removeWaiter removes a specific waiter channel from one key. Caller must hold lock.
func (l *listStore) removeWaiter(key string, ch chan [2]string) {
	waiters := l.waiters[key]
	for i, w := range waiters {
		if w == ch {
			l.waiters[key] = append(waiters[:i], waiters[i+1:]...)
			break
		}
	}
	if len(l.waiters[key]) == 0 {
		delete(l.waiters, key)
	}
}

// removeWaiterFromAllKeys removes a waiter channel from ALL keys it's registered on.
// This prevents double-delivery when the same waiter is registered on multiple keys.
// Caller must hold lock.
func (l *listStore) removeWaiterFromAllKeys(ch chan [2]string) {
	for key := range l.waiters {
		l.removeWaiter(key, ch)
	}
}

func (l *listStore) Delete(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	_, existed := l.data[key]
	delete(l.data, key)

	if existed {
		l.registry.Release(key)
	}

	return existed
}
