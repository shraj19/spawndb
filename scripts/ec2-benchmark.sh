#!/bin/bash
# SpawnDB vs Redis Benchmark Script
# Runs on client EC2, collects all results

set -e

SPAWNDB_HOST="10.0.1.194"
REDIS_HOST="10.0.1.190"
SPAWNDB_METRICS="http://10.0.1.194:9121"
RESULTS_DIR="$HOME/benchmark-results-$(date +%Y%m%d-%H%M%S)"
REQUESTS=100000
BENCHMARK="redis6-benchmark"

mkdir -p "$RESULTS_DIR"
cd "$RESULTS_DIR"

log() {
    echo "[$(date '+%H:%M:%S')] $1" | tee -a benchmark.log
}

# Test connectivity first
log "Testing connectivity..."
redis6-cli -h $SPAWNDB_HOST -p 6379 PING || { log "ERROR: Cannot connect to SpawnDB"; exit 1; }
redis6-cli -h $REDIS_HOST -p 6379 PING || { log "ERROR: Cannot connect to Redis"; exit 1; }
log "Both servers responding to PING"

# Collect system info
log "Collecting system info..."
echo "Client: $(hostname)" > system-info.txt
echo "Date: $(date)" >> system-info.txt
echo "Kernel: $(uname -r)" >> system-info.txt
cat /proc/cpuinfo | grep "model name" | head -1 >> system-info.txt

# Function to run benchmark
run_bench() {
    local host=$1
    local name=$2
    local clients=$3
    local pipeline=$4
    local output="$name-c${clients}-p${pipeline}.csv"
    
    log "Running: $name clients=$clients pipeline=$pipeline"
    $BENCHMARK -h $host -p 6379 -n $REQUESTS -c $clients -P $pipeline \
        -t set,get,incr,lpush,rpush,lpop,lrange_100 --csv > "$output" 2>&1
}

# Warmup
log "=== WARMUP ==="
$BENCHMARK -h $SPAWNDB_HOST -p 6379 -n 10000 -c 10 -t set,get -q
$BENCHMARK -h $REDIS_HOST -p 6379 -n 10000 -c 10 -t set,get -q

# Baseline: 50 clients, no pipeline
log "=== BASELINE (50 clients, P=1) ==="
run_bench $SPAWNDB_HOST "spawndb" 50 1
run_bench $REDIS_HOST "redis" 50 1

# Pipeline depth comparison
for P in 1 16 64; do
    log "=== PIPELINE DEPTH $P ==="
    run_bench $SPAWNDB_HOST "spawndb" 50 $P
    run_bench $REDIS_HOST "redis" 50 $P
done

# Client scaling
for C in 10 50 100 200; do
    log "=== CLIENT COUNT $C ==="
    run_bench $SPAWNDB_HOST "spawndb" $C 1
    run_bench $REDIS_HOST "redis" $C 1
done

# Collect SpawnDB metrics
log "=== COLLECTING METRICS ==="
curl -s "$SPAWNDB_METRICS/metrics" > spawndb-metrics.txt 2>/dev/null || log "Warning: Could not fetch SpawnDB metrics"

# Collect pprof profiles (30 second CPU profile)
log "=== COLLECTING PPROF (30s CPU profile) ==="
curl -s "$SPAWNDB_METRICS/debug/pprof/profile?seconds=30" > spawndb-cpu.prof 2>/dev/null &
PPROF_PID=$!

# Run a load during profiling
log "Running load during profiling..."
$BENCHMARK -h $SPAWNDB_HOST -p 6379 -n 100000 -c 50 -t set,get -q

wait $PPROF_PID 2>/dev/null || log "Warning: pprof may not have completed"

# Heap profile
curl -s "$SPAWNDB_METRICS/debug/pprof/heap" > spawndb-heap.prof 2>/dev/null || log "Warning: Could not fetch heap profile"

log "=== BENCHMARK COMPLETE ==="
log "Results saved to: $RESULTS_DIR"
ls -la "$RESULTS_DIR"

echo ""
echo "=== QUICK SUMMARY ==="
echo "SpawnDB baseline (50c, P=1):"
cat spawndb-c50-p1.csv | head -5
echo ""
echo "Redis baseline (50c, P=1):"
cat redis-c50-p1.csv | head -5
