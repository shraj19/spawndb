#!/bin/bash
#
# Generate side-by-side comparison of FlashDB vs Redis benchmark results
#
# Usage: ./compare.sh <run-dir>
#
# Example: ./compare.sh ./benchmark-results/20241010-123456

set -euo pipefail

RUN_DIR="${1:-.}"

FLASHDB_CSV="${RUN_DIR}/flashdb-results.csv"
REDIS_CSV="${RUN_DIR}/redis-results.csv"

if [[ ! -f "$FLASHDB_CSV" ]]; then
    echo "Error: FlashDB results not found at $FLASHDB_CSV"
    exit 1
fi

echo ""
echo "╔════════════════════════════════════════════════════════════════════════════╗"
echo "║                    FlashDB vs Redis Comparison                             ║"
echo "╠════════════════════════════════════════════════════════════════════════════╣"
echo ""

# Print header
printf "%-15s %15s %15s %15s\n" "Command" "FlashDB (rps)" "Redis (rps)" "Ratio"
echo "─────────────────────────────────────────────────────────────────────────────"

# Parse CSVs and compare
# redis-benchmark CSV format: "test","rps","avg_latency_ms","min_latency_ms","p50_latency_ms","p95_latency_ms","p99_latency_ms","max_latency_ms"

tail -n +2 "$FLASHDB_CSV" | while IFS=',' read -r test flashdb_rps rest; do
    # Remove quotes
    test="${test//\"/}"
    flashdb_rps="${flashdb_rps//\"/}"

    if [[ -f "$REDIS_CSV" ]]; then
        # Find matching line in Redis results
        redis_line=$(grep "\"$test\"" "$REDIS_CSV" 2>/dev/null || echo "")
        if [[ -n "$redis_line" ]]; then
            redis_rps=$(echo "$redis_line" | cut -d',' -f2 | tr -d '"')

            # Calculate ratio (FlashDB / Redis)
            if [[ "$redis_rps" != "0" && -n "$redis_rps" ]]; then
                ratio=$(awk "BEGIN {printf \"%.2f\", $flashdb_rps / $redis_rps}")
                if (( $(echo "$ratio < 1" | bc -l) )); then
                    ratio_display="${ratio}x (slower)"
                else
                    ratio_display="${ratio}x"
                fi
            else
                ratio_display="N/A"
            fi

            printf "%-15s %15s %15s %15s\n" "$test" "$flashdb_rps" "$redis_rps" "$ratio_display"
        else
            printf "%-15s %15s %15s %15s\n" "$test" "$flashdb_rps" "N/A" "N/A"
        fi
    else
        printf "%-15s %15s %15s %15s\n" "$test" "$flashdb_rps" "N/A" "N/A"
    fi
done

echo ""
echo "╚════════════════════════════════════════════════════════════════════════════╝"
echo ""

# Show params if available
if [[ -f "${RUN_DIR}/params.json" ]]; then
    echo "Run parameters:"
    cat "${RUN_DIR}/params.json"
fi
