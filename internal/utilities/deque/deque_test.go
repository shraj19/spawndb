package deque

import (
	"testing"
)

func TestPushBackPopFrontFIFO(t *testing.T) {
	d := New[int]()
	for i := range 1000 {
		d.PushBack(i)
	}
	if d.Len() != 1000 {
		t.Fatalf("len = %d, want 1000", d.Len())
	}
	for i := range 1000 {
		v, ok := d.PopFront()
		if !ok || v != i {
			t.Fatalf("PopFront #%d = (%d,%v), want (%d,true)", i, v, ok, i)
		}
	}
	if _, ok := d.PopFront(); ok {
		t.Fatal("expected empty after draining")
	}
}

func TestPushFrontOrder(t *testing.T) {
	d := New[int]()
	for i := range 100 {
		d.PushFront(i) // 99,98,...,0 at the front
	}
	// front should be the last pushed (99), draining front gives 99..0
	for want := 99; want >= 0; want-- {
		v, ok := d.PopFront()
		if !ok || v != want {
			t.Fatalf("got (%d,%v), want %d", v, ok, want)
		}
	}
}

// Forces wrap-around: fill, pop some from front, push more so the tail laps
// past the end of the backing array, then verify logical order is intact.
func TestWrapAround(t *testing.T) {
	d := New[int](4) // capacity starts at 4
	// fill 0,1,2,3
	for i := range 4 {
		d.PushBack(i)
	}
	// pop two from front -> head advances to index 2
	d.PopFront() // removes 0
	d.PopFront() // removes 1
	// push 4,5 -> these wrap to physical slots 0,1 (no grow, size now 4 == cap)
	d.PushBack(4)
	d.PushBack(5)
	if d.Cap() != 4 {
		t.Fatalf("cap = %d, expected no grow (4)", d.Cap())
	}
	// logical order must be 2,3,4,5
	want := []int{2, 3, 4, 5}
	for _, w := range want {
		v, ok := d.PopFront()
		if !ok || v != w {
			t.Fatalf("wrap: got (%d,%v), want %d", v, ok, w)
		}
	}
}

// Grow while wrapped: ensures re-packing preserves logical order.
func TestGrowWhileWrapped(t *testing.T) {
	d := New[int](4)
	for i := range 4 {
		d.PushBack(i)
	}
	d.PopFront() // 0 gone, head=1
	d.PopFront() // 1 gone, head=2
	d.PushBack(4) // slot 0
	d.PushBack(5) // slot 1  -> now wrapped: logical 2,3,4,5
	// now full (size 4 cap 4) and wrapped; next push must grow + unwrap
	d.PushBack(6)
	if d.Cap() <= 4 {
		t.Fatalf("expected grow, cap = %d", d.Cap())
	}
	want := []int{2, 3, 4, 5, 6}
	for _, w := range want {
		v, ok := d.PopFront()
		if !ok || v != w {
			t.Fatalf("grow-wrapped: got (%d,%v), want %d", v, ok, w)
		}
	}
}

func TestMixedEnds(t *testing.T) {
	d := New[int]()
	// build sequence via both ends: PushFront 2,1,0 ; PushBack 3,4,5
	d.PushFront(2)
	d.PushFront(1)
	d.PushFront(0)
	d.PushBack(3)
	d.PushBack(4)
	d.PushBack(5)
	// logical: 0,1,2,3,4,5
	for i := range 6 {
		if got := d.At(i); got != i {
			t.Fatalf("At(%d) = %d, want %d", i, got, i)
		}
	}
	// pop from back: 5,4,3
	for w := 5; w >= 3; w-- {
		v, ok := d.PopBack()
		if !ok || v != w {
			t.Fatalf("PopBack got (%d,%v), want %d", v, ok, w)
		}
	}
	// remaining 0,1,2 from front
	for w := 0; w <= 2; w++ {
		v, _ := d.PopFront()
		if v != w {
			t.Fatalf("remaining got %d, want %d", v, w)
		}
	}
}

func TestRange(t *testing.T) {
	d := New[int]()
	for i := range 10 {
		d.PushBack(i)
	}
	got := d.Range(nil, 3, 6)
	want := []int{3, 4, 5, 6}
	if len(got) != len(want) {
		t.Fatalf("range len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("range[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

// Verify pop clears the slot (reference types not pinned). We can't observe GC
// directly, but we can assert the backing slot was zeroed.
func TestPopClearsSlot(t *testing.T) {
	d := New[string](4)
	d.PushBack("hello")
	d.PushBack("world")
	headBefore := d.head
	v, ok := d.PopFront()
	if !ok || v != "hello" {
		t.Fatalf("got (%q,%v)", v, ok)
	}
	if d.buf[headBefore] != "" {
		t.Fatalf("vacated slot not cleared: %q", d.buf[headBefore])
	}
	tailIdx := d.index(d.size - 1) // "world" position
	_, _ = d.PopBack()
	if d.buf[tailIdx] != "" {
		t.Fatalf("vacated back slot not cleared: %q", d.buf[tailIdx])
	}
}

func TestManyGrows(t *testing.T) {
	d := New[int]()
	const N = 100000
	for i := range N {
		d.PushBack(i)
	}
	for i := range N {
		v, ok := d.PopFront()
		if !ok || v != i {
			t.Fatalf("after many grows: got (%d,%v), want %d", v, ok, i)
		}
	}
}
