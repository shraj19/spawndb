# FlashDB has been renamed to SpawnDB

This project has been renamed to better reflect its concurrent architecture.

## New Location

**👉 [github.com/shraj19/spawndb](https://github.com/shraj19/spawndb)**

## Why the rename?

- **SpawnDB** = "spawn goroutines" — directly reflects the Go concurrency model
- The name better communicates the parallel, goroutine-per-connection architecture
- "FlashDB" was too generic and commonly used

## Migration

If you have `github.com/shraj19/flashdb` in your `go.mod`, update it to:

```go
require github.com/shraj19/spawndb v0.x.x
```

All features, documentation, and benchmarks are available at the new repo.
