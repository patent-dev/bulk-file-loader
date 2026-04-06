#!/bin/bash
#
# CLI integration test. Requires credentials in environment:
#   EPO_BDDS_USERNAME, EPO_BDDS_PASSWORD
#   DPMA_CONNECT_PLUS_USERNAME, DPMA_CONNECT_PLUS_PASSWORD
#
# Usage: ./test/cli-integration.sh ./bulk-file-loader
#
set -euo pipefail

BLF="${1:?Usage: $0 <binary>}"
DIR=$(mktemp -d)
trap "rm -rf $DIR" EXIT

: "${EPO_BDDS_USERNAME:?EPO_BDDS_USERNAME not set}"
: "${EPO_BDDS_PASSWORD:?EPO_BDDS_PASSWORD not set}"
: "${DPMA_CONNECT_PLUS_USERNAME:?DPMA_CONNECT_PLUS_USERNAME not set}"
: "${DPMA_CONNECT_PLUS_PASSWORD:?DPMA_CONNECT_PLUS_PASSWORD not set}"

export BULK_LOADER_PASSPHRASE=testtest
export BULK_LOADER_DATA_DIR="$DIR"

PASS=0
FAIL=0
ERRORS=""

run() {
    local name="$1"
    shift
    if "$@" >/dev/null 2>&1; then
        PASS=$((PASS + 1))
    else
        FAIL=$((FAIL + 1))
        ERRORS="$ERRORS\n  FAIL: $name"
    fi
    printf "  %-50s %s\n" "$name" "$([ $? -eq 0 ] && echo 'PASS' || echo 'FAIL')"
}

# expect_fail: command should return non-zero
run_fail() {
    local name="$1"
    shift
    if ! "$@" >/dev/null 2>&1; then
        PASS=$((PASS + 1))
        printf "  %-50s %s\n" "$name" "PASS"
    else
        FAIL=$((FAIL + 1))
        ERRORS="$ERRORS\n  FAIL: $name (expected failure)"
        printf "  %-50s %s\n" "$name" "FAIL"
    fi
}

echo "CLI Test Suite"
echo "Binary: $BLF"
echo "Data dir: $DIR"
echo ""

echo "=== Basic ==="
run "version"                     "$BLF" version
run "help"                        "$BLF" --help
run "status (fresh db)"           "$BLF" status
run "status --format json"        "$BLF" status --format json
run "status --format csv"         "$BLF" status --format csv
run "invalid format returns error" bash -c "! $BLF status --format xml 2>/dev/null"

echo ""
echo "=== Setup ==="
run "setup (env var)"             "$BLF" setup

echo ""
echo "=== Sources ==="
run "source ls"                   "$BLF" source ls
run "source ls --format json"     "$BLF" source ls --format json
run "source ls --format csv"      "$BLF" source ls --format csv
run "source ls -q"                "$BLF" source ls -q
run "source show epo-bdds"        "$BLF" source show epo-bdds
run "source show --format json"   "$BLF" source show epo-bdds --format json
run_fail "source show nonexistent" "$BLF" source show nonexistent

run "source enable epo-bdds"      "$BLF" source enable epo-bdds --username "$EPO_BDDS_USERNAME" --password "$EPO_BDDS_PASSWORD"
run "source enable dpma"          "$BLF" source enable dpma-connect-plus --username "$DPMA_CONNECT_PLUS_USERNAME" --password "$DPMA_CONNECT_PLUS_PASSWORD"
run "source test epo-bdds"        "$BLF" source test epo-bdds --username "$EPO_BDDS_USERNAME" --password "$EPO_BDDS_PASSWORD"
run "source disable dpma"         "$BLF" source disable dpma-connect-plus
run "source ls shows disabled"    bash -c "$BLF source ls 2>/dev/null | grep -q 'no'"

echo ""
echo "=== Products ==="
run "product sync --all"          "$BLF" product sync --all
run "product ls"                  "$BLF" product ls
run "product ls --source epo"     "$BLF" product ls --source epo-bdds
run "product ls --format json"    "$BLF" product ls --format json
run "product ls --format csv"     "$BLF" product ls --format csv
run "product ls -q"               "$BLF" product ls -q

# Pick a product with files
PRODUCT=$("$BLF" product ls -q --source epo-bdds 2>/dev/null | head -1)
run "product show"                "$BLF" product show "$PRODUCT"
run "product show --format json"  "$BLF" product show "$PRODUCT" --format json
run_fail "product show nonexistent" "$BLF" product show nonexistent

run "product enable"              "$BLF" product enable "$PRODUCT" --schedule "0 6 * * *"
run "product disable"             "$BLF" product disable "$PRODUCT"

echo ""
echo "=== Files ==="
run "file ls"                     "$BLF" file ls --limit 5
run "file ls --product"           "$BLF" file ls --product "$PRODUCT" --limit 5
run "file ls --source"            "$BLF" file ls --source epo-bdds --limit 5
run "file ls --format json"       "$BLF" file ls --limit 3 --format json
run "file ls --format csv"        "$BLF" file ls --limit 3 --format csv
run "file ls -q"                  "$BLF" file ls --limit 3 -q

# Get a small file ID for download testing (samples product)
SAMPLES=$("$BLF" product ls -q --source epo-bdds 2>/dev/null | grep ":20$" || echo "")
if [ -n "$SAMPLES" ]; then
    FILE_ID=$("$BLF" file ls -q --product "$SAMPLES" --status available --limit 1 2>/dev/null | head -1)
else
    FILE_ID=$("$BLF" file ls -q --source epo-bdds --status available --limit 1 2>/dev/null | head -1)
fi

if [ -n "$FILE_ID" ]; then
    run "file show"               "$BLF" file show "$FILE_ID"
    run "file show --format json" "$BLF" file show "$FILE_ID" --format json
    run "file download"           "$BLF" file download "$FILE_ID"
    run "file ls --status downloaded" "$BLF" file ls --status downloaded --limit 1
    run "file reset"              "$BLF" file reset "$FILE_ID"
    run "file skip"               "$BLF" file skip "$FILE_ID"
    run "file unskip"             "$BLF" file unskip "$FILE_ID"
    run "file download (again)"   "$BLF" file download "$FILE_ID"
    run "file delete"             "$BLF" file delete "$FILE_ID"
else
    echo "  SKIP: no available file found for download tests"
fi

run_fail "file show nonexistent"  "$BLF" file show nonexistent
run_fail "file cancel nonexistent" "$BLF" file cancel nonexistent
run_fail "file skip nonexistent"  "$BLF" file skip nonexistent

echo ""
echo "=== Downloads ==="
run "download history"            "$BLF" download history
run "download history --format json" "$BLF" download history --format json
run "download history --limit 1"  "$BLF" download history --limit 1

echo ""
echo "=== Webhooks ==="
run "webhook add"                 "$BLF" webhook add testbook https://example.com/hook --events download.completed,download.failed
run "webhook ls"                  "$BLF" webhook ls
run "webhook ls --format json"    "$BLF" webhook ls --format json
run "webhook update"              "$BLF" webhook update 1 --enabled=false
run "webhook rm"                  "$BLF" webhook rm 1
run "webhook ls (empty)"          "$BLF" webhook ls

echo ""
echo "=== Completion ==="
run "completion bash"             "$BLF" completion bash
run "completion zsh"              "$BLF" completion zsh

echo ""
echo "=== Serve ==="
run "serve --help"                "$BLF" serve --help

echo ""
echo "==============================="
echo "Results: $PASS passed, $FAIL failed"
if [ $FAIL -gt 0 ]; then
    echo -e "\nFailures:$ERRORS"
    exit 1
fi
echo "All tests passed."
