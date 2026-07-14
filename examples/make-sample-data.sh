#!/usr/bin/env bash
# Generates a small mixed corpus for trying packbench flags: repetitive
# service logs, semi-structured NDJSON events, and one incompressible
# binary blob. Prints the corpus path on the last line.
set -euo pipefail

DEST="${1:-$(mktemp -d)/sample-data}"
mkdir -p "$DEST/logs" "$DEST/exports"

# Service logs: highly regular, the classic compression win.
awk 'BEGIN{
  for (i = 0; i < 20000; i++)
    printf "2026-07-%02dT%02d:%02d:%02dZ INFO request served path=/api/v1/items/%d status=200 latency_ms=%d bytes=%d\n",
      i % 28 + 1, i % 24, int(i / 60) % 60, i % 60, i % 9973, i % 180 + 3, i % 4096 + 200
}' > "$DEST/logs/app.log"

# NDJSON events: structured keys repeat, values vary.
awk 'BEGIN{
  for (i = 0; i < 8000; i++)
    printf "{\"event\":\"page_view\",\"user\":%d,\"session\":\"s-%07d\",\"ts\":\"2026-07-%02dT12:00:00Z\",\"props\":{\"path\":\"/product/%d\",\"ref\":\"search\"}}\n",
      i * 31 % 100000, i * 7919 % 9999999, i % 28 + 1, i % 5000
}' > "$DEST/exports/events.ndjson"

# Already-compressed media stands in for the incompressible tail every
# real bucket has; measuring it is the point (you want to see ~1.0).
head -c 262144 /dev/urandom > "$DEST/media.bin" 2>/dev/null ||
  seq 1 300000 | gzip -c | head -c 262144 > "$DEST/media.bin"

echo "corpus ready:"
du -sh "$DEST"/* | sed 's/^/  /'
echo "$DEST"
