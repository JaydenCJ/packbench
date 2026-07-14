package scan

import (
	"fmt"
	"strconv"
	"strings"
)

// Size units. Decimal units (KB, MB, …) are powers of 1000; binary units
// (KiB, MiB, …) are powers of 1024. Storage vendors bill in binary units
// while calling them "GB", so packbench prices per GiB and says so.
const (
	KB  int64 = 1000
	MB        = 1000 * KB
	GB        = 1000 * MB
	TB        = 1000 * GB
	KiB int64 = 1024
	MiB       = 1024 * KiB
	GiB       = 1024 * MiB
	TiB       = 1024 * GiB
)

var suffixes = []struct {
	name  string
	bytes int64
}{
	// Longest suffixes first so "MiB" is not matched as "B".
	{"kib", KiB}, {"mib", MiB}, {"gib", GiB}, {"tib", TiB},
	{"kb", KB}, {"mb", MB}, {"gb", GB}, {"tb", TB},
	{"k", KiB}, {"m", MiB}, {"g", GiB}, {"t", TiB},
	{"b", 1},
}

// ParseSize converts a human size string ("64MiB", "1.5GB", "2048") to
// bytes. Bare numbers are bytes; short suffixes (K/M/G/T) are binary,
// matching the convention of every classic Unix tool.
func ParseSize(s string) (int64, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	if t == "" {
		return 0, fmt.Errorf("empty size")
	}
	mult := int64(1)
	for _, suf := range suffixes {
		if strings.HasSuffix(t, suf.name) {
			mult = suf.bytes
			t = strings.TrimSpace(strings.TrimSuffix(t, suf.name))
			break
		}
	}
	if t == "" {
		return 0, fmt.Errorf("size %q has no number", s)
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return int64(f * float64(mult)), nil
}

// FormatSize renders bytes as a compact human string in binary units,
// e.g. 1536 -> "1.5 KiB". Values below 1 KiB stay exact ("512 B").
func FormatSize(n int64) string {
	switch {
	case n >= TiB:
		return fmt.Sprintf("%.1f TiB", float64(n)/float64(TiB))
	case n >= GiB:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(GiB))
	case n >= MiB:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(MiB))
	case n >= KiB:
		return fmt.Sprintf("%.1f KiB", float64(n)/float64(KiB))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
