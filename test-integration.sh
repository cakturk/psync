#!/bin/bash
#
# psync Integration Test Suite
# Tests full sync, delta sync, and file deletion scenarios
#

set -e  # Exit on error

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Test configuration
TEST_DIR="/tmp/psync-test-$$"
SERVER_DIR="${TEST_DIR}/server"
CLIENT_DIR="${TEST_DIR}/client"
SERVER_ADDR="127.0.0.1:33445"
BLOCKSIZE=512
SERVER_PID=""

# Binary paths (will be built)
PSYNCD_BIN=""
PSYNC_BIN=""

# Counters
TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0

#############################################
# Helper Functions
#############################################

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[PASS]${NC} $1"
    TESTS_PASSED=$((TESTS_PASSED + 1))
}

log_error() {
    echo -e "${RED}[FAIL]${NC} $1"
    TESTS_FAILED=$((TESTS_FAILED + 1))
}

log_test() {
    TESTS_RUN=$((TESTS_RUN + 1))
    echo -e "\n${YELLOW}[TEST ${TESTS_RUN}]${NC} $1"
}

cleanup() {
    log_info "Cleaning up test environment..."
    if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
        log_info "Stopping server (PID: $SERVER_PID)..."
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi
    if [ -d "$TEST_DIR" ]; then
        rm -rf "$TEST_DIR"
    fi
}

# Trap exit to ensure cleanup
trap cleanup EXIT INT TERM

verify_directories_identical() {
    local client="$1"
    local server="$2"
    local test_name="$3"

    if diff -r "$client" "$server" >/dev/null 2>&1; then
        log_success "$test_name: Directories are identical"
        return 0
    else
        log_error "$test_name: Directories differ!"
        diff -r "$client" "$server" | head -20
        return 1
    fi
}

verify_file_count() {
    local client="$1"
    local server="$2"
    local expected="$3"
    local test_name="$4"

    local client_count=$(find "$client" -type f | wc -l)
    local server_count=$(find "$server" -type f | wc -l)

    if [ "$client_count" -eq "$expected" ] && [ "$server_count" -eq "$expected" ]; then
        log_success "$test_name: File count correct ($expected files)"
        return 0
    else
        log_error "$test_name: File count mismatch (client: $client_count, server: $server_count, expected: $expected)"
        return 1
    fi
}

#############################################
# Setup Functions
#############################################

setup_environment() {
    log_info "Setting up test environment at $TEST_DIR..."

    # Create directories
    mkdir -p "$SERVER_DIR" "$CLIENT_DIR"

    # Build binaries
    log_info "Building psync binaries..."
    PSYNCD_BIN="${TEST_DIR}/psyncd"
    PSYNC_BIN="${TEST_DIR}/psync"

    go build -o "$PSYNCD_BIN" ./cmd/psyncd || {
        log_error "Failed to build psyncd"
        exit 1
    }

    go build -o "$PSYNC_BIN" ./cmd/psync || {
        log_error "Failed to build psync"
        exit 1
    }

    log_success "Environment setup complete"
}

start_server() {
    log_info "Starting psyncd server on $SERVER_ADDR (blocksize=$BLOCKSIZE)..."

    "$PSYNCD_BIN" -blocksize "$BLOCKSIZE" -listenaddr "$SERVER_ADDR" "$SERVER_DIR" \
        > "${TEST_DIR}/server.log" 2>&1 &
    SERVER_PID=$!

    # Wait for server to start
    sleep 1

    if kill -0 "$SERVER_PID" 2>/dev/null; then
        log_success "Server started (PID: $SERVER_PID)"
    else
        log_error "Server failed to start"
        cat "${TEST_DIR}/server.log"
        exit 1
    fi
}

create_test_files() {
    log_info "Creating test files in client directory..."

    cd "$CLIENT_DIR"

    # Binary files with random data
    dd if=/dev/urandom of=file1.bin bs=1024 count=100 2>/dev/null
    dd if=/dev/urandom of=file2.bin bs=1024 count=50 2>/dev/null
    dd if=/dev/urandom of=file3.bin bs=512 count=200 2>/dev/null

    # Text files
    cat > textfile.txt << 'EOF'
The quick brown fox jumps over the lazy dog
EOF

    cat > hello.txt << 'EOF'
Hello, World! This is a test file for psync.
EOF

    cat > nested.txt << 'EOF'
This is a multi-line test file.
It contains various types of content.
Including special characters: !@#$%^&*()
And some numbers: 1234567890
EOF

    # Nested directories
    mkdir -p subdir/nested
    echo "File in subdirectory" > subdir/subfile.txt
    echo "Deeply nested file" > subdir/nested/deep.txt

    # Empty directory (to test -allowemptydirs)
    mkdir -p emptydir

    cd - >/dev/null

    local file_count=$(find "$CLIENT_DIR" -type f | wc -l)
    log_success "Created $file_count test files"
}

#############################################
# Test Cases
#############################################

test_initial_sync() {
    log_test "Initial full synchronization"

    log_info "Running psync client..."
    "$PSYNC_BIN" -addr "$SERVER_ADDR" "$CLIENT_DIR" > "${TEST_DIR}/sync1.log" 2>&1

    if grep -q "recv'd ack:" "${TEST_DIR}/sync1.log"; then
        log_success "Received server acknowledgment"
    else
        log_error "No acknowledgment received"
        cat "${TEST_DIR}/sync1.log"
        return 1
    fi

    verify_file_count "$CLIENT_DIR" "$SERVER_DIR" 8 "Initial sync"
    verify_directories_identical "$CLIENT_DIR" "$SERVER_DIR" "Initial sync"
}

test_verify_checksums() {
    log_test "Verify file checksums match"

    local files=("file1.bin" "hello.txt" "subdir/subfile.txt")
    local all_match=true

    for file in "${files[@]}"; do
        local client_sum=$(md5sum "$CLIENT_DIR/$file" | awk '{print $1}')
        local server_sum=$(md5sum "$SERVER_DIR/$file" | awk '{print $1}')

        if [ "$client_sum" = "$server_sum" ]; then
            log_info "✓ $file: checksums match ($client_sum)"
        else
            log_error "✗ $file: checksum mismatch!"
            all_match=false
        fi
    done

    if $all_match; then
        log_success "All checksums verified"
    else
        log_error "Checksum verification failed"
        return 1
    fi
}

test_delta_sync() {
    log_test "Delta sync - modify existing file"

    log_info "Modifying hello.txt..."
    echo "This is new content appended to the file!" >> "$CLIENT_DIR/hello.txt"

    log_info "Running delta sync..."
    "$PSYNC_BIN" -addr "$SERVER_ADDR" "$CLIENT_DIR" > "${TEST_DIR}/sync2.log" 2>&1

    if grep -q "recv'd ack:" "${TEST_DIR}/sync2.log"; then
        log_success "Delta sync completed"
    else
        log_error "Delta sync failed"
        return 1
    fi

    # Verify the modification was synced
    if diff "$CLIENT_DIR/hello.txt" "$SERVER_DIR/hello.txt" >/dev/null; then
        log_success "Modified file synced correctly"
    else
        log_error "Modified file differs on server"
        return 1
    fi
}

test_new_file_sync() {
    log_test "Add new file and sync"

    log_info "Creating new file..."
    echo "This is a brand new file created after initial sync" > "$CLIENT_DIR/newfile.txt"

    log_info "Running sync..."
    "$PSYNC_BIN" -addr "$SERVER_ADDR" "$CLIENT_DIR" > "${TEST_DIR}/sync3.log" 2>&1

    if [ -f "$SERVER_DIR/newfile.txt" ]; then
        log_success "New file transferred to server"
    else
        log_error "New file not found on server"
        return 1
    fi

    verify_file_count "$CLIENT_DIR" "$SERVER_DIR" 9 "New file sync"
    verify_directories_identical "$CLIENT_DIR" "$SERVER_DIR" "New file sync"
}

test_file_deletion() {
    log_test "Delete file and sync"

    log_info "Deleting file2.bin from client..."
    rm "$CLIENT_DIR/file2.bin"

    log_info "Running sync with deletion..."
    "$PSYNC_BIN" -addr "$SERVER_ADDR" "$CLIENT_DIR" > "${TEST_DIR}/sync4.log" 2>&1

    # Check if file was deleted on server
    if [ ! -f "$SERVER_DIR/file2.bin" ]; then
        log_success "Deleted file removed from server"
    else
        log_error "Deleted file still exists on server"
        return 1
    fi

    verify_file_count "$CLIENT_DIR" "$SERVER_DIR" 8 "File deletion sync"
    verify_directories_identical "$CLIENT_DIR" "$SERVER_DIR" "File deletion sync"
}

test_large_file() {
    log_test "Sync large file (10MB)"

    log_info "Creating 10MB file..."
    dd if=/dev/urandom of="$CLIENT_DIR/largefile.bin" bs=1M count=10 2>/dev/null

    log_info "Syncing large file..."
    local start_time=$(date +%s)
    "$PSYNC_BIN" -addr "$SERVER_ADDR" "$CLIENT_DIR" > "${TEST_DIR}/sync5.log" 2>&1
    local end_time=$(date +%s)
    local duration=$((end_time - start_time))

    if [ -f "$SERVER_DIR/largefile.bin" ]; then
        local client_sum=$(md5sum "$CLIENT_DIR/largefile.bin" | awk '{print $1}')
        local server_sum=$(md5sum "$SERVER_DIR/largefile.bin" | awk '{print $1}')

        if [ "$client_sum" = "$server_sum" ]; then
            log_success "Large file synced correctly in ${duration}s"
        else
            log_error "Large file checksum mismatch"
            return 1
        fi
    else
        log_error "Large file not found on server"
        return 1
    fi
}

test_nested_directories() {
    log_test "Deep nested directory structure"

    log_info "Creating deeply nested structure..."
    mkdir -p "$CLIENT_DIR/level1/level2/level3/level4"
    echo "Deep file" > "$CLIENT_DIR/level1/level2/level3/level4/deepfile.txt"

    log_info "Syncing nested directories..."
    "$PSYNC_BIN" -addr "$SERVER_ADDR" "$CLIENT_DIR" > "${TEST_DIR}/sync6.log" 2>&1

    if [ -f "$SERVER_DIR/level1/level2/level3/level4/deepfile.txt" ]; then
        log_success "Nested directories synced correctly"
    else
        log_error "Nested file not found on server"
        return 1
    fi
}

test_special_characters() {
    log_test "Files with special characters in names"

    log_info "Creating files with special characters..."
    touch "$CLIENT_DIR/file with spaces.txt"
    echo "Content" > "$CLIENT_DIR/file_with-dashes.txt"
    echo "Content" > "$CLIENT_DIR/file.multiple.dots.txt"

    log_info "Syncing files with special names..."
    "$PSYNC_BIN" -addr "$SERVER_ADDR" "$CLIENT_DIR" > "${TEST_DIR}/sync7.log" 2>&1

    local all_exist=true
    if [ ! -f "$SERVER_DIR/file with spaces.txt" ]; then
        log_error "File with spaces not synced"
        all_exist=false
    fi
    if [ ! -f "$SERVER_DIR/file_with-dashes.txt" ]; then
        log_error "File with dashes not synced"
        all_exist=false
    fi
    if [ ! -f "$SERVER_DIR/file.multiple.dots.txt" ]; then
        log_error "File with dots not synced"
        all_exist=false
    fi

    if $all_exist; then
        log_success "Special character filenames handled correctly"
    else
        return 1
    fi
}

#############################################
# Main Test Runner
#############################################

print_header() {
    echo ""
    echo "=========================================="
    echo "  psync Integration Test Suite"
    echo "=========================================="
    echo ""
}

print_summary() {
    echo ""
    echo "=========================================="
    echo "  Test Summary"
    echo "=========================================="
    echo "Total tests run:    $TESTS_RUN"
    echo -e "${GREEN}Tests passed:       $TESTS_PASSED${NC}"
    if [ $TESTS_FAILED -gt 0 ]; then
        echo -e "${RED}Tests failed:       $TESTS_FAILED${NC}"
    else
        echo "Tests failed:       $TESTS_FAILED"
    fi
    echo "=========================================="

    if [ $TESTS_FAILED -eq 0 ]; then
        echo -e "${GREEN}✓ All tests passed!${NC}"
        return 0
    else
        echo -e "${RED}✗ Some tests failed${NC}"
        return 1
    fi
}

main() {
    print_header

    # Setup
    setup_environment
    create_test_files
    start_server

    # Run tests
    test_initial_sync || true
    test_verify_checksums || true
    test_delta_sync || true
    test_new_file_sync || true
    test_file_deletion || true
    test_large_file || true
    test_nested_directories || true
    test_special_characters || true

    # Final verification
    log_test "Final state verification"
    verify_directories_identical "$CLIENT_DIR" "$SERVER_DIR" "Final state"

    # Print summary
    print_summary
}

# Run main
main
exit $?
