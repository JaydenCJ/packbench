# The packbench JSON report (`schema_version: 1`)

`packbench run --format json` emits one JSON document. The schema is
versioned and additive: fields may be added within `schema_version: 1`,
never renamed or removed. With `--no-timing`, the document is
byte-identical for identical input, flags, and seed — safe to commit
and diff in CI.

## Envelope

```json
{
  "schema_version": 1,
  "tool": "packbench",
  "version": "0.1.0",
  "mode": "per-file",
  "corpus": { ... },
  "results": [ ... ],
  "cost": { ... },
  "recommendations": { ... }
}
```

| Key | Type | Meaning |
|---|---|---|
| `schema_version` | int | always `1` for this document shape |
| `tool` | string | always `"packbench"` |
| `version` | string | the packbench build that produced the report |
| `mode` | string | `"per-file"` (each file compressed alone) or `"concat"` (one solid archive) |
| `cost` | object? | present only when `--price` was given |
| `recommendations` | object? | present only when at least one non-store codec ran without error |

## `corpus`

What was scanned versus what was actually measured. Ratios are computed
on the sample; cost is projected onto the full scanned byte count.

| Key | Type | Meaning |
|---|---|---|
| `paths` | string[] | the positional path arguments, verbatim |
| `files_scanned` | int | files that matched the filters |
| `bytes_scanned` | int | their total size — the projection base |
| `files_sampled` | int | files the seeded sample selected |
| `bytes_sampled` | int | bytes actually read and benchmarked |
| `truncated` | bool | true when the byte budget cut the last file short |
| `seed` | int | the sampling seed; same seed, same sample |

## `results[]`

One entry per (codec, level), pre-sorted by the `--sort` key with
errored rows last; every tie breaks on `id` so order is stable.

| Key | Type | Meaning |
|---|---|---|
| `id` | string | stable identifier, e.g. `"zstd:19"`, `"lzw"` |
| `codec` / `level` | string / int | the family and level separately |
| `source` | string | `"builtin"` or the binary driving it, e.g. `"zstd bin"` |
| `in_bytes` / `out_bytes` | int | sample bytes in, compressed bytes out |
| `ratio` | float | `out/in`, 4 decimals; smaller is better, store is 1.0 |
| `saved_pct` | float | `(1 - ratio) * 100`, 1 decimal |
| `compress_mbps` | float? | decimal MB/s of input; absent under `--no-timing` or on error |
| `decompress_mbps` | float? | decimal MB/s of output; absent when `--no-verify` skipped decompression |
| `monthly_usd` / `monthly_saving_usd` | float? | present only with `--price` |
| `verified` | bool | decompressed bytes matched the input exactly; always `false` under `--no-verify` (the check did not run) |
| `error` | string? | why this codec produced no numbers; the run still exits 1 |

## `cost`

| Key | Type | Meaning |
|---|---|---|
| `price_usd_per_gib_month` | float | the `--price` you passed |
| `projected_on_bytes` | int | equals `corpus.bytes_scanned` |
| `raw_monthly_usd` / `raw_yearly_usd` | float | the bill for storing the corpus uncompressed |

Prices are per **GiB**-month (2^30 bytes) because storage vendors bill
binary gigabytes even when the price sheet says "GB".

## `recommendations`

| Key | Meaning |
|---|---|
| `best_ratio` | smallest ratio among error-free, non-store rows (verification failures carry an error and never win) |
| `fastest` | highest compress MB/s (absent under `--no-timing`) |
| `balanced` | most bytes shed per CPU-second of compression — usually the codec worth deploying |

## Exit codes

| Code | Meaning |
|---|---|
| 0 | every selected codec ran and verified |
| 1 | at least one codec errored or failed round-trip verification (report still printed) |
| 2 | usage error: bad flag, bad spec, no paths |
| 3 | runtime error: unreadable path, unwritable `--out` |
