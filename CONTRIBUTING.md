# Contributing to packbench

Issues, discussions and pull requests are all welcome.

## Getting started

You need Go ≥1.22; nothing else — the tool and its tests are pure
standard library and never touch the network. The external codecs
(zstd, xz, bzip2, lz4, brotli) are optional at runtime and the test
suite fakes them with local shell scripts, so it passes on a machine
with none of them installed.

```bash
git clone https://github.com/JaydenCJ/packbench && cd packbench
go build ./...
go test ./...
bash scripts/smoke.sh
```

`scripts/smoke.sh` builds the binary, generates a realistic mixed corpus
in a temp dir, and asserts on real CLI output and exit codes across
every subcommand and output format; it must finish by printing `SMOKE OK`.

## Before you open a pull request

1. `gofmt -l .` reports nothing (formatting is enforced).
2. `go vet ./...` passes with no findings.
3. `go test ./...` passes (91 deterministic tests, no network).
4. `bash scripts/smoke.sh` prints `SMOKE OK`.
5. Add tests for behavior changes; keep logic in pure, unit-testable
   modules (bench math, spec resolution, and rendering never touch the
   clock, PATH, or filesystem directly — those are injected).

## Ground rules

- Keep dependencies at zero; adding one needs strong justification in
  the PR.
- No network calls, ever, and no telemetry — packbench only reads the
  paths you point it at and writes the report you asked for.
- Benchmarks must stay honest: one thread per external codec (`-T1`
  wherever a tool would auto-parallelize), round-trip verification on
  by default, and the store baseline always available for comparison.
- Determinism first: with `--no-timing`, identical input and flags must
  produce byte-identical output — reports get committed to repos.
- New codec families need a catalogue entry, a level-range check, a
  round-trip test, and a fake-binary failure test; never let one broken
  codec abort the run.
- Code comments and doc comments are written in English.

## Reporting bugs

Include the output of `packbench version`, the full command you ran,
the report header line (it carries the corpus size, sample size, mode,
and seed), and `packbench codecs` output so we can see which external
binaries were in play.

## Security

Please do not open public issues for security problems; use GitHub's
private vulnerability reporting on this repository instead.
