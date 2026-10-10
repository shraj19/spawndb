#!/bin/bash
#
# FlashDB vs Redis Benchmark Harness
#
# Runs identical redis-benchmark workloads against both FlashDB and real Redis,
# captures results as CSV, scrapes final /metrics, and optionally uploads to S3.
#
# Usage:
#   ./benchmark.sh [options]
#
# Options:
#   --flashdb-host HOST    FlashDB hostname/IP (default: localhost)
#   --flashdb-port PORT    FlashDB port (default: 6379)
#   --redis-host HOST      Redis hostname/IP (default: localhost)
#   --redis-port PORT      Redis port (default: 6380)
#   --metrics-port PORT    FlashDB metrics port (default: 9121)
#   --requests N           Number of requests per test (default: 100000)
#   --clients C            Number of parallel clients (default: 50)
#   --pipeline P           Pipeline depth (default: 1)
#   --data-size D          Data size in bytes (default: 3)
#   --output-dir DIR       Output directory (default: ./benchmark-results)
#   --s3-bucket BUCKET     S3 bucket for upload (optional)
#   --run-name NAME        Name for this run (default: timestamp)
#   --skip-redis           Skip Redis benchmark (FlashDB only)
#   --help                 Show this help
#
# Examples:
#   # Local Docker Compose test
#   ./benchmark.sh
#
#   # AWS benchmark with S3 upload
#   ./benchmark.sh --flashdb-host 10.0.1.10 --redis-host 10.0.1.20 \
#                  --s3-bucket my-benchmark-bucket --requests 1000000
#
#   # Sweep pipeline depths
#   for p in 1 4 16 64; do
#     ./benchmark.sh --pipeline $p --run-name "pipeline-$p"
#   done

set -euo pipefail

# ─────────────────────────────────────────────────────────────────────────────
# Default configuration
# ─────────────────────────────────────────────────────────────────────────────

FLASHDB_HOST="localhost"
FLASHDB_PORT="6379"
REDIS_HOST="localhost"
REDIS_PORT="6380"
METRICS_PORT="9121"
REQUESTS="100000"
CLIENTS="50"
PIPELINE="1"
DATA_SIZE="3"
OUTPUT_DIR="./benchmark-results"
S3_BUCKET=""
RUN_NAME=""
SKIP_REDIS=false

# Commands to benchmark (subset that FlashDB supports)
COMMANDS="set,get,incr,lpush,rpush,lpop,lrange_100,lrange_600"

# ─────────────────────────────────────────────────────────────────────────────
# Parse arguments
# ─────────────────────────────────────────────────────────────────────────────

while [[ $# -gt 0 ]]; do
    case $1 in
        --flashdb-host)   FLASHDB_HOST="$2"; shift 2 ;;
        --flashdb-port)   FLASHDB_PORT="$2"; shift 2 ;;
        --redis-host)     REDIS_HOST="$2"; shift 2 ;;
        --redis-port)     REDIS_PORT="$2"; shift 2 ;;
        --metrics-port)   METRICS_PORT="$2"; shift 2 ;;
        --requests)       REQUESTS="$2"; shift 2 ;;
        --clients)        CLIENTS="$2"; shift 2 ;;
        --pipeline)       PIPELINE="$2"; shift 2 ;;
        --data-size)      DATA_SIZE="$2"; shift 2 ;;
        --output-dir)     OUTPUT_DIR="$2"; shift 2 ;;
        --s3-bucket)      S3_BUCKET="$2"; shift 2 ;;
        --run-name)       RUN_NAME="$2"; shift 2 ;;
        --skip-redis)     SKIP_REDIS=true; shift ;;
        --help)
            head -45 "$0" | tail -40
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

# ─────────────────────────────────────────────────────────────────────────────
# Setup
# ─────────────────────────────────────────────────────────────────────────────

if [[ -z "$RUN_NAME" ]]; then
    RUN_NAME="$(date +%Y%m%d-%H%M%S)"
fi

RUN_DIR="${OUTPUT_DIR}/${RUN_NAME}"
mkdir -p "$RUN_DIR"

echo "╔══════════════════════════════════════════════════════════════════╗"
echo "║           FlashDB vs Redis Benchmark                             ║"
echo "╠══════════════════════════════════════════════════════════════════╣"
echo "║ Run Name:    $RUN_NAME"
echo "║ Requests:    $REQUESTS"
echo "║ Clients:     $CLIENTS"
echo "║ Pipeline:    $PIPELINE"
echo "║ Data Size:   $DATA_SIZE bytes"
echo "║ Commands:    $COMMANDS"
echo "║ Output:      $RUN_DIR"
echo "╚══════════════════════════════════════════════════════════════════╝"
echo ""

# Save run parameters
cat > "${RUN_DIR}/params.json" << EOF
{
    "run_name": "$RUN_NAME",
    "timestamp": "$(date -Iseconds)",
    "flashdb_host": "$FLASHDB_HOST",
    "flashdb_port": "$FLASHDB_PORT",
    "redis_host": "$REDIS_HOST",
    "redis_port": "$REDIS_PORT",
    "requests": $REQUESTS,
    "clients": $CLIENTS,
    "pipeline": $PIPELINE,
    "data_size": $DATA_SIZE,
    "commands": "$COMMANDS"
}
EOF

# ─────────────────────────────────────────────────────────────────────────────
# Helper functions
# ─────────────────────────────────────────────────────────────────────────────

run_benchmark() {
    local name="$1"
    local host="$2"
    local port="$3"
    local output_prefix="${RUN_DIR}/${name}"

    echo "────────────────────────────────────────────────────────────────────"
    echo "Benchmarking: $name ($host:$port)"
    echo "────────────────────────────────────────────────────────────────────"

    # Check connectivity first
    if ! redis-cli -h "$host" -p "$port" PING > /dev/null 2>&1; then
        echo "ERROR: Cannot connect to $name at $host:$port"
        return 1
    fi

    # Run benchmark with CSV output
    redis-benchmark \
        -h "$host" \
        -p "$port" \
        -n "$REQUESTS" \
        -c "$CLIENTS" \
        -P "$PIPELINE" \
        -d "$DATA_SIZE" \
        -t "$COMMANDS" \
        --csv \
        > "${output_prefix}-results.csv" 2>&1

    echo "Results saved to ${output_prefix}-results.csv"

    # Also run with latency percentiles (human readable)
    redis-benchmark \
        -h "$host" \
        -p "$port" \
        -n "$REQUESTS" \
        -c "$CLIENTS" \
        -P "$PIPELINE" \
        -d "$DATA_SIZE" \
        -t "$COMMANDS" \
        > "${output_prefix}-results.txt" 2>&1

    echo "Detailed output saved to ${output_prefix}-results.txt"
    echo ""
}

scrape_metrics() {
    local name="$1"
    local host="$2"
    local port="$3"
    local output="${RUN_DIR}/${name}-metrics.txt"

    echo "Scraping metrics from $name ($host:$port)..."

    if curl -s "http://${host}:${port}/metrics" > "$output" 2>/dev/null; then
        echo "Metrics saved to $output"
    else
        echo "Warning: Could not scrape metrics from $host:$port"
    fi
}

# ─────────────────────────────────────────────────────────────────────────────
# Run benchmarks
# ─────────────────────────────────────────────────────────────────────────────

# Scrape pre-benchmark metrics
echo ""
echo "═══════════════════════════════════════════════════════════════════"
echo "PRE-BENCHMARK METRICS"
echo "═══════════════════════════════════════════════════════════════════"
scrape_metrics "flashdb-pre" "$FLASHDB_HOST" "$METRICS_PORT"

# Benchmark FlashDB
echo ""
echo "═══════════════════════════════════════════════════════════════════"
echo "BENCHMARKING FLASHDB"
echo "═══════════════════════════════════════════════════════════════════"
run_benchmark "flashdb" "$FLASHDB_HOST" "$FLASHDB_PORT"

# Benchmark Redis (unless skipped)
if [[ "$SKIP_REDIS" == "false" ]]; then
    echo ""
    echo "═══════════════════════════════════════════════════════════════════"
    echo "BENCHMARKING REDIS (BASELINE)"
    echo "═══════════════════════════════════════════════════════════════════"
    run_benchmark "redis" "$REDIS_HOST" "$REDIS_PORT"
fi

# Scrape post-benchmark metrics
echo ""
echo "═══════════════════════════════════════════════════════════════════"
echo "POST-BENCHMARK METRICS"
echo "═══════════════════════════════════════════════════════════════════"
scrape_metrics "flashdb-post" "$FLASHDB_HOST" "$METRICS_PORT"

# ─────────────────────────────────────────────────────────────────────────────
# Generate comparison summary
# ─────────────────────────────────────────────────────────────────────────────

echo ""
echo "═══════════════════════════════════════════════════════════════════"
echo "RESULTS SUMMARY"
echo "═══════════════════════════════════════════════════════════════════"

echo ""
echo "FlashDB Results:"
echo "────────────────"
if [[ -f "${RUN_DIR}/flashdb-results.csv" ]]; then
    cat "${RUN_DIR}/flashdb-results.csv"
fi

if [[ "$SKIP_REDIS" == "false" ]]; then
    echo ""
    echo "Redis Results:"
    echo "──────────────"
    if [[ -f "${RUN_DIR}/redis-results.csv" ]]; then
        cat "${RUN_DIR}/redis-results.csv"
    fi
fi

# ─────────────────────────────────────────────────────────────────────────────
# Upload to S3 (if configured)
# ─────────────────────────────────────────────────────────────────────────────

if [[ -n "$S3_BUCKET" ]]; then
    echo ""
    echo "═══════════════════════════════════════════════════════════════════"
    echo "UPLOADING TO S3"
    echo "═══════════════════════════════════════════════════════════════════"

    S3_PATH="s3://${S3_BUCKET}/benchmark-runs/${RUN_NAME}/"
    echo "Uploading to $S3_PATH ..."

    aws s3 cp "$RUN_DIR" "$S3_PATH" --recursive

    echo "Upload complete."
    echo "View results: aws s3 ls $S3_PATH"
fi

echo ""
echo "╔══════════════════════════════════════════════════════════════════╗"
echo "║                    BENCHMARK COMPLETE                            ║"
echo "╠══════════════════════════════════════════════════════════════════╣"
echo "║ Results: $RUN_DIR"
if [[ -n "$S3_BUCKET" ]]; then
echo "║ S3:      s3://${S3_BUCKET}/benchmark-runs/${RUN_NAME}/"
fi
echo "╚══════════════════════════════════════════════════════════════════╝"
