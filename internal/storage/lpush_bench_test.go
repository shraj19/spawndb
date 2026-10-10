package storage

import (
	"fmt"
	"strconv"
	"testing"
	"time"
)

// Measures how long it takes to build a single list of size n via repeated LPush.
// If LPush is O(1) amortized, time grows ~linearly with n.
// If LPush is O(n) (slice prepend), total build is O(n^2) -> time grows ~quadratically.
func benchBuild(n int) time.Duration {
	store, _ := newListStoreWithDeps()
	key := "benchkey"
	start := time.Now()
	for i := 0; i < n; i++ {
		_, _ = store.LPush(key, "v")
	}
	return time.Since(start)
}

func TestLPushScaling(t *testing.T) {
	sizes := []int{5000, 10000, 20000, 40000, 80000}
	var prev time.Duration
	for _, n := range sizes {
		// average of a few runs to reduce noise
		const runs = 3
		var total time.Duration
		for r := 0; r < runs; r++ {
			total += benchBuild(n)
		}
		d := total / runs
		ratio := 0.0
		if prev > 0 {
			ratio = float64(d) / float64(prev)
		}
		fmt.Printf("n=%-7d build=%-14v  ratioVsPrev(2x n)=%.2f  (O(n)->~2.0, O(n^2)->~4.0)\n",
			n, d, ratio)
		prev = d
	}
}

// Direct head-insert cost: current slice-prepend vs amortized tail-append deque,
// using plain []string so we isolate the data-structure cost.
func TestPrependVsDeque(t *testing.T) {
	for _, n := range []int{10000, 50000, 100000} {
		// slice prepend (what LPush does today)
		var sl []string
		t0 := time.Now()
		for i := 0; i < n; i++ {
			sl = append([]string{strconv.Itoa(i)}, sl...)
		}
		dSlice := time.Since(t0)

		// deque: append to tail (amortized O(1)); head is logical end
		var dq []string
		t1 := time.Now()
		for i := 0; i < n; i++ {
			dq = append(dq, strconv.Itoa(i))
		}
		dDeque := time.Since(t1)

		fmt.Printf("n=%-7d slice-prepend=%-14v deque-append=%-14v  slowdown=%.1fx\n",
			n, dSlice, dDeque, float64(dSlice)/float64(dDeque))
	}
}
