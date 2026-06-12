#!/usr/bin/env bash
# =============================================================================
# Gortex Comprehensive Benchmark Suite for Raspberry Pi / Low-Resource Devices
# =============================================================================
#
# Usage:
#   ./scripts/bench-rpi.sh                    # Full suite
#   ./scripts/bench-rpi.sh --quick            # Quick smoke test (1 iteration)
#   ./scripts/bench-rpi.sh --profile          # With CPU/memory profiles
#   ./scripts/bench-rpi.sh --compare <file>   # Compare against baseline
#   ./scripts/bench-rpi.sh --package graph    # Single package
#
# Output:
#   results/bench-<timestamp>.txt             # Raw benchmark output
#   results/bench-<timestamp>.json            # System info + summary
#   results/profiles/                         # CPU/mem profiles (if --profile)
#
# Requirements:
#   - Go 1.26+. Pure-Go tree-sitter runtime — no C toolchain needed.
#   - benchstat (go install golang.org/x/perf/cmd/benchstat@latest)
#
set -euo pipefail

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

RESULTS_DIR="results"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
BENCH_FILE="${RESULTS_DIR}/bench-${TIMESTAMP}.txt"
INFO_FILE="${RESULTS_DIR}/bench-${TIMESTAMP}.json"
PROFILE_DIR="${RESULTS_DIR}/profiles/${TIMESTAMP}"

BENCH_COUNT=3
BENCH_TIME="2s"
BENCH_TIMEOUT="30m"
DO_PROFILE=false
COMPARE_FILE=""
SINGLE_PACKAGE=""

# Packages to benchmark (in dependency order).
BENCH_PACKAGES=(
    "./internal/graph/"
    "./internal/search/"
    "./internal/parser/languages/"
    "./internal/resolver/"
    "./internal/indexer/"
    "./internal/query/"
    "./internal/analysis/"
)

# ---------------------------------------------------------------------------
# Parse arguments
# ---------------------------------------------------------------------------

while [[ $# -gt 0 ]]; do
    case "$1" in
        --quick)
            BENCH_COUNT=1
            BENCH_TIME="500ms"
            BENCH_TIMEOUT="10m"
            shift
            ;;
        --profile)
            DO_PROFILE=true
            shift
            ;;
        --compare)
            COMPARE_FILE="$2"
            shift 2
            ;;
        --package)
            SINGLE_PACKAGE="$2"
            shift 2
            ;;
        --count)
            BENCH_COUNT="$2"
            shift 2
            ;;
        --benchtime)
            BENCH_TIME="$2"
            shift 2
            ;;
        -h|--help)
            head -20 "$0" | tail -15
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

# ---------------------------------------------------------------------------
# Setup
# ---------------------------------------------------------------------------

mkdir -p "${RESULTS_DIR}"
if $DO_PROFILE; then
    mkdir -p "${PROFILE_DIR}"
fi

# Filter to single package if requested.
if [[ -n "$SINGLE_PACKAGE" ]]; then
    BENCH_PACKAGES=("./internal/${SINGLE_PACKAGE}/")
fi

# ---------------------------------------------------------------------------
# System information
# ---------------------------------------------------------------------------

echo "=== Gortex Benchmark Suite ==="
echo "Timestamp: ${TIMESTAMP}"
echo ""

collect_sysinfo() {
    local arch os cpus mem_total_kb mem_total_mb go_version kernel
    arch=$(uname -m)
    os=$(uname -s)
    kernel=$(uname -r)
    go_version=$(go version 2>/dev/null | awk '{print $3}')

    # CPU count
    if [[ "$os" == "Darwin" ]]; then
        cpus=$(sysctl -n hw.ncpu 2>/dev/null || echo "unknown")
        mem_total_mb=$(( $(sysctl -n hw.memsize 2>/dev/null || echo 0) / 1024 / 1024 ))
    elif [[ "$os" == "Linux" ]]; then
        cpus=$(nproc 2>/dev/null || echo "unknown")
        mem_total_kb=$(awk '/MemTotal/ {print $2}' /proc/meminfo 2>/dev/null || echo 0)
        mem_total_mb=$(( mem_total_kb / 1024 ))
    else
        cpus="unknown"
        mem_total_mb=0
    fi

    # Detect RPi
    local is_rpi=false
    if [[ -f /proc/device-tree/model ]]; then
        if grep -qi "raspberry" /proc/device-tree/model 2>/dev/null; then
            is_rpi=true
        fi
    fi

    # CPU model
    local cpu_model="unknown"
    if [[ "$os" == "Darwin" ]]; then
        cpu_model=$(sysctl -n machdep.cpu.brand_string 2>/dev/null || echo "unknown")
    elif [[ "$os" == "Linux" ]]; then
        cpu_model=$(awk -F: '/model name/ {print $2; exit}' /proc/cpuinfo 2>/dev/null | xargs || echo "unknown")
    fi

    # CPU frequency (MHz)
    local cpu_freq="unknown"
    if [[ "$os" == "Linux" ]] && [[ -f /sys/devices/system/cpu/cpu0/cpufreq/scaling_max_freq ]]; then
        cpu_freq=$(( $(cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_max_freq) / 1000 ))
    fi

    cat <<EOF
{
  "timestamp": "${TIMESTAMP}",
  "arch": "${arch}",
  "os": "${os}",
  "kernel": "${kernel}",
  "go_version": "${go_version}",
  "cpu_model": "${cpu_model}",
  "cpu_cores": ${cpus},
  "cpu_freq_mhz": "${cpu_freq}",
  "memory_mb": ${mem_total_mb},
  "is_raspberry_pi": ${is_rpi},
  "gomaxprocs": $(go env GOMAXPROCS 2>/dev/null || echo 0),
  "cgo_enabled": "$(go env CGO_ENABLED 2>/dev/null || echo unknown)",
  "goarch": "$(go env GOARCH 2>/dev/null || echo unknown)",
  "goos": "$(go env GOOS 2>/dev/null || echo unknown)",
  "bench_count": ${BENCH_COUNT},
  "bench_time": "${BENCH_TIME}"
}
EOF
}

SYSINFO=$(collect_sysinfo)
echo "$SYSINFO" > "${INFO_FILE}"
echo "System: $(echo "$SYSINFO" | python3 -c "import sys,json; d=json.load(sys.stdin); print(f'{d[\"cpu_model\"]} | {d[\"cpu_cores\"]} cores | {d[\"memory_mb\"]}MB RAM | {d[\"arch\"]} | Go {d[\"go_version\"]}')" 2>/dev/null || echo "System info saved to ${INFO_FILE}")"
echo "Output: ${BENCH_FILE}"
echo ""

# ---------------------------------------------------------------------------
# Run benchmarks
# ---------------------------------------------------------------------------

echo "Running benchmarks (count=${BENCH_COUNT}, benchtime=${BENCH_TIME})..."
echo ""

# Ensure binary builds.
echo "--- Building gortex ---"
go build -o /dev/null ./cmd/gortex/ 2>&1 || {
    echo "ERROR: Build failed."
    exit 1
}
echo "Build OK"
echo ""

run_bench() {
    local pkg="$1"
    local pkg_name
    pkg_name=$(basename "$pkg" | tr -d '/')

    echo "--- Benchmarking: ${pkg_name} ---"

    local extra_flags="-bench=. -benchmem -count=${BENCH_COUNT} -benchtime=${BENCH_TIME} -timeout=${BENCH_TIMEOUT}"

    if $DO_PROFILE; then
        local cpu_prof="${PROFILE_DIR}/${pkg_name}_cpu.prof"
        local mem_prof="${PROFILE_DIR}/${pkg_name}_mem.prof"
        extra_flags="${extra_flags} -cpuprofile=${cpu_prof} -memprofile=${mem_prof}"
    fi

    # Run with -race disabled for accurate timing (race detector adds ~10x overhead).
    if ! go test ${extra_flags} -run='^$' "${pkg}" 2>&1 | tee -a "${BENCH_FILE}"; then
        echo "WARNING: Benchmarks failed for ${pkg_name}" >&2
    fi
    echo "" >> "${BENCH_FILE}"
}

# Header in results file.
{
    echo "# Gortex Benchmark Results"
    echo "# Timestamp: ${TIMESTAMP}"
    echo "# System: $(uname -m) / $(uname -s) / $(go version)"
    echo "# Count: ${BENCH_COUNT}, BenchTime: ${BENCH_TIME}"
    echo ""
} > "${BENCH_FILE}"

for pkg in "${BENCH_PACKAGES[@]}"; do
    run_bench "$pkg"
done

echo ""
echo "=== Benchmark Complete ==="
echo "Results: ${BENCH_FILE}"

# ---------------------------------------------------------------------------
# Summary statistics
# ---------------------------------------------------------------------------

if command -v benchstat &>/dev/null; then
    echo ""
    echo "--- Summary (benchstat) ---"
    benchstat "${BENCH_FILE}" 2>/dev/null || true
fi

# ---------------------------------------------------------------------------
# Compare against baseline
# ---------------------------------------------------------------------------

if [[ -n "$COMPARE_FILE" ]]; then
    if command -v benchstat &>/dev/null; then
        echo ""
        echo "--- Comparison vs Baseline ---"
        benchstat "$COMPARE_FILE" "${BENCH_FILE}"
    else
        echo "Install benchstat for comparison: go install golang.org/x/perf/cmd/benchstat@latest"
    fi
fi

# ---------------------------------------------------------------------------
# RPi-specific warnings
# ---------------------------------------------------------------------------

MEM_MB=$(echo "$SYSINFO" | python3 -c "import sys,json; print(json.load(sys.stdin)['memory_mb'])" 2>/dev/null || echo 0)
if [[ "$MEM_MB" -gt 0 ]] && [[ "$MEM_MB" -lt 2048 ]]; then
    echo ""
    echo "⚠️  Low memory detected (${MEM_MB}MB). Consider:"
    echo "   - Setting GOGC=50 to reduce GC pressure"
    echo "   - Using --quick for faster iteration"
    echo "   - Running individual packages with --package <name>"
fi

IS_RPI=$(echo "$SYSINFO" | python3 -c "import sys,json; print(json.load(sys.stdin)['is_raspberry_pi'])" 2>/dev/null || echo false)
if [[ "$IS_RPI" == "true" ]]; then
    echo ""
    echo "🍓 Raspberry Pi detected! Tips:"
    echo "   - Ensure adequate cooling (benchmarks are CPU-intensive)"
    echo "   - Consider running with: GOMAXPROCS=2 ./scripts/bench-rpi.sh"
    echo "   - Use --quick for initial validation"
fi

if $DO_PROFILE; then
    echo ""
    echo "--- Profiles ---"
    echo "CPU profiles: ${PROFILE_DIR}/*_cpu.prof"
    echo "Mem profiles: ${PROFILE_DIR}/*_mem.prof"
    echo ""
    echo "Analyze with:"
    echo "  go tool pprof -http=:8080 ${PROFILE_DIR}/<pkg>_cpu.prof"
    echo "  go tool pprof -top ${PROFILE_DIR}/<pkg>_mem.prof"
fi
