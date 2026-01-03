#!/bin/bash
#
# psync Monitor Mode Stress Test
# Simulates high-frequency filesystem churn to test monitor mode stability
# under conditions similar to C compiler builds (rapid create/modify/delete)
#

set -e  # Exit on error (except where explicitly handled)

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Test configuration
TEST_DIR="/tmp/psync-monitor-stress-$$"
SOURCE_DIR="${TEST_DIR}/source"
TARGET_DIR="${TEST_DIR}/target"
SERVER_ADDR="127.0.0.1:33446"
BLOCKSIZE=512
SERVER_PID=""
PSYNC_PID=""
CHURN_PIDS=()

# Configurable parameters
DURATION="${DURATION:-30}"           # Test duration in seconds
CHURN_WORKERS="${CHURN_WORKERS:-4}" # Number of parallel churn generators
PSYNC_BIN="${PSYNC_BIN:-}"          # Path to psync binary
PSYNCD_BIN="${PSYNCD_BIN:-}"        # Path to psyncd binary
VERBOSE="${VERBOSE:-0}"              # Verbose output

# Test state
TEST_FAILED=0
PSYNC_CRASHED=0
PSYNC_HUNG=0

#############################################
# Helper Functions
#############################################

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[PASS]${NC} $1"
}

log_error() {
    echo -e "${RED}[FAIL]${NC} $1"
    TEST_FAILED=1
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

cleanup() {
    # Prevent recursive cleanup
    if [ "${CLEANUP_RUNNING:-0}" -eq 1 ]; then
        return
    fi
    CLEANUP_RUNNING=1

    log_info "Cleaning up test environment..."

    # Stop churn generators first (most aggressive)
    for pid in "${CHURN_PIDS[@]}"; do
        if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
            kill -9 "$pid" 2>/dev/null || true
        fi
    done

    # Don't wait for churn processes - they're background noise

    # Stop psync client
    if [ -n "$PSYNC_PID" ] && kill -0 "$PSYNC_PID" 2>/dev/null; then
        kill -9 "$PSYNC_PID" 2>/dev/null || true
    fi

    # Stop server
    if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
        kill -9 "$SERVER_PID" 2>/dev/null || true
    fi

    # Brief sleep to let processes die
    sleep 0.5

    # Clean up test directories
    if [ -d "$TEST_DIR" ]; then
        rm -rf "$TEST_DIR" 2>/dev/null || true
    fi
}

# Trap exit to ensure cleanup
trap cleanup EXIT INT TERM

usage() {
    cat <<EOF
Usage: $0 [OPTIONS]

Monitor mode stress test for psync. Simulates high-frequency filesystem
churn similar to C compiler builds.

Options:
  -d DURATION    Test duration in seconds (default: 30)
  -w WORKERS     Number of parallel churn generators (default: 4)
  -p PSYNC_BIN   Path to psync binary (default: auto-detect)
  -s PSYNCD_BIN  Path to psyncd binary (default: auto-detect)
  -v             Verbose output
  -h             Show this help message

Environment Variables:
  DURATION       Same as -d option
  CHURN_WORKERS  Same as -w option
  PSYNC_BIN      Same as -p option
  PSYNCD_BIN     Same as -s option
  VERBOSE        Enable verbose output (set to 1)

Examples:
  # Run with defaults (30 seconds, 4 workers)
  $0

  # Run for 60 seconds with 8 workers
  $0 -d 60 -w 8

  # Use specific binaries
  $0 -p ./psync -s ./psyncd

EOF
    exit 0
}

#############################################
# Churn Generator Functions
#############################################

# Simulates C compiler object file creation pattern:
# - Create temp file with random content
# - Rename to final name (atomic)
churn_compile_pattern() {
    local worker_id=$1
    local subdir="${SOURCE_DIR}/build${worker_id}"
    mkdir -p "$subdir"

    local counter=0
    while true; do
        local filename="file_${worker_id}_${counter}.o"
        local tempname=".${filename}.tmp"

        # Write to temp file (simulating compilation)
        dd if=/dev/urandom of="${subdir}/${tempname}" bs=1024 count=$((RANDOM % 100 + 1)) 2>/dev/null

        # Atomic rename (simulating compiler finalizing object file)
        mv "${subdir}/${tempname}" "${subdir}/${filename}" 2>/dev/null || true

        counter=$((counter + 1))

        # Occasionally delete old files (simulating 'make clean')
        if [ $((counter % 20)) -eq 0 ]; then
            find "$subdir" -name "*.o" -type f | head -5 | xargs rm -f 2>/dev/null || true
        fi

        sleep 0.01  # 100 ops/sec per worker
    done
}

# Simulates rapid append operations (log files, incremental writes)
churn_append_pattern() {
    local worker_id=$1
    local subdir="${SOURCE_DIR}/logs${worker_id}"
    mkdir -p "$subdir"

    local logfile="${subdir}/build.log"
    touch "$logfile"

    while true; do
        echo "Log entry $(date +%s.%N) from worker $worker_id" >> "$logfile"

        # Occasionally truncate log
        if [ $((RANDOM % 100)) -eq 0 ]; then
            > "$logfile"
        fi

        sleep 0.02  # 50 ops/sec per worker
    done
}

# Simulates nested directory creation/deletion
churn_directory_pattern() {
    local worker_id=$1
    local base="${SOURCE_DIR}/tmp${worker_id}"

    while true; do
        local depth=$((RANDOM % 4 + 1))
        local dirpath="$base"

        # Create nested directories
        for i in $(seq 1 $depth); do
            dirpath="${dirpath}/d${i}"
        done
        mkdir -p "$dirpath" 2>/dev/null || true

        # Create some files
        for i in {1..3}; do
            echo "data" > "${dirpath}/f${i}.txt" 2>/dev/null || true
        done

        # Occasionally delete entire subtree
        if [ $((RANDOM % 10)) -eq 0 ]; then
            rm -rf "$base" 2>/dev/null || true
        fi

        sleep 0.05  # 20 ops/sec per worker
    done
}

# Simulates rapid small file creation/deletion
churn_rapid_small_files() {
    local worker_id=$1
    local subdir="${SOURCE_DIR}/temp${worker_id}"
    mkdir -p "$subdir"

    while true; do
        # Burst create
        for i in {1..10}; do
            echo "temp data $i" > "${subdir}/temp${i}.txt" 2>/dev/null || true
        done

        sleep 0.01

        # Burst delete
        rm -f "${subdir}"/temp*.txt 2>/dev/null || true

        sleep 0.01
    done
}

#############################################
# Setup and Execution
#############################################

parse_args() {
    while getopts "d:w:p:s:vh" opt; do
        case $opt in
            d) DURATION="$OPTARG" ;;
            w) CHURN_WORKERS="$OPTARG" ;;
            p) PSYNC_BIN="$OPTARG" ;;
            s) PSYNCD_BIN="$OPTARG" ;;
            v) VERBOSE=1 ;;
            h) usage ;;
            *) usage ;;
        esac
    done
}

setup_environment() {
    log_info "Setting up test environment at $TEST_DIR..."

    # Create directories
    mkdir -p "$SOURCE_DIR" "$TARGET_DIR"

    # Detect or verify binaries
    if [ -z "$PSYNCD_BIN" ]; then
        if [ -f "./psyncd" ]; then
            PSYNCD_BIN="./psyncd"
        elif command -v psyncd >/dev/null 2>&1; then
            PSYNCD_BIN="psyncd"
        else
            # Build it
            log_info "Building psyncd..."
            go build -o "${TEST_DIR}/psyncd" ./cmd/psyncd || {
                log_error "Failed to build psyncd"
                exit 1
            }
            PSYNCD_BIN="${TEST_DIR}/psyncd"
        fi
    fi

    if [ -z "$PSYNC_BIN" ]; then
        if [ -f "./psync" ]; then
            PSYNC_BIN="./psync"
        elif command -v psync >/dev/null 2>&1; then
            PSYNC_BIN="psync"
        else
            # Build it
            log_info "Building psync..."
            go build -o "${TEST_DIR}/psync" ./cmd/psync || {
                log_error "Failed to build psync"
                exit 1
            }
            PSYNC_BIN="${TEST_DIR}/psync"
        fi
    fi

    log_success "Using psync: $PSYNC_BIN"
    log_success "Using psyncd: $PSYNCD_BIN"
}

start_server() {
    log_info "Starting psyncd server on $SERVER_ADDR..."

    local log_file="${TEST_DIR}/server.log"
    if [ "$VERBOSE" -eq 1 ]; then
        "$PSYNCD_BIN" -blocksize "$BLOCKSIZE" -listenaddr "$SERVER_ADDR" "$TARGET_DIR" \
            > "$log_file" 2>&1 &
    else
        "$PSYNCD_BIN" -blocksize "$BLOCKSIZE" -listenaddr "$SERVER_ADDR" "$TARGET_DIR" \
            >/dev/null 2>&1 &
    fi
    SERVER_PID=$!

    sleep 1

    if kill -0 "$SERVER_PID" 2>/dev/null; then
        log_success "Server started (PID: $SERVER_PID)"
    else
        log_error "Server failed to start"
        if [ -f "$log_file" ]; then
            cat "$log_file"
        fi
        exit 1
    fi
}

start_monitor_mode() {
    log_info "Starting psync in monitor mode..."

    local log_file="${TEST_DIR}/psync.log"
    if [ "$VERBOSE" -eq 1 ]; then
        "$PSYNC_BIN" -mon -addr "$SERVER_ADDR" "$SOURCE_DIR" \
            > "$log_file" 2>&1 &
    else
        "$PSYNC_BIN" -mon -addr "$SERVER_ADDR" "$SOURCE_DIR" \
            >/dev/null 2>&1 &
    fi
    PSYNC_PID=$!

    sleep 2

    if kill -0 "$PSYNC_PID" 2>/dev/null; then
        log_success "psync monitor started (PID: $PSYNC_PID)"
    else
        PSYNC_CRASHED=1
        log_error "psync failed to start in monitor mode"
        if [ -f "$log_file" ]; then
            cat "$log_file"
        fi
        exit 1
    fi
}

start_churn_generators() {
    log_info "Starting $CHURN_WORKERS churn generator workers..."

    local patterns=(
        churn_compile_pattern
        churn_append_pattern
        churn_directory_pattern
        churn_rapid_small_files
    )

    for i in $(seq 0 $((CHURN_WORKERS - 1))); do
        local pattern_idx=$((i % ${#patterns[@]}))
        local pattern="${patterns[$pattern_idx]}"

        $pattern "$i" &
        CHURN_PIDS+=($!)

        [ "$VERBOSE" -eq 1 ] && log_info "Started worker $i (PID: ${CHURN_PIDS[-1]}) - $pattern"
    done

    log_success "Started $CHURN_WORKERS churn generators"
}

monitor_psync_health() {
    local duration=$1
    local check_interval=1  # Check every second
    local checks=$((duration / check_interval))

    log_info "Monitoring psync health for ${duration}s..."

    for i in $(seq 1 $checks); do
        sleep $check_interval

        if ! kill -0 "$PSYNC_PID" 2>/dev/null; then
            PSYNC_CRASHED=1
            log_error "psync crashed during churn (after ${i}s)"
            return 1
        fi

        if [ $((i % 10)) -eq 0 ]; then
            log_info "Still running... (${i}/${duration}s)"
        fi
    done

    log_success "psync survived ${duration}s of churn"
    return 0
}

stop_churn_generators() {
    log_info "Stopping churn generators..."

    for pid in "${CHURN_PIDS[@]}"; do
        if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
            kill -9 "$pid" 2>/dev/null || true
        fi
    done

    # Don't wait - just kill them all
    CHURN_PIDS=()

    log_success "Churn generators stopped"
}

verify_final_state() {
    log_info "Verifying final state..."

    # If psync already crashed, just do final sync verification
    if ! kill -0 "$PSYNC_PID" 2>/dev/null; then
        log_warn "psync already dead, performing final verification sync..."
    else
        # Try graceful stop with short timeout
        log_info "Stopping psync..."
        kill -TERM "$PSYNC_PID" 2>/dev/null || true

        local wait_count=0
        while kill -0 "$PSYNC_PID" 2>/dev/null && [ $wait_count -lt 30 ]; do
            sleep 0.1
            wait_count=$((wait_count + 1))
        done

        if kill -0 "$PSYNC_PID" 2>/dev/null; then
            log_warn "psync did not exit within 3s, force killing"
            kill -9 "$PSYNC_PID" 2>/dev/null || true
            PSYNC_HUNG=1
        fi
    fi

    # Do one final manual sync to establish ground truth
    log_info "Performing final one-off sync for verification..."
    if timeout 10 "$PSYNC_BIN" -addr "$SERVER_ADDR" "$SOURCE_DIR" >/dev/null 2>&1; then
        log_success "Final sync completed"
    else
        log_error "Final sync failed or timed out"
        return 1
    fi

    # Compare source and target
    local source_count=$(find "$SOURCE_DIR" -type f 2>/dev/null | wc -l)
    local target_count=$(find "$TARGET_DIR" -type f 2>/dev/null | wc -l)

    log_info "Source files: $source_count, Target files: $target_count"

    # After final sync, they should match
    if diff -r "$SOURCE_DIR" "$TARGET_DIR" >/dev/null 2>&1; then
        log_success "Final state: directories match perfectly"
    else
        log_error "Final state: directories differ"
        if [ "$VERBOSE" -eq 1 ]; then
            diff -r "$SOURCE_DIR" "$TARGET_DIR" 2>&1 | head -20
        fi
        TEST_FAILED=1
        return 1
    fi

    return 0
}

print_summary() {
    echo ""
    echo "=========================================="
    echo "  Monitor Mode Stress Test Summary"
    echo "=========================================="
    echo "Duration:        ${DURATION}s"
    echo "Workers:         $CHURN_WORKERS"
    echo "Crash detected:  $([ $PSYNC_CRASHED -eq 1 ] && echo 'YES' || echo 'NO')"
    echo "Hung on exit:    $([ $PSYNC_HUNG -eq 1 ] && echo 'YES' || echo 'NO')"
    echo "Test result:     $([ $TEST_FAILED -eq 0 ] && echo -e "${GREEN}PASS${NC}" || echo -e "${RED}FAIL${NC}")"
    echo "=========================================="

    if [ $TEST_FAILED -eq 0 ]; then
        echo -e "${GREEN}✓ Monitor mode survived stress test${NC}"
        return 0
    else
        echo -e "${RED}✗ Monitor mode failed stress test${NC}"
        echo ""
        echo "Logs available at:"
        echo "  Server: ${TEST_DIR}/server.log"
        echo "  Client: ${TEST_DIR}/psync.log"
        return 1
    fi
}

#############################################
# Main
#############################################

main() {
    parse_args "$@"

    echo ""
    echo "=========================================="
    echo "  psync Monitor Mode Stress Test"
    echo "=========================================="
    echo "Duration:        ${DURATION}s"
    echo "Churn workers:   $CHURN_WORKERS"
    echo "=========================================="
    echo ""

    setup_environment
    start_server
    start_monitor_mode
    start_churn_generators

    if monitor_psync_health "$DURATION"; then
        stop_churn_generators
        verify_final_state || true
    else
        stop_churn_generators
        TEST_FAILED=1
    fi

    print_summary
}

main "$@"
exit $TEST_FAILED
