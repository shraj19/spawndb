# SpawnDB Development Checklist

**Last Updated:** 2026-10-16  
**Status:** Post-benchmarking, needs systematic audit and fixes

---

## Task Allocation Legend

| Symbol | Meaning |
|--------|---------|
| 🤖 | AI does it (you review) |
| 👤 | You do it (AI assists) |
| 🤝 | Pair work (AI implements, you guide decisions) |

---

## Milestone A — Audit ⬜

> **Goal:** Understand what actually works before changing anything.

| Task | Owner | Notes |
|------|-------|-------|
| Trace command execution path | 🤖 | AI reads code, produces diagram |
| Document lock ownership | 🤖 | AI analyzes, you verify understanding |
| Verify each command's error behavior | 🤖 | AI creates test matrix |
| Audit blocking ops (BLPOP, XREAD) | 🤝 | Complex — AI audits, you validate logic |
| Audit transaction behavior | 🤝 | AI documents, you decide intended semantics |
| Audit replication propagation | 🤖 | AI checks which commands propagate |
| Audit persistence coverage | 🤖 | AI checks what's serialized |
| Run tests + race detector | 🤖 | AI runs, reports failures |
| Create architecture doc | 🤖 | AI writes, you review |
| Create command compatibility matrix | 🤖 | AI generates from code |
| **Create prioritized remediation plan** | 👤 | **You decide priorities** |

**Your time:** ~1-2 hours reviewing AI output, making priority decisions  
**AI time:** ~2-3 hours of analysis and documentation

---

## Milestone B — Correctness ⬜

> **Goal:** Fix bugs found in audit. No new features.

| Task | Owner | Notes |
|------|-------|-------|
| Define storage invariants | 👤 | You decide guarantees |
| Fix races | 🤝 | AI proposes fix, you verify correctness |
| Fix deadlocks | 🤝 | AI proposes, you understand the lock order |
| Fix waiter lifecycle issues | 🤝 | Complex concurrency — understand before merging |
| Test client disconnect during blocking | 🤖 | AI writes tests |
| Test expired keys + concurrency | 🤖 | AI writes tests |
| Test WRONGTYPE scenarios | 🤖 | AI writes tests |
| Add regression tests | 🤖 | AI writes, you review coverage |

**Your time:** ~3-4 hours understanding concurrency fixes  
**AI time:** ~4-5 hours implementing and testing

**⚠️ IMPORTANT:** Don't blindly merge concurrency fixes. Understand the lock ordering yourself.

---

## Milestone C — Protocol ⬜

> **Goal:** Robust RESP handling and pipelining.

| Task | Owner | Notes |
|------|-------|-------|
| Audit RESP parser edge cases | 🤖 | AI reviews code |
| Test partial reads/writes | 🤖 | AI writes tests |
| Test malformed frames | 🤖 | AI fuzzes parser |
| Define frame size limits | 👤 | You decide limits |
| **Implement pipelining** | 🤝 | **Key optimization — understand the design** |
| Benchmark pipelining | 🤖 | AI runs benchmarks |

**Your time:** ~2-3 hours on pipelining design  
**AI time:** ~3-4 hours implementation and testing

**⚠️ Pipelining is your biggest perf gap (5-6x). Understand this deeply for interviews.**

---

## Milestone D — Replication ⬜

> **Goal:** All mutations replicate correctly.

| Task | Owner | Notes |
|------|-------|-------|
| Document current behavior | 🤖 | AI audits |
| **Design: which commands propagate** | 👤 | **You decide the replication contract** |
| Fix propagation for all commands | 🤝 | AI implements your design |
| Fix offset accounting | 🤝 | AI implements, you verify math |
| Test reconnection scenarios | 🤖 | AI writes tests |
| Test lag under load | 🤖 | AI runs load tests |
| Document consistency guarantees | 👤 | You write — interview material |

**Your time:** ~3-4 hours on design decisions and documentation  
**AI time:** ~4-5 hours implementation

**⚠️ Replication semantics are interview gold. Own the design.**

---

## Milestone E — Persistence ⬜

> **Goal:** Snapshots work for all data types.

| Task | Owner | Notes |
|------|-------|-------|
| Audit what's currently persisted | 🤖 | AI checks |
| **Design versioned format** | 👤 | You decide format evolution strategy |
| Fix: persist all data types | 🤝 | AI implements your format |
| Handle corrupt snapshots | 🤖 | AI adds validation |
| Test crash recovery | 🤖 | AI writes tests |
| Document guarantees | 👤 | You write |

**Your time:** ~2-3 hours on format design  
**AI time:** ~3-4 hours implementation

---

## Milestone F — Transactions ⬜

> **Goal:** MULTI/EXEC has defined semantics.

| Task | Owner | Notes |
|------|-------|-------|
| Document current behavior | 🤖 | AI audits |
| **Define atomicity guarantees** | 👤 | **You decide — this is a design choice** |
| Test concurrent transactions | 🤖 | AI writes tests |
| Test transaction + TTL | 🤖 | AI writes tests |
| Test transaction + replication | 🤖 | AI writes tests |
| Document semantics | 👤 | You write |

**Your time:** ~2 hours on design  
**AI time:** ~2-3 hours testing

---

## Milestone G — Benchmarking ✅ (Partially Done)

> **Goal:** Fair, reproducible comparisons.

| Task | Owner | Status |
|------|-------|--------|
| Build benchmark harness | 🤖 | ✅ Done (Oct 2026) |
| Benchmark basic ops | 🤖 | ✅ Done |
| Benchmark pipelining | 🤖 | ✅ Done (gap identified) |
| Benchmark contention | 🤖 | ✅ Done |
| Fix network bias issue | 🤖 | ✅ Done (localhost) |
| Document methodology | 🤖 | ✅ Done |
| **Re-run after fixes** | 🤖 | ⬜ After Milestone B-F |

---

## Milestone H — Optimization ⬜

> **Goal:** Close performance gaps with evidence.

| Task | Owner | Notes |
|------|-------|-------|
| Profile pipelining gap | 🤖 | AI profiles |
| **Design pipelining fix** | 🤝 | You understand, AI implements |
| Profile large-value gap | 🤖 | AI profiles |
| Fix large-value allocation | 🤖 | AI implements sync.Pool etc |
| Reduce tail latency | 🤝 | GC tuning — understand trade-offs |
| Document before/after | 🤖 | AI generates reports |

**Your time:** ~3-4 hours understanding optimizations  
**AI time:** ~5-6 hours profiling and implementing

---

## Milestone I — Advanced (Future) ⬜

> **Only after A-H are solid.**

| Task | Owner | Notes |
|------|-------|-------|
| Evaluate AOF/WAL | 🤝 | Research + implement |
| Evaluate sorted sets | 🤝 | New data structure |
| Evaluate pub/sub | 🤝 | New feature |
| Evaluate clustering | 👤 | Big design decision |

---

## Recommended Execution Plan

### Week 1: Audit + Critical Fixes
```
Day 1-2: Milestone A (Audit) — AI does heavy lifting, you review
Day 3-5: Milestone B (Correctness) — Fix critical bugs found
```

### Week 2: Protocol + Replication
```
Day 1-2: Milestone C (Protocol + Pipelining)
Day 3-5: Milestone D (Replication)
```

### Week 3: Persistence + Transactions + Polish
```
Day 1-2: Milestone E (Persistence)
Day 3: Milestone F (Transactions)
Day 4-5: Re-benchmark, document, optimize
```

---

## What YOU Must Understand Deeply (Interview Prep)

These are topics you'll be asked about. Don't let AI do all the thinking:

1. **Lock ordering in KeyLock** — why 256 buckets? what's the trade-off?
2. **Pipelining design** — why is it slow now? what's the fix?
3. **Replication semantics** — what consistency do you guarantee?
4. **LPUSH optimization story** — you already have this (132x improvement)
5. **Benchmark methodology** — network bias discovery, localhost fix

---

## Time Estimate Summary

| Milestone | Your Time | AI Time | Total |
|-----------|-----------|---------|-------|
| A (Audit) | 2h | 3h | 5h |
| B (Correctness) | 4h | 5h | 9h |
| C (Protocol) | 3h | 4h | 7h |
| D (Replication) | 4h | 5h | 9h |
| E (Persistence) | 3h | 4h | 7h |
| F (Transactions) | 2h | 3h | 5h |
| G (Benchmark) | — | — | ✅ Done |
| H (Optimization) | 4h | 6h | 10h |
| **Total** | **22h** | **30h** | **52h** |

**Realistic timeline:** 2-3 weeks of part-time work, or 1 week intensive.

---

## Golden Rules

1. **AI writes code, you own design decisions**
2. **Never merge concurrency fixes you don't understand**
3. **Document your guarantees before implementing**
4. **Replication and persistence are correctness-critical — review carefully**
5. **Benchmarks must be reproducible and fair**
6. **The spec says "correctness before performance" — follow it**
