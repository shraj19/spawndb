// Package deque implements a growable ring-buffer double-ended queue.
//
// All head/tail operations (PushFront, PushBack, PopFront, PopBack) are
// amortized O(1). Elements live in a single contiguous backing slice, giving
// good cache locality; the slice doubles in capacity when full, exactly like
// the built-in append growth strategy.
//
// Design notes (ring buffer):
//   - State is the backing slice `buf`, the logical head index `head`, and the
//     element count `size`. The tail position is derived, never stored:
//     tailWriteIndex = (head + size) % cap.
//   - `size` disambiguates empty (size == 0) from full (size == cap); the
//     head == tail aliasing problem never arises because we track count
//     explicitly rather than leaving a slot empty.
//   - On grow we re-pack elements into logical order starting at index 0 and
//     reset head = 0. A ring buffer cannot keep elements wrapped across the old
//     capacity boundary after the capacity changes, so growth must restore
//     contiguity.
//   - Vacated slots are zeroed on pop so the backing array does not pin
//     references (important for pointer/reference element types such as string).
package deque

// Deque is a growable ring-buffer double-ended queue of T.
// The zero value is an empty, ready-to-use deque.
type Deque[T any] struct {
	buf  []T
	head int // index of the front element (valid only when size > 0)
	size int // number of elements currently stored
}

// New returns an empty deque. An optional initial capacity hint may be given;
// capacities are rounded up to a power of two and to a sensible minimum.
func New[T any](capacityHint ...int) *Deque[T] {
	cap0 := 0
	if len(capacityHint) > 0 && capacityHint[0] > 0 {
		cap0 = capacityHint[0]
	}
	d := &Deque[T]{}
	if cap0 > 0 {
		d.buf = make([]T, roundUpPow2(cap0))
	}
	return d
}

// Len returns the number of elements in the deque.
func (d *Deque[T]) Len() int { return d.size }

// Cap returns the current backing-array capacity.
func (d *Deque[T]) Cap() int { return len(d.buf) }

// PushFront inserts v at the front (head) of the deque. Amortized O(1).
func (d *Deque[T]) PushFront(v T) {
	if d.size == len(d.buf) {
		d.grow()
	}
	// move head back one slot (wrapping), then write.
	d.head = d.dec(d.head)
	d.buf[d.head] = v
	d.size++
}

// PushBack inserts v at the back (tail) of the deque. Amortized O(1).
func (d *Deque[T]) PushBack(v T) {
	if d.size == len(d.buf) {
		d.grow()
	}
	tail := d.index(d.size) // one past the last element
	d.buf[tail] = v
	d.size++
}

// PopFront removes and returns the front element. ok is false if empty.
func (d *Deque[T]) PopFront() (v T, ok bool) {
	if d.size == 0 {
		return v, false
	}
	v = d.buf[d.head]
	var zero T
	d.buf[d.head] = zero // release reference so GC can reclaim it
	d.head = d.inc(d.head)
	d.size--
	return v, true
}

// PopBack removes and returns the back element. ok is false if empty.
func (d *Deque[T]) PopBack() (v T, ok bool) {
	if d.size == 0 {
		return v, false
	}
	tail := d.index(d.size - 1) // last element
	v = d.buf[tail]
	var zero T
	d.buf[tail] = zero // release reference
	d.size--
	return v, true
}

// Front returns the front element without removing it. ok is false if empty.
func (d *Deque[T]) Front() (v T, ok bool) {
	if d.size == 0 {
		return v, false
	}
	return d.buf[d.head], true
}

// Back returns the back element without removing it. ok is false if empty.
func (d *Deque[T]) Back() (v T, ok bool) {
	if d.size == 0 {
		return v, false
	}
	return d.buf[d.index(d.size-1)], true
}

// At returns the element at logical position i (0 == front). It panics if i is
// out of range, matching slice indexing semantics. O(1).
func (d *Deque[T]) At(i int) T {
	if i < 0 || i >= d.size {
		panic("deque: index out of range")
	}
	return d.buf[d.index(i)]
}

// Range appends elements in logical positions [start, stop] (inclusive) to dst
// and returns it. Caller is responsible for clamping start/stop to [0, Len()-1];
// if start > stop or the range is empty, dst is returned unchanged. O(stop-start).
func (d *Deque[T]) Range(dst []T, start, stop int) []T {
	if start < 0 {
		start = 0
	}
	if stop >= d.size {
		stop = d.size - 1
	}
	for i := start; i <= stop; i++ {
		dst = append(dst, d.buf[d.index(i)])
	}
	return dst
}

// --- internal helpers ---

// index maps a logical offset (0 == front) to a physical backing-array index.
//
// Capacity is always a power of two (see New/grow), so the wrap is a bitwise
// AND with (cap-1) rather than a modulo. On the hot path (At/Range/push/pop)
// this replaces an integer division with a single ALU op.
func (d *Deque[T]) index(logical int) int {
	return (d.head + logical) & (len(d.buf) - 1)
}

// inc / dec step a physical index forward / backward with wraparound.
func (d *Deque[T]) inc(i int) int {
	return (i + 1) & (len(d.buf) - 1)
}

func (d *Deque[T]) dec(i int) int {
	return (i - 1) & (len(d.buf) - 1)
}

// grow doubles capacity (minimum 1) and re-packs elements into logical order
// starting at index 0, resetting head to 0.
func (d *Deque[T]) grow() {
	newCap := len(d.buf) * 2
	if newCap == 0 {
		newCap = 1
	}
	newBuf := make([]T, newCap)
	// Copy in logical order. Elements may be wrapped: head..end, then 0..tail.
	if d.size > 0 {
		n := len(d.buf)
		if d.head+d.size <= n {
			// contiguous, no wrap
			copy(newBuf, d.buf[d.head:d.head+d.size])
		} else {
			// wrapped: front chunk [head:n], then back chunk [0:remaining]
			first := copy(newBuf, d.buf[d.head:n])
			copy(newBuf[first:], d.buf[:d.size-first])
		}
	}
	d.buf = newBuf
	d.head = 0
}

// roundUpPow2 rounds n up to the next power of two (n >= 1).
func roundUpPow2(n int) int {
	if n < 1 {
		return 1
	}
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}
