#!/bin/bash
set -euo pipefail

S3_BUCKET="flashdb-bench-artifacts-072055531063"
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
RESULTS_DIR="/home/ec2-user/benchmark-results/full-suite-${TIMESTAMP}"
S3_PATH="s3://${S3_BUCKET}/benchmark-runs
/full-suite-${TIMESTAMP}"
LOG_FILE="${RESULTS_DIR}/benchmark.log"

mkdir -p "$RESULTS_DIR"

log() { 
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" | tee -a "$LOG_FILE"
    aws s3 cp "$LOG_FILE" "${S3_PATH}/benchmark.log" --quiet 2>/dev/null || true
}

save_to_s3() {
    local file="$1"
    aws s3 cp "$file" "${S3_PATH}/${file#$RESULTS_DIR/}" --quiet 2>/dev/null || true
}

run_bench() {
    local name=$1 host=$2 port=$3 pipeline=$4 requests=$5 clients=$6 dir=$7
    local csv_file="${dir}/${name}-P${pipeline}.csv"
    
    log "  Running: $name (P=$pipeline n=$requests c=$clients)"
    redis6-benchmark -h "$host" -p "$port" -n "$requests" -c "$clients" -P "$pipeline" -t set,get,incr,lpush,lpop --csv > "$csv_file" 2>&1 || true
    save_to_s3 "$csv_file"
    log "  Saved to S3: ${name}-P${pipeline}.csv"
}

log "=== FlashDB Benchmark Suite ==="
log "S3: $S3_PATH"

# System info
echo "Timestamp: $(date)" > "$RESULTS_DIR/system-info.txt"
echo "FlashDB: 10.0.1.87:6379" >> "$RESULTS_DIR/system-info.txt"
echo "Redis Native: 10.0.1.73:6379" >> "$RESULTS_DIR/system-info.txt"
echo "Redis Docker: 10.0.1.73:6380" >> "$RESULTS_DIR/system-info.txt"
save_to_s3 "$RESULTS_DIR/system-info.txt"

# Connectivity test
log "Testing connectivity..."
redis6-cli -h 10.0.1.87 -p 6379 PING > /dev/null 2>&1 && log "  FlashDB: OK" || log "  FlashDB: FAILED"
redis6-cli -h 10.0.1.73 -p 6379 PING > /dev/null 2>&1 && log "  Redis Native: OK" || log "  Redis Native: FAILED"  
redis6-cli -h 10.0.1.73 -p 6380 PING > /dev/null 2>&1 && log "  Redis Docker: OK" || log "  Redis Docker: FAILED"

# TEST 1: Pipeline depths
for P in 1 16 64; do
    log "=== Pipeline $P ==="
    mkdir -p "$RESULTS_DIR/pipeline-$P"
    run_bench "flashdb" "10.0.1.87" "6379" "$P" "100000" "50" "$RESULTS_DIR/pipeline-$P"
    run_bench "redis-native" "10.0.1.73" "6379" "$P" "100000" "50" "$RESULTS_DIR/pipeline-$P"
    run_bench "redis-docker" "10.0.1.73" "6380" "$P" "100000" "50" "$RESULTS_DIR/pipeline-$P"
done

# TEST 2: Concurrency
for C in 10 50 100 200; do
    log "=== Clients $C ==="
    mkdir -p "$RESULTS_DIR/clients-$C"
    run_bench "flashdb" "10.0.1.87" "6379" "1" "100000" "$C" "$RESULTS_DIR/clients-$C"
    run_bench "redis-native" "10.0.1.73" "6379" "1" "100000" "$C" "$RESULTS_DIR/clients-$C"
done

# TEST 3: Data sizes
for D in 3 100 1024 10240; do
    log "=== DataSize $D ==="
    mkdir -p "$RESULTS_DIR/datasize-$D"
    redis6-benchmark -h 10.0.1.87 -p 6379 -n 100000 -c 50 -d $D -t set,get --csv > "$RESULTS_DIR/datasize-$D/flashdb.csv" 2>&1 || true
    save_to_s3 "$RESULTS_DIR/datasize-$D/flashdb.csv"
    redis6-benchmark -h 10.0.1.73 -p 6379 -n 100000 -c 50 -d $D -t set,get --csv > "$RESULTS_DIR/datasize-$D/redis.csv" 2>&1 || true
    save_to_s3 "$RESULTS_DIR/datasize-$D/redis.csv"
done

# TEST 4: Extended
log "=== Extended 1M ==="
mkdir -p "$RESULTS_DIR/extended"
run_bench "flashdb" "10.0.1.87" "6379" "1" "1000000" "50" "$RESULTS_DIR/extended"
run_bench "redis-native" "10.0.1.73" "6379" "1" "1000000" "50" "$RESULTS_DIR/extended"

# Final metrics
curl -s http://10.0.1.87:9121/metrics > "$RESULTS_DIR/flashdb-metrics.txt" 2>/dev/null || true
save_to_s3 "$RESULTS_DIR/flashdb-metrics.txt"

log "=== COMPLETE ==="
log "Download: aws s3 cp $S3_PATH ./results/ --recursive"
