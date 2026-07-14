# packbench examples

Both scripts are self-contained and offline, and neither touches your
data: sample corpora are generated under a temp directory.

- **`make-sample-data.sh`** — generates a small mixed corpus (repetitive
  service logs, NDJSON events, an incompressible binary) and prints the
  path. Useful for trying flags without pointing packbench at real data.
- **`storage-audit.sh`** — the "am I overpaying for storage?" workflow:
  benchmarks a directory (first argument, defaults to a generated
  sample), writes a commit-ready `packbench-report.json`, and prints
  the cost table at S3 Standard pricing plus the headline picks.

Run them from anywhere:

```bash
bash examples/make-sample-data.sh
bash examples/storage-audit.sh /var/log
```

Typical follow-ups once you have a report:

```bash
# Pin the winner across the levels you would actually deploy
packbench run --codecs zstd:1-9 --sort saved /var/log

# Model tar-then-compress instead of per-object compression
packbench run --concat --codecs zstd:3,gzip:6 /var/log

# Commit a deterministic report and diff it in CI when data drifts
packbench run --no-timing --format json --seed 7 --out report.json /var/log
```
