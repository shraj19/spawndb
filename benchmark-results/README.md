# Benchmark Results

This folder contains benchmark tooling and consolidated results.

## Structure

```
benchmark-results/
├── .gitignore      # Ignores raw data
├── README.md       # This file
├── HISTORY.md      # Consolidated results (committed)
└── <run-folders>/  # Raw data (gitignored)
```

## Running Benchmarks

### On EC2 (recommended for fair comparison)

```bash
# Provision infrastructure
./scripts/aws-provision.sh

# Run benchmarks
./scripts/benchmark.sh \
  --flashdb-host <spawndb-ip> \
  --redis-host <redis-ip> \
  --requests 100000 \
  --clients 50

# Full suite (multiple configs)
./scripts/run-all-benchmarks.sh
```

### Local (for quick iteration)

```bash
# Start SpawnDB
go run ./cmd/server

# Run redis-benchmark against it
redis-benchmark -p 6379 -n 10000 -c 10 -t set,get
```

## After Benchmarking

1. Analyze raw results in `<run-folder>/`
2. Add summary to `HISTORY.md`
3. Delete raw data (or keep locally, it's gitignored)

## Profiling

SpawnDB exposes pprof at `:6060`:

```bash
# CPU profile (30 seconds)
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30

# Memory profile
go tool pprof http://localhost:6060/debug/pprof/heap

# Mutex contention
go tool pprof http://localhost:6060/debug/pprof/mutex
```
