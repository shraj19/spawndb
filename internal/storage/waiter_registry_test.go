package storage

import "testing"

type testWaiter struct{ id int }

func TestWaiterRegistry_AddPeekPop(t *testing.T) {
	r := newWaiterRegistry[*testWaiter]()
	a, b := &testWaiter{1}, &testWaiter{2}
	r.Add([]string{"k"}, a)
	r.Add([]string{"k"}, b)

	if r.Len("k") != 2 {
		t.Fatalf("len = %d, want 2", r.Len("k"))
	}
	if w, ok := r.Peek("k"); !ok || w != a {
		t.Fatalf("peek = %v,%v want a", w, ok)
	}
	if w, ok := r.PopFront("k"); !ok || w != a {
		t.Fatalf("pop1 = %v,%v want a", w, ok)
	}
	if w, ok := r.PopFront("k"); !ok || w != b {
		t.Fatalf("pop2 = %v,%v want b", w, ok)
	}
	if _, ok := r.PopFront("k"); ok {
		t.Fatal("expected empty")
	}
	if r.Len("k") != 0 {
		t.Fatalf("len after drain = %d", r.Len("k"))
	}
}

func TestWaiterRegistry_MultiKeyRemove(t *testing.T) {
	r := newWaiterRegistry[*testWaiter]()
	w := &testWaiter{1}
	other := &testWaiter{2}
	keys := []string{"a", "b", "c"}
	r.Add(keys, w)
	r.Add([]string{"b"}, other) // b has two waiters

	// remove w from all its keys, skipping "a" (pretend caller handled it)
	r.RemoveFromKeys(keys, w, "a")

	// "a" still has w (skipped)
	if w2, ok := r.Peek("a"); !ok || w2 != w {
		t.Fatalf("a should still have w")
	}
	// "b" should now have only `other`
	if r.Len("b") != 1 {
		t.Fatalf("b len = %d, want 1", r.Len("b"))
	}
	if w2, _ := r.Peek("b"); w2 != other {
		t.Fatalf("b should have other")
	}
	// "c" should be empty and deleted
	if r.Len("c") != 0 {
		t.Fatalf("c len = %d, want 0", r.Len("c"))
	}
}

func TestWaiterRegistry_RangeSnapshotAllowsMutation(t *testing.T) {
	r := newWaiterRegistry[*testWaiter]()
	a, b, c := &testWaiter{1}, &testWaiter{2}, &testWaiter{3}
	r.Add([]string{"k"}, a)
	r.Add([]string{"k"}, b)
	r.Add([]string{"k"}, c)

	var seen []int
	// mutate during iteration: remove each as we see it
	r.Range("k", func(w *testWaiter) {
		seen = append(seen, w.id)
		r.RemoveFromKey("k", w)
	})

	if len(seen) != 3 || seen[0] != 1 || seen[1] != 2 || seen[2] != 3 {
		t.Fatalf("range order wrong: %v", seen)
	}
	if r.Len("k") != 0 {
		t.Fatalf("expected all removed, len = %d", r.Len("k"))
	}
}

func TestWaiterRegistry_RemoveAbsentIsSafe(t *testing.T) {
	r := newWaiterRegistry[*testWaiter]()
	w := &testWaiter{1}
	r.RemoveFromKey("missing", w) // must not panic
	r.RemoveFromKeys([]string{"x", "y"}, w, "")
	if r.Len("x") != 0 {
		t.Fatal("unexpected content")
	}
}
