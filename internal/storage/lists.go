package storage

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/shraj19/spawndb/internal/utilities/deque"
)

// listStore implements ListStore interface.
// Waiters for blocking operations live inside the store.
//
// Each list is backed by a ring-buffer deque (internal/utilities/deque) so that
// head/tail pushes and head pops are amortized O(1). The previous
// []string-with-prepend implementation made LPUSH O(n) per call (O(n^2) to build
// a list); see docs/list-store-performance-investigation.md.
type listStore struct {
	data      map[string]*deque.Deque[string]
	waiterReg *waiterRegistry[*waiter] // key -> waiters blocked on that key
	registry  Registry
	ttl       TTLManager
	mu        sync.RWMutex
}

// waiter represents one blocked BLPop call.
//
// Analogy: a customer who put their name on the waitlist at several restaurants
// (keys) and will take whichever table opens first.
//   - keys: the list it registered on, so cleanup touches only those keys
//     instead of scanning every key in the store ("the card in their pocket").
//   - claimed: a one-time "seated" stamp. Exactly one pusher can win the stamp
//     and deliver; everyone else backs off. This removes any chance of
//     double-delivery and the previous "deliver then maybe re-add" race.
type waiter struct {
	ch      chan [2]string
	keys    []string
	claimed atomic.Bool
}

// claim atomically marks the waiter as served. Returns true for the single
// caller that wins; all later callers get false.
func (w *waiter) claim() bool {
	return w.claimed.CompareAndSwap(false, true)
}

// NewListStore creates a new list store with dependencies.
func NewListStore(registry Registry, ttl TTLManager) ListStore {
	return &listStore{
		data:      make(map[string]*deque.Deque[string]),
		waiterReg: newWaiterRegistry[*waiter](),
		registry:  registry,
		ttl:       ttl,
	}
}

// getOrCreate returns the deque for key, creating it if absent. Caller holds lock.
func (l *listStore) getOrCreate(key string) *deque.Deque[string] {
	d := l.data[key]
	if d == nil {
		d = deque.New[string]()
		l.data[key] = d
	}
	return d
}

// checkExpiry performs lazy expiry check. If key is expired, deletes it and returns true.
// Caller must NOT hold lock (this method acquires write lock).
func (l *listStore) checkExpiry(key string) bool {
	if !l.ttl.CheckAndDelete(key) {
		return false
	}
	// Key expired — TTL already cleaned up, now delete data
	l.mu.Lock()
	_, existed := l.data[key]
	if existed {
		delete(l.data, key)
		l.registry.Release(key) // Release while holding lock to prevent race
	}
	l.mu.Unlock()
	return existed
}

type listDelivery struct {
	w   *waiter
	val [2]string
}

func (l *listStore) LPush(key string, values ...string) (int, error) {
	// Lazy expiry check
	l.checkExpiry(key)

	// Register type
	if err := l.registry.Acquire(key, TypeList); err != nil {
		return 0, err
	}

	l.mu.Lock()
	toDeliver, finalLen := l.pushLocked(key, values, true)
	l.mu.Unlock()

	l.deliver(toDeliver)
	return finalLen, nil
}

func (l *listStore) RPush(key string, values ...string) (int, error) {
	// Lazy expiry check
	l.checkExpiry(key)

	// Register type
	if err := l.registry.Acquire(key, TypeList); err != nil {
		return 0, err
	}

	l.mu.Lock()
	toDeliver, finalLen := l.pushLocked(key, values, false)
	l.mu.Unlock()

	l.deliver(toDeliver)
	return finalLen, nil
}

// pushLocked matches leading values to waiting BLPop callers (FIFO), then stores
// the remainder. For LPUSH each remaining value is pushed to the head in
// argument order (so the last arg ends up at the very front, matching Redis);
// for RPUSH each is appended to the tail. Caller must hold the write lock.
//
// Because a waiter is claimed here (under the lock) before we hand it a value,
// the value cannot be lost: if we claimed it, we own the delivery; if we could
// not claim it (already served via another key), we skip it and the value stays
// in the list for the next consumer. No out-of-lock re-add is needed.
func (l *listStore) pushLocked(key string, values []string, front bool) ([]listDelivery, int) {
	var toDeliver []listDelivery
	idx := 0
	for l.waiterReg.Len(key) > 0 && idx < len(values) {
		w, _ := l.waiterReg.PopFront(key)

		// Try to claim this waiter. If another key's push already served it,
		// skip without consuming a value.
		if !w.claim() {
			continue
		}

		// Won the claim: remove it from the other keys it was waiting on.
		l.waiterReg.RemoveFromKeys(w.keys, w, key)

		v := values[idx]
		idx++
		toDeliver = append(toDeliver, listDelivery{w: w, val: [2]string{key, v}})
	}

	var finalLen int
	if idx < len(values) {
		d := l.getOrCreate(key)
		for ; idx < len(values); idx++ {
			if front {
				d.PushFront(values[idx])
			} else {
				d.PushBack(values[idx])
			}
		}
		finalLen = d.Len()
	} else if d := l.data[key]; d != nil {
		finalLen = d.Len()
	}

	return toDeliver, finalLen
}

// deliver sends claimed values to their waiters OUTSIDE the store lock (to avoid
// deadlock). The waiter channel is buffered (size 1) and each waiter is claimed
// exactly once, so the send never blocks and never races.
func (l *listStore) deliver(toDeliver []listDelivery) {
	for _, d := range toDeliver {
		d.w.ch <- d.val
	}
}

func (l *listStore) LRange(key string, start, stop int) ([]string, error) {
	// Lazy expiry check
	if l.checkExpiry(key) {
		return []string{}, nil
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	d, ok := l.data[key]
	if !ok || d.Len() == 0 {
		return []string{}, nil
	}

	length := d.Len()

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

	result := make([]string, 0, stop-start+1)
	result = d.Range(result, start, stop)
	return result, nil
}

func (l *listStore) LLen(key string) (int, error) {
	// Lazy expiry check
	if l.checkExpiry(key) {
		return 0, nil
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	if d := l.data[key]; d != nil {
		return d.Len(), nil
	}
	return 0, nil
}

func (l *listStore) LPop(key string, count int) ([]string, error) {
	// Lazy expiry check
	if l.checkExpiry(key) {
		return []string{}, nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	d, ok := l.data[key]
	if !ok || d.Len() == 0 {
		return []string{}, nil
	}

	if count > d.Len() {
		count = d.Len()
	}

	popped := make([]string, count)
	for i := 0; i < count; i++ {
		v, _ := d.PopFront()
		popped[i] = v
	}

	if d.Len() == 0 {
		delete(l.data, key)
		l.registry.Release(key)
	}

	return popped, nil
}

func (l *listStore) BLPop(keys []string, timeout time.Duration) (key, value string, ok bool) {
	// Lazy expiry check for all keys
	for _, k := range keys {
		l.checkExpiry(k)
	}

	l.mu.Lock()

	// Check all keys for existing data (priority order)
	for _, k := range keys {
		d := l.data[k]
		if d != nil && d.Len() > 0 {
			elem, _ := d.PopFront()
			if d.Len() == 0 {
				delete(l.data, k)
				l.registry.Release(k)
			}
			l.mu.Unlock()
			return k, elem, true
		}
	}

	// No data available — register one waiter on ALL keys. The waiter carries
	// its own key list so cleanup is cheap.
	w := &waiter{
		ch:   make(chan [2]string, 1),
		keys: keys,
	}
	l.waiterReg.Add(keys, w)
	l.mu.Unlock()

	// Wait with timeout
	var timeoutCh <-chan time.Time
	if timeout > 0 {
		timeoutCh = time.After(timeout)
	}

	select {
	case kv := <-w.ch:
		return kv[0], kv[1], true
	case <-timeoutCh:
		// Timed out. Claim ourselves so a concurrent pusher cannot also serve
		// us; if the pusher already claimed us, drain the delivered value and
		// honor it instead of reporting a false timeout.
		if w.claim() {
			l.mu.Lock()
			l.waiterReg.RemoveFromKeys(w.keys, w, "")
			l.mu.Unlock()
			return "", "", false
		}
		kv := <-w.ch // pusher won the claim; a value is (or will be) delivered
		return kv[0], kv[1], true
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

// ForEach iterates over all lists for persistence.
// Uses snapshot approach: briefly locks to get keys, then iterates without holding lock.
// Keys added during iteration may be missed; keys deleted are skipped safely.
func (l *listStore) ForEach(fn func(key string, elements []string, expiry time.Time, hasTTL bool)) {
	// Take a quick snapshot of keys
	l.mu.RLock()
	keys := make([]string, 0, len(l.data))
	for k := range l.data {
		keys = append(keys, k)
	}
	l.mu.RUnlock()

	// Iterate over snapshot (no lock held during callback)
	for _, key := range keys {
		// Skip expired
		if l.ttl.IsExpired(key) {
			continue
		}

		// Get data for this key (brief lock)
		l.mu.RLock()
		d, exists := l.data[key]
		if !exists || d.Len() == 0 {
			l.mu.RUnlock()
			continue
		}
		elements := make([]string, 0, d.Len())
		elements = d.Range(elements, 0, d.Len()-1)
		l.mu.RUnlock()

		expiry, hasTTL := l.ttl.Get(key)
		fn(key, elements, expiry, hasTTL)
	}
}

// Load adds a list during database load.
func (l *listStore) Load(key string, elements []string, expiry time.Time, hasTTL bool) {
	// Register type
	if err := l.registry.Acquire(key, TypeList); err != nil {
		return // Skip if type conflict (shouldn't happen on clean load)
	}

	l.mu.Lock()
	d := deque.New[string]()
	for _, elem := range elements {
		d.PushBack(elem)
	}
	l.data[key] = d
	l.mu.Unlock()

	// Set TTL if applicable
	if hasTTL {
		l.ttl.Set(key, expiry)
	}
}
