package storage

// waiterRegistry is generic, semantics-free bookkeeping for blocking commands.
//
// It answers only one question: "which waiters are registered on which keys?"
// It deliberately knows nothing about HOW a waiter is served — whether it
// receives a value (BLPop: work-queue hand-off) or just a wakeup signal
// (XReadBlock: pub-sub recheck). Each store keeps its own matching and delivery
// logic and uses this type purely to track and clean up registrations.
//
// This lets any future blocking command (BRPOP, BLMOVE, WAIT, ...) reuse the
// same registration/cleanup plumbing without duplicating the map handling or
// re-introducing the old "scan every key in the store" cleanup cost.
//
// W must be comparable so a specific waiter can be found and removed; use a
// pointer type (e.g. *waiter, *StreamWaiter).
//
// waiterRegistry is NOT internally synchronized. The owning store's lock must
// be held for all calls, exactly as the raw map was before.
type waiterRegistry[W comparable] struct {
	byKey map[string][]W
}

// newWaiterRegistry returns an empty registry ready for use.
func newWaiterRegistry[W comparable]() *waiterRegistry[W] {
	return &waiterRegistry[W]{byKey: make(map[string][]W)}
}

// Add registers w on every key in keys.
func (r *waiterRegistry[W]) Add(keys []string, w W) {
	for _, k := range keys {
		r.byKey[k] = append(r.byKey[k], w)
	}
}

// Len returns the number of waiters currently registered on key.
func (r *waiterRegistry[W]) Len(key string) int {
	return len(r.byKey[key])
}

// Peek returns the first (oldest, FIFO) waiter on key and whether one exists.
// It does not remove the waiter.
func (r *waiterRegistry[W]) Peek(key string) (W, bool) {
	ws := r.byKey[key]
	if len(ws) == 0 {
		var zero W
		return zero, false
	}
	return ws[0], true
}

// PopFront removes and returns the first (oldest) waiter on key.
func (r *waiterRegistry[W]) PopFront(key string) (W, bool) {
	ws := r.byKey[key]
	if len(ws) == 0 {
		var zero W
		return zero, false
	}
	w := ws[0]
	r.byKey[key] = ws[1:]
	if len(r.byKey[key]) == 0 {
		delete(r.byKey, key)
	}
	return w, true
}

// RemoveFromKey removes a specific waiter from one key. Safe if absent.
func (r *waiterRegistry[W]) RemoveFromKey(key string, target W) {
	ws := r.byKey[key]
	for i, w := range ws {
		if w == target {
			r.byKey[key] = append(ws[:i], ws[i+1:]...)
			break
		}
	}
	if len(r.byKey[key]) == 0 {
		delete(r.byKey, key)
	}
}

// RemoveFromKeys removes a waiter from every key in keys, optionally skipping
// one key the caller has already handled (pass "" to skip nothing).
//
// Cost is O(len(keys)) — it touches only the keys this waiter registered on,
// never every key in the store. This is the fix for the old whole-map scan.
func (r *waiterRegistry[W]) RemoveFromKeys(keys []string, target W, skip string) {
	for _, k := range keys {
		if k == skip {
			continue
		}
		r.RemoveFromKey(k, target)
	}
}

// Range iterates the waiters currently on key, in FIFO order, calling fn for
// each. It operates on a snapshot so fn may safely mutate the registry (e.g.
// remove waiters) during iteration. Used by notify-style (pub-sub) stores that
// must examine every waiter on a key.
func (r *waiterRegistry[W]) Range(key string, fn func(w W)) {
	ws := r.byKey[key]
	if len(ws) == 0 {
		return
	}
	snapshot := make([]W, len(ws))
	copy(snapshot, ws)
	for _, w := range snapshot {
		fn(w)
	}
}
