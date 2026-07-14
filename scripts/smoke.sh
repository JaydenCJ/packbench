#!/usr/bin/env bash
# End-to-end smoke test for packbench: builds the binary, generates a
# realistic mixed corpus in a temp dir, and asserts on real CLI output
# and exit codes across every subcommand and format. No network,
# idempotent, finishes in seconds.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

fail() {
  echo "SMOKE FAIL: $*" >&2
  exit 1
}

BIN="$WORKDIR/packbench"

echo "1. build"
(cd "$ROOT" && go build -o "$BIN" ./cmd/packbench) || fail "go build failed"

echo "2. version matches manifest"
# Capture instead of piping the binary straight into grep -q: an early
# grep exit would EPIPE the writer and trip pipefail (same below).
[ "$("$BIN" --version)" = "packbench 0.1.0" ] || fail "--version mismatch"

echo "3. generate a mixed corpus (compressible logs + incompressible blob)"
DATA="$WORKDIR/data"
mkdir -p "$DATA/logs" "$DATA/.git/objects"
for i in $(seq 1 400); do
  echo "2026-07-01T12:00:0${i}Z INFO request served path=/api/v1/items status=200 bytes=1234"
done > "$DATA/logs/app.log"
head -c 8192 /dev/urandom > "$DATA/blob.bin" 2>/dev/null || \
  seq 1 100000 | gzip -c | head -c 8192 > "$DATA/blob.bin"
echo "packfile-noise" > "$DATA/.git/objects/pack"

echo "4. table report on the built-in codec set"
OUT="$("$BIN" run --no-external "$DATA")"
echo "$OUT" | grep -q "2 files scanned" || fail ".git was not pruned from the scan"
for id in store gzip:1 gzip:6 gzip:9 lzw; do
  echo "$OUT" | grep -q "$id" || fail "codec $id missing from the table"
done
echo "$OUT" | grep -q "best ratio" || fail "recommendations missing"

echo "5. ratios are honest: gzip crushes logs, store stays 1.0000"
LOGS="$("$BIN" run --no-external --include '*.log' --sort ratio --format csv "$DATA")"
echo "$LOGS" | grep -q "^store,store,0,builtin,.*,1.0000,0.0," || fail "store baseline is not ratio 1"
GZ9_RATIO="$(echo "$LOGS" | awk -F, '$1=="gzip:9"{print $7}')"
awk "BEGIN{exit !($GZ9_RATIO < 0.2)}" || fail "gzip:9 ratio $GZ9_RATIO on repetitive logs (want < 0.2)"

echo "6. JSON report is schema-versioned and verified"
JSON="$("$BIN" run --no-external --format json "$DATA")"
echo "$JSON" | grep -q '"schema_version": 1' || fail "schema_version missing"
echo "$JSON" | grep -q '"tool": "packbench"' || fail "tool field missing"
echo "$JSON" | grep -q '"verified": true' || fail "round-trip verification missing"
echo "$JSON" | grep -q '"recommendations"' || fail "recommendations missing from JSON"

echo "7. cost projection prices the corpus"
COST="$("$BIN" run --no-external --price 0.023 "$DATA")"
echo "$COST" | grep -q "USD/MO" || fail "cost columns missing"
echo "$COST" | grep -q '\$0.0230 per GiB-month' || fail "price line missing"

echo "8. deterministic mode: two runs are byte-identical"
"$BIN" run --no-external --no-timing --format json --out "$WORKDIR/r1.json" "$DATA"
"$BIN" run --no-external --no-timing --format json --out "$WORKDIR/r2.json" "$DATA"
cmp -s "$WORKDIR/r1.json" "$WORKDIR/r2.json" || fail "deterministic reports diverged"
if grep -q "compress_mbps" "$WORKDIR/r1.json"; then
  fail "--no-timing leaked throughput fields"
fi

echo "9. markdown report is a pipe table"
MD="$("$BIN" run --no-external --format md "$DATA")"
case "$MD" in "| CODEC |"*) ;; *) fail "md header wrong" ;; esac

echo "10. codecs subcommand lists the catalogue"
CAT="$("$BIN" codecs --no-external)"
echo "$CAT" | grep -q "^store" || fail "catalogue missing store"
echo "$CAT" | grep -q "not found" || fail "external codecs not marked unavailable"

echo "11. explicit levels and sampling budgets"
LEVELS="$("$BIN" run --no-external --codecs gzip:1-3 --max-bytes 4KiB --seed 7 --format csv "$DATA")"
echo "$LEVELS" | grep -q "^gzip:2," || fail "level range did not expand"

echo "12. usage errors exit 2"
set +e
"$BIN" run >/dev/null 2>&1
[ $? -eq 2 ] || fail "run without paths should exit 2"
"$BIN" run --codecs snappy "$DATA" >/dev/null 2>&1
[ $? -eq 2 ] || fail "unknown codec should exit 2"
"$BIN" frobnicate >/dev/null 2>&1
[ $? -eq 2 ] || fail "unknown command should exit 2"
"$BIN" run --no-external "$WORKDIR/does-not-exist" >/dev/null 2>&1
[ $? -eq 3 ] || fail "missing path should exit 3"
set -e

echo "13. a broken external codec is reported, exit 1, others still measured"
FAKEBINS="$WORKDIR/bins"
mkdir -p "$FAKEBINS"
printf '#!/bin/sh\necho "corrupt install" >&2\nexit 1\n' > "$FAKEBINS/zstd"
chmod +x "$FAKEBINS/zstd"
set +e
BROKEN="$(PATH="$FAKEBINS" "$BIN" run --codecs zstd:3,gzip:6 "$DATA" 2>/dev/null)"
CODE=$?
set -e
[ "$CODE" -eq 1 ] || fail "broken codec should exit 1, got $CODE"
echo "$BROKEN" | grep -q "failed        zstd:3" || fail "broken codec not reported"
echo "$BROKEN" | grep -q "best ratio    gzip:6" || fail "healthy codec dropped from report"

echo "SMOKE OK"
