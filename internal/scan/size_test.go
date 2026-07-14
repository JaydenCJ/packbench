// Size-string parsing and formatting. These strings appear on the CLI
// (--max-bytes, --min-size) and in every report header, so both
// directions get exercised at each unit boundary.
package scan

import "testing"

func TestParseSizeAcceptsEveryUnitConvention(t *testing.T) {
	for in, want := range map[string]int64{
		"2048": 2048, // bare numbers are bytes
		"512b": 512,
		// Decimal units are powers of 1000.
		"1kb": 1000, "1mb": 1000_000, "1gb": 1000_000_000, "2tb": 2_000_000_000_000,
		// Binary units are powers of 1024.
		"1KiB": 1024, "64MiB": 64 << 20, "1GiB": 1 << 30, "1TiB": 1 << 40,
		// Short suffixes follow the classic Unix convention: -k is 1024.
		"4k": 4096, "2m": 2 << 20, "1g": 1 << 30, "1t": 1 << 40,
		// Fractions, mixed case, stray whitespace, and the exact string
		// FormatSize renders ("1.5 KiB") all parse — users copy report
		// values straight back into --max-bytes.
		"1.5KiB": 1536, "1.5 KiB": 1536, "  64 mIb ": 64 << 20,
	} {
		n, err := ParseSize(in)
		if err != nil || n != want {
			t.Fatalf("%q: got %d, %v; want %d", in, n, err, want)
		}
	}
}

func TestParseSizeRejectsMalformedInput(t *testing.T) {
	for _, in := range []string{
		"   ",   // empty
		"MiB",   // suffix with no number
		"-5MB",  // negative
		"lots",  // not a number at all
		"1.2.3", // two decimal points
	} {
		if _, err := ParseSize(in); err == nil {
			t.Fatalf("%q: expected an error", in)
		}
	}
}

func TestFormatSizeBoundaries(t *testing.T) {
	for in, want := range map[int64]string{
		0:        "0 B",
		512:      "512 B",
		1023:     "1023 B", // stays exact below 1 KiB
		1536:     "1.5 KiB",
		64 << 20: "64.0 MiB",
		3 << 30:  "3.0 GiB",
		2 << 40:  "2.0 TiB",
	} {
		if got := FormatSize(in); got != want {
			t.Fatalf("%d: got %q, want %q", in, got, want)
		}
	}
}
