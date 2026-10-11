# List Store Performance Investigation: O(n²) LPush/LPop

Status: REMEDY IMPLEMENTED (steps 1 and 2 done); verified
Area: `internal/storage/lists.go`, `internal/utilities/deque/`
Date: 2026-10-11

## Summary

The list store is roughly 80× slower than Redis on list-heavy workloads. The
root cause is a single structural mistake: `[]string` is used as a double-ended
queue, but `LPush` prepends with `append([]string{v}, list...)`, which copies the
entire list on every call. Building a list of n elements is therefore O(n²)
instead of O(n). Redis uses a quicklist (linked list of ziplists) where head/tail
insertion is O(1).

This is not a case of many small inefficiencies — it is one wrong data-structure
choice that turns every list build into quadratic work.

## Evidence (measured on go1.25.0, windows/amd64)

Benchmarks added in `internal/storage/lpush_bench_test.go`.

### 1. Real `listStore.LPush` scaling (`TestLPushScaling`)

Build one list of size n via repeated LPush:

| n      | build time   | ratio vs previous (n doubled) |
|--------|--------------|-------------------------------|
| 5000   | 43.9 ms      | —                             |
| 10000  | 218.7 ms     | 4.98×                         |
| 20000  | 1.242 s      | 5.68×                         |
| 40000  | 6.306 s      | 5.08×                         |
| 80000  | 39.99 s      | 6.34×                         |

Interpretation: for amortized O(1) LPush the total build is O(n), so doubling n
should ~double the time (ratio ≈ 2.0). The observed ratio of ~5–6× per doubling
is the O(n²) signature (ideal quadratic is 4×; the surplus is GC/allocation
pressure from repeatedly copying a growing slice). Building a single 80k-element
list takes ~40 seconds.

### 2. Data-structure isolation (`TestPrependVsDeque`)

Slice head-prepend vs amortized tail-append, plain `[]string`:

| n      | slice-prepend | deque-append | slowdown  |
|--------|---------------|--------------|-----------|
| 10000  | 732.9 ms      | 512.1 µs     | 1431×     |
| 50000  | 16.27 s       | 8.31 ms      | 1959×     |
| 100000 | 1m12.19 s     | 12.50 ms     | 5777×     |

The slowdown grows with n (1431× → 5777×), confirming O(n²) vs O(n). This
isolates the cost to the data structure itself, independent of the store.

## Bottlenecks, worst first

### 1. O(n) prepend on every LPush — dominant

`lists.go`, `LPush`:

```go
list = append(reversed, list...)   // copies the entire existing list every call
```

Head insertion must be O(1). The `[]string` with head-prepend is the wrong
structure for a value that receives LPUSH/LPOP. This accounts for the bulk of the
80× gap.

### 2. LPop re-slicing compounds the same root cause

`lists.go`, `LPop`:

```go
list = list[count:]   // never reclaims the head of the backing array
```

Interacts badly with the prepend path; same root cause, same fix (deque).

### 3. removeWaiterFromAllKeys scans the entire waiters map

`lists.go`, `removeWaiterFromAllKeys`:

```go
func (l *listStore) removeWaiterFromAllKeys(ch chan [2]string) {
    for key := range l.waiters {        // iterates ALL keys in the store
        l.removeWaiter(key, ch)
    }
}
```

Called once per delivered value inside the match loop, so cost is
O(deliveries · total_keys · avg_waiters). A waiter only registers on its own
`keys` (from `BLPop`), so scanning all keys is wasteful. Matters under
blocking-heavy workloads, not the primary 80× cause.

### 4. Defensive re-add also does an O(n) prepend

`lists.go`, delivery fallback:

```go
l.data[key] = append([]string{d.val[1]}, l.data[key]...)
```

Only runs on a rare "shouldn't happen" race — correctness smell more than perf.
Resolved for free once the deque exists.

## Not bottlenecks (leave alone)

- The reverse loop allocating `reversed` — necessary for LPUSH semantics, negligible.
- The `delivery` struct / `toDeliver` slice — sized only to number of waiters.
- `[2]string` value type — fine.
- Delivering outside the lock — correct and intentional; keep it.

## Remediation plan

Priority order (first item closes ~90% of the gap):

1. Replace the `[]string` list value with a deque so LPUSH/RPUSH/LPOP/RPOP are
   amortized O(1). Options, closest-to-Redis first:
   - doubly-linked list of chunks (quicklist-style), or
   - ring buffer, or
   - minimum viable: grow at the tail and track a logical head offset so head
     ops are amortized O(1).
   Keep the `ListStore` interface and all waiter/blocking semantics unchanged.

2. Make `removeWaiterFromAllKeys` touch only the waiter's own keys. Store each
   waiter's key set (e.g. `map[chan [2]string][]string`, or wrap the channel in a
   small `waiter` struct holding its key list) and remove only from those keys.

3. The defensive re-add becomes O(1) once the deque is in place — no separate work.

### Verification plan

- Keep `TestLPushScaling`: after the fix, the ratio per doubling should drop from
  ~5–6× to ~2× (linear total build).
- Keep `TestPrependVsDeque` as a regression guard on the data-structure choice.
- Run the existing list tests (`internal/storage/lists_test.go`) to confirm
  semantics (ordering, blocking BLPop, waiter delivery) are preserved.

## Reproduction

```
go test ./internal/storage/ -run "TestLPushScaling|TestPrependVsDeque" -v -count=1
```

Note: these scaling tests are intentionally slow (the current O(n²) path takes
minutes at large n). Consider gating them behind `testing.Short()` or converting
to `go test -bench` once the fix lands.

## Implemented remedy (step 1 of 3) — ring-buffer deque

Step 1 (the dominant fix) is done. The `[]string` list value was replaced with a
generic ring-buffer deque.

### What changed

- New package `internal/utilities/deque` — a generic `Deque[T any]` ring buffer.
  - State: backing slice `buf`, logical head index `head`, element count `size`.
    The tail is derived (`(head+size) & (cap-1)`), never stored.
  - `size` disambiguates empty vs full; no empty-slot sentinel needed.
  - Capacity is always a power of two (New rounds up; grow doubles; empty→1), so
    index wrap is a bitwise `& (cap-1)` instead of `%` — a single ALU op on the
    hot path (At/Range/push/pop). This matters for a latency-sensitive in-memory
    store.
  - `grow` re-packs elements into logical order from index 0 and resets head to 0
    (a ring buffer cannot stay wrapped across a changed capacity boundary).
  - Pop zeroes the vacated slot (`var zero T`) so the backing array does not pin
    references — important for `string` elements (GC).
  - Unit tests in `internal/utilities/deque/deque_test.go` cover FIFO, LIFO,
    wrap-around without grow, grow-while-wrapped, mixed ends, Range, slot
    clearing, and many-grows (100k).
- `internal/storage/lists.go` now uses `map[string]*deque.Deque[string]`.
  LPUSH pushes each arg to the front in order (the explicit reverse was removed —
  pushing a,b,c to the front yields head=c,b,a, matching Redis). LPOP pops the
  front; LRANGE uses `Range`; empty-list cleanup (`delete` + `registry.Release`)
  preserved. Waiter / BLPop logic unchanged.

### Measured result (go1.25.0, windows/amd64)

`TestLPushScaling` — building one list via repeated LPush:

| n     | before (O(n²)) | after (deque) | ratio/doubling before → after |
|-------|----------------|---------------|-------------------------------|
| 5000  | 43.9 ms        | 1.97 ms       | —                             |
| 10000 | 218.7 ms       | 3.77 ms       | 4.98× → 1.91×                 |
| 20000 | 1.242 s        | 4.69 ms       | 5.68× → 1.24×                 |
| 40000 | 6.306 s        | 5.09 ms       | 5.08× → 1.09×                 |
| 80000 | 39.99 s        | 11.13 ms      | 6.34× → 2.18×                 |

The per-doubling ratio dropped from ~5–6× (quadratic) to ~2× (linear),
confirming O(n²) → O(n). At n=80000 the build went from ~40 s to ~11 ms
(~3600× at that size; the gain grows with n).

All existing list tests pass (LPush/RPush/LRange/LLen/LPop/Delete/WrongType and
the full BLPop suite: immediate, priority, blocking, multi-key, timeout, waiter
cleanup, double-delivery), confirming semantics are preserved.

### Remaining work

- Step 2 (DONE): the waiter subsystem was redesigned. A `waiter` struct now
  carries its own key list, so cleanup touches only the keys that waiter
  registered on — O(len(keys)) instead of O(all keys with waiters). The whole-map
  scan in the old `removeWaiterFromAllKeys` is gone. A single-use atomic
  "claimed" flag replaces the previous "non-blocking send then re-add on failure"
  race: a waiter is claimed under the lock before any value is handed over, so
  values can never be lost and double-delivery is impossible by construction.
  The out-of-lock re-add branch was removed. Verified with the full BLPop suite
  (immediate, priority, blocking, multi-key, timeout, waiter cleanup,
  double-delivery, deadlock) under `go test -race`.
- Step 3: subsumed — the defensive re-add is gone (see step 2).
- Known gap: the deque is grow-only; it never shrinks the backing array after
  many pops (Redis's quicklist reclaims memory). Candidate future work: halve
  capacity when `size` drops below `cap/4`. Left out to keep this change focused.
- Possible future step: evolve the single ring buffer into a quicklist
  (doubly-linked list of fixed-size ring-buffer chunks) to bound resize cost and
  allow incremental memory reclamation — the structure real Redis uses.
- Possible future step: the streams store (`internal/storage/streams.go`) has its
  own parallel waiter implementation. Once the list waiter shape is proven, a
  shared blocking-registry could remove that duplication. Not done; needs the two
  blocking semantics compared first.
