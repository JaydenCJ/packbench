#!/usr/bin/env bash
# The "am I overpaying for storage?" workflow: benchmark a directory,
# write a commit-ready JSON report, and print the cost table at S3
# Standard pricing ($0.023/GiB-month). Pass your own directory as $1;
# without one it generates a sample corpus first.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$(mktemp -d)/packbench"
(cd "$ROOT" && go build -o "$BIN" ./cmd/packbench)

DATA="${1:-}"
if [ -z "$DATA" ]; then
  DATA="$(bash "$ROOT/examples/make-sample-data.sh" | tail -1)"
fi

echo "== cost table (sorted by monthly bill) =="
"$BIN" run --price 0.023 --sort cost "$DATA"

echo
echo "== deterministic JSON report -> packbench-report.json =="
"$BIN" run --price 0.023 --no-timing --seed 7 --format json \
  --out packbench-report.json "$DATA"
echo "wrote packbench-report.json (commit it; re-run and diff when your data drifts)"
