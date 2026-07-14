# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-13

### Added

- Codec catalogue with ten families: five compiled in from the Go
  standard library (store baseline, gzip, zlib, flate, lzw) and five
  driven through their own binaries when found on PATH (zstd, xz,
  bzip2, lz4, brotli), every invocation held to a single thread (`-T1`
  where the tool would auto-parallelize) so speed numbers compare one
  core against one core.
- `--codecs` spec language: `auto` (default; everything this machine
  has, at curated levels), `all`, and explicit comma lists with level
  ranges (`gzip:1-9,zstd:3,lzw`); asking for an absent binary fails
  loudly instead of silently skipping it.
- Corpus scanner with include/exclude globs, a min-size floor,
  unconditional VCS-directory pruning, symlink skipping, and a seeded,
  budget-bounded sample (`--max-bytes`, `--max-files`, `--seed`) so
  multi-TiB trees are measured in seconds and the same seed draws the
  same sample forever.
- Bench runner with round-trip verification on by default (decompress
  and byte-compare, so a lying codec is flagged, not trusted),
  per-file and `--concat` solid-archive modes, and per-codec failure
  isolation — one broken binary never aborts the report.
- Report renderer in four formats (aligned table, GitHub Markdown,
  CSV, `schema_version: 1` JSON) with six sort keys, three headline
  recommendations (best ratio, fastest, balanced = most bytes shed per
  CPU-second), and byte-identical output under `--no-timing`.
- Storage-cost projection (`--price`, USD per GiB-month): sample
  ratios extrapolated onto the full scanned corpus, with monthly and
  yearly raw bills plus per-codec monthly cost and saving columns.
- `codecs` subcommand printing the catalogue with level ranges,
  defaults, auto levels, and each resolved binary path.
- Documented exit codes (0 ok, 1 codec failure, 2 usage, 3 runtime),
  runnable examples (`examples/make-sample-data.sh`,
  `examples/storage-audit.sh`), and a JSON schema reference
  (`docs/report-format.md`).
- 91 deterministic offline tests (injected clock, injected PATH
  lookups, fake shell-script codecs) and `scripts/smoke.sh`.

[0.1.0]: https://github.com/JaydenCJ/packbench/releases/tag/v0.1.0
