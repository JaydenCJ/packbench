// Package codec defines the codec families packbench can benchmark:
// the four standard-library codecs (plus a "store" baseline) compiled
// into the binary, and five external codecs driven through their own
// battle-tested binaries when they are on PATH. Every codec is a pure
// []byte -> []byte pair, so the bench layer never cares which is which.
package codec

import (
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Family describes one codec family and its level range.
type Family struct {
	Name    string
	Min     int
	Max     int
	Default int
	Leveled bool
	Bin     string // external binary name; empty means built in
	Auto    []int  // levels the auto/all sets benchmark
	Note    string // one-line description for `packbench codecs`
}

// Families is the ordered catalogue. Order here is report order.
var Families = []Family{
	{Name: "store", Auto: []int{0}, Note: "no compression; the baseline every row is judged against"},
	{Name: "gzip", Min: 1, Max: 9, Default: 6, Leveled: true, Auto: []int{1, 6, 9},
		Note: "DEFLATE in the ubiquitous gzip container (compress/gzip)"},
	{Name: "zlib", Min: 1, Max: 9, Default: 6, Leveled: true, Auto: []int{6},
		Note: "DEFLATE in the 6-byte zlib container (compress/zlib)"},
	{Name: "flate", Min: 1, Max: 9, Default: 6, Leveled: true, Auto: []int{6},
		Note: "raw DEFLATE stream, no container (compress/flate)"},
	{Name: "lzw", Auto: []int{0}, Note: "LZW as used by Unix compress (compress/lzw)"},
	{Name: "zstd", Min: 1, Max: 19, Default: 3, Leveled: true, Bin: "zstd", Auto: []int{3, 19},
		Note: "Zstandard via the zstd binary"},
	{Name: "xz", Min: 0, Max: 9, Default: 6, Leveled: true, Bin: "xz", Auto: []int{6},
		Note: "LZMA2 via the xz binary (single-threaded for fair timing)"},
	{Name: "bzip2", Min: 1, Max: 9, Default: 9, Leveled: true, Bin: "bzip2", Auto: []int{9},
		Note: "Burrows-Wheeler via the bzip2 binary"},
	{Name: "lz4", Min: 1, Max: 12, Default: 1, Leveled: true, Bin: "lz4", Auto: []int{1, 9},
		Note: "LZ4 via the lz4 binary"},
	{Name: "brotli", Min: 0, Max: 11, Default: 6, Leveled: true, Bin: "brotli", Auto: []int{6, 11},
		Note: "Brotli via the brotli binary"},
}

func familyByName(name string) (Family, bool) {
	for _, f := range Families {
		if f.Name == name {
			return f, true
		}
	}
	return Family{}, false
}

// Codec is one benchmarkable (family, level) pair.
type Codec struct {
	Family Family
	Level  int
	path   string // resolved binary path; external codecs only
}

// ID is the stable identifier used in reports and --codecs specs,
// e.g. "gzip:6", "lzw", "zstd:19".
func (c Codec) ID() string {
	if !c.Family.Leveled {
		return c.Family.Name
	}
	return fmt.Sprintf("%s:%d", c.Family.Name, c.Level)
}

// Source reports where the implementation lives: "builtin" or the
// external binary name.
func (c Codec) Source() string {
	if c.Family.Bin == "" {
		return "builtin"
	}
	return c.Family.Bin + " bin"
}

// Compress encodes src with this codec.
func (c Codec) Compress(src []byte) ([]byte, error) {
	if c.Family.Bin != "" {
		return runExternal(c.path, extArgs(c.Family, c.Level, false), src)
	}
	return builtinCompress(c.Family.Name, c.Level, src)
}

// Decompress decodes data produced by Compress.
func (c Codec) Decompress(src []byte) ([]byte, error) {
	if c.Family.Bin != "" {
		return runExternal(c.path, extArgs(c.Family, c.Level, true), src)
	}
	return builtinDecompress(c.Family.Name, src)
}

// Registry resolves codec specs against what this machine actually has.
// LookPath is injectable so tests never depend on the host's PATH.
type Registry struct {
	LookPath        func(string) (string, error)
	DisableExternal bool

	cache map[string]string // binary name -> resolved path ("" = absent)
}

func (r *Registry) lookPath(bin string) (string, bool) {
	if r.DisableExternal {
		return "", false
	}
	if r.cache == nil {
		r.cache = map[string]string{}
	}
	if p, ok := r.cache[bin]; ok {
		return p, p != ""
	}
	look := r.LookPath
	if look == nil {
		look = exec.LookPath
	}
	p, err := look(bin)
	if err != nil {
		p = ""
	}
	r.cache[bin] = p
	return p, p != ""
}

// available reports whether a family can run here, and its binary path.
func (r *Registry) available(f Family) (string, bool) {
	if f.Bin == "" {
		return "", true
	}
	return r.lookPath(f.Bin)
}

// Status is one row of `packbench codecs`.
type Status struct {
	Family    Family
	Available bool
	Path      string
}

// Statuses lists every family with its availability, in catalogue order.
func (r *Registry) Statuses() []Status {
	out := make([]Status, 0, len(Families))
	for _, f := range Families {
		p, ok := r.available(f)
		out = append(out, Status{Family: f, Available: ok, Path: p})
	}
	return out
}

// Resolve turns a --codecs spec into a concrete codec list.
//
//	"auto"  store + gzip:1/6/9 + lzw + every detected external at its
//	        Auto levels — the default, and never an error.
//	"all"   every available family at its Auto levels (adds zlib, flate).
//	else    comma-separated entries: name, name:LEVEL, or name:LO-HI.
//	        Explicitly requesting an absent external codec is an error;
//	        auto/all silently skip what the machine does not have.
func (r *Registry) Resolve(spec string) ([]Codec, error) {
	spec = strings.TrimSpace(spec)
	switch spec {
	case "", "auto":
		return r.autoSet(map[string]bool{"zlib": true, "flate": true}), nil
	case "all":
		return r.autoSet(nil), nil
	}
	var out []Codec
	seen := map[string]bool{}
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		cs, err := r.resolveEntry(entry)
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			if !seen[c.ID()] {
				seen[c.ID()] = true
				out = append(out, c)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--codecs %q selects no codecs", spec)
	}
	sortCodecs(out)
	return out, nil
}

func (r *Registry) autoSet(skip map[string]bool) []Codec {
	var out []Codec
	for _, f := range Families {
		if skip[f.Name] {
			continue
		}
		path, ok := r.available(f)
		if !ok {
			continue
		}
		for _, lvl := range f.Auto {
			out = append(out, Codec{Family: f, Level: lvl, path: path})
		}
	}
	return out
}

func (r *Registry) resolveEntry(entry string) ([]Codec, error) {
	name, levels, hasLevel := strings.Cut(entry, ":")
	f, ok := familyByName(name)
	if !ok {
		return nil, fmt.Errorf("unknown codec %q (run `packbench codecs` for the catalogue)", name)
	}
	path, avail := r.available(f)
	if !avail {
		reason := f.Bin + " not found on PATH"
		if r.DisableExternal {
			reason = "external codecs disabled by --no-external"
		}
		return nil, fmt.Errorf("codec %q requested but %s", name, reason)
	}
	if !hasLevel {
		return []Codec{{Family: f, Level: f.Default, path: path}}, nil
	}
	if !f.Leveled {
		return nil, fmt.Errorf("codec %q has no levels; use it bare", name)
	}
	lo, hi, err := parseLevels(levels)
	if err != nil {
		return nil, fmt.Errorf("codec %q: %w", name, err)
	}
	if lo < f.Min || hi > f.Max {
		return nil, fmt.Errorf("codec %q supports levels %d-%d, got %s", name, f.Min, f.Max, levels)
	}
	var out []Codec
	for lvl := lo; lvl <= hi; lvl++ {
		out = append(out, Codec{Family: f, Level: lvl, path: path})
	}
	return out, nil
}

func parseLevels(s string) (lo, hi int, err error) {
	loS, hiS, isRange := strings.Cut(s, "-")
	if lo, err = strconv.Atoi(loS); err != nil {
		return 0, 0, fmt.Errorf("invalid level %q", s)
	}
	if !isRange {
		return lo, lo, nil
	}
	if hi, err = strconv.Atoi(hiS); err != nil {
		return 0, 0, fmt.Errorf("invalid level range %q", s)
	}
	if hi < lo {
		return 0, 0, fmt.Errorf("level range %q is backwards", s)
	}
	return lo, hi, nil
}

// sortCodecs orders by catalogue position, then level ascending, so
// explicit specs render in the same stable order as auto.
func sortCodecs(cs []Codec) {
	pos := map[string]int{}
	for i, f := range Families {
		pos[f.Name] = i
	}
	sort.SliceStable(cs, func(i, j int) bool {
		pi, pj := pos[cs[i].Family.Name], pos[cs[j].Family.Name]
		if pi != pj {
			return pi < pj
		}
		return cs[i].Level < cs[j].Level
	})
}
