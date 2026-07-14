// Codec catalogue, spec resolution, and built-in round-trips. External
// availability is faked through Registry.LookPath, so nothing here ever
// depends on what the host machine has installed.
package codec

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// noBins is a Registry on a machine with no external codecs at all.
func noBins() *Registry {
	return &Registry{LookPath: func(string) (string, error) { return "", errors.New("absent") }}
}

// allBins is a Registry where every external binary resolves.
func allBins() *Registry {
	return &Registry{LookPath: func(bin string) (string, error) { return "/opt/fake/" + bin, nil }}
}

func ids(cs []Codec) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID()
	}
	return out
}

func sample() []byte {
	return bytes.Repeat([]byte("packbench measures your data, not a synthetic corpus. "), 40)
}

func TestBuiltinRoundTrips(t *testing.T) {
	src := sample()
	for _, name := range []string{"store", "gzip", "zlib", "flate", "lzw"} {
		f, ok := familyByName(name)
		if !ok {
			t.Fatalf("family %q missing from catalogue", name)
		}
		c := Codec{Family: f, Level: f.Default}
		packed, err := c.Compress(src)
		if err != nil {
			t.Fatalf("%s compress: %v", name, err)
		}
		back, err := c.Decompress(packed)
		if err != nil {
			t.Fatalf("%s decompress: %v", name, err)
		}
		if !bytes.Equal(back, src) {
			t.Fatalf("%s round-trip mismatch", name)
		}
		// Zero-byte files exist in every real corpus; codecs must not choke.
		packed, err = c.Compress(nil)
		if err != nil {
			t.Fatalf("%s compress empty: %v", name, err)
		}
		back, err = c.Decompress(packed)
		if err != nil || len(back) != 0 {
			t.Fatalf("%s empty round-trip: %d bytes, %v", name, len(back), err)
		}
	}
}

func TestBuiltinCompressActuallyShrinksRedundantData(t *testing.T) {
	src := sample()
	f, _ := familyByName("gzip")
	packed, err := Codec{Family: f, Level: 6}.Compress(src)
	if err != nil || len(packed) >= len(src) {
		t.Fatalf("gzip:6 did not shrink %d -> %d (%v)", len(src), len(packed), err)
	}
}

func TestBuiltinRejectsUnknownName(t *testing.T) {
	if _, err := builtinCompress("snappy", 1, nil); err == nil {
		t.Fatal("expected an error for an unknown builtin")
	}
	if _, err := builtinDecompress("snappy", nil); err == nil {
		t.Fatal("expected an error for an unknown builtin")
	}
}

func TestStoreBaselineIsByteExactCopy(t *testing.T) {
	src := []byte{0, 1, 2, 254, 255}
	f, _ := familyByName("store")
	packed, err := Codec{Family: f}.Compress(src)
	if err != nil || !bytes.Equal(packed, src) {
		t.Fatalf("store must copy verbatim: %v (%v)", packed, err)
	}
	packed[0] = 99
	if src[0] == 99 {
		t.Fatal("store must copy, not alias, the input")
	}
}

func TestIDAndSourceLabels(t *testing.T) {
	gz, _ := familyByName("gzip")
	lz, _ := familyByName("lzw")
	zs, _ := familyByName("zstd")
	if got := (Codec{Family: gz, Level: 6}).ID(); got != "gzip:6" {
		t.Fatalf("got %q", got)
	}
	if got := (Codec{Family: lz}).ID(); got != "lzw" {
		t.Fatalf("unleveled codecs use the bare name, got %q", got)
	}
	if got := (Codec{Family: gz}).Source(); got != "builtin" {
		t.Fatalf("got %q", got)
	}
	if got := (Codec{Family: zs}).Source(); got != "zstd bin" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveAutoWithNoExternals(t *testing.T) {
	cs, err := noBins().Resolve("auto")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"store", "gzip:1", "gzip:6", "gzip:9", "lzw"}
	if strings.Join(ids(cs), " ") != strings.Join(want, " ") {
		t.Fatalf("got %v, want %v", ids(cs), want)
	}
	// The empty spec is the CLI default and must mean exactly "auto".
	blank, err := noBins().Resolve("")
	if err != nil || strings.Join(ids(blank), " ") != strings.Join(want, " ") {
		t.Fatalf("empty spec: %v, %v", ids(blank), err)
	}
}

func TestResolveAutoAddsDetectedExternalsAtAutoLevels(t *testing.T) {
	cs, err := allBins().Resolve("auto")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(ids(cs), " ")
	for _, want := range []string{"zstd:3", "zstd:19", "xz:6", "bzip2:9", "lz4:1", "lz4:9", "brotli:6", "brotli:11"} {
		if !strings.Contains(got, want) {
			t.Fatalf("auto with all bins missing %s: %v", want, ids(cs))
		}
	}
}

func TestResolveAllIncludesZlibAndFlate(t *testing.T) {
	cs, err := noBins().Resolve("all")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(ids(cs), " ")
	if !strings.Contains(got, "zlib:6") || !strings.Contains(got, "flate:6") {
		t.Fatalf("all must add zlib and flate: %v", ids(cs))
	}
}

func TestResolveExplicitNameUsesDefaultLevel(t *testing.T) {
	cs, err := noBins().Resolve("gzip")
	if err != nil || len(cs) != 1 || cs[0].ID() != "gzip:6" {
		t.Fatalf("got %v, %v", ids(cs), err)
	}
}

func TestResolveLevelRangeExpands(t *testing.T) {
	cs, err := noBins().Resolve("gzip:3-5")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"gzip:3", "gzip:4", "gzip:5"}
	if strings.Join(ids(cs), " ") != strings.Join(want, " ") {
		t.Fatalf("got %v", ids(cs))
	}
}

func TestResolveDeduplicatesAndSortsCatalogueOrder(t *testing.T) {
	cs, err := noBins().Resolve("lzw,gzip:9,gzip:1,store,gzip:9")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"store", "gzip:1", "gzip:9", "lzw"}
	if strings.Join(ids(cs), " ") != strings.Join(want, " ") {
		t.Fatalf("got %v, want %v", ids(cs), want)
	}
}

func TestResolveRejectsBadSpecs(t *testing.T) {
	// Explicitly requesting an absent external codec fails loudly (auto
	// silently skips it) — a silent skip would misreport the winner.
	cases := []struct {
		spec     string
		disabled bool // use DisableExternal instead of an empty PATH
		want     string
	}{
		{spec: "snappy", want: "unknown codec"},
		{spec: "zstd:3", want: "not found on PATH"},
		{spec: "zstd", disabled: true, want: "--no-external"},
		{spec: "gzip:12", want: "levels 1-9"},
		{spec: "lzw:3", want: "no levels"},
		{spec: "gzip:9-1", want: "backwards"},
		{spec: "gzip:max", want: "invalid level"},
		{spec: " , ,", want: "selects no codecs"},
	}
	for _, tc := range cases {
		r := noBins()
		if tc.disabled {
			r = &Registry{DisableExternal: true}
		}
		_, err := r.Resolve(tc.spec)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%q: got %v, want %q", tc.spec, err, tc.want)
		}
	}
}

func TestResolveSkipsBlankEntriesBetweenCommas(t *testing.T) {
	cs, err := noBins().Resolve("gzip:6, ,lzw")
	if err != nil || len(cs) != 2 {
		t.Fatalf("got %v, %v", ids(cs), err)
	}
}

func TestRegistryCachesLookups(t *testing.T) {
	calls := 0
	r := &Registry{LookPath: func(bin string) (string, error) { calls++; return "/opt/fake/" + bin, nil }}
	r.Resolve("zstd:3")
	r.Resolve("zstd:19")
	if calls != 1 {
		t.Fatalf("LookPath called %d times for one binary", calls)
	}
}

func TestStatusesCoverWholeCatalogueInOrder(t *testing.T) {
	sts := allBins().Statuses()
	if len(sts) != len(Families) {
		t.Fatalf("got %d statuses for %d families", len(sts), len(Families))
	}
	for i, st := range sts {
		if st.Family.Name != Families[i].Name || !st.Available {
			t.Fatalf("status %d = %+v", i, st)
		}
	}
}

func TestCatalogueDefaultsWithinDeclaredRange(t *testing.T) {
	// A default outside [Min,Max] would make `packbench run --codecs name`
	// reject its own catalogue.
	for _, f := range Families {
		if !f.Leveled {
			continue
		}
		if f.Default < f.Min || f.Default > f.Max {
			t.Fatalf("%s default %d outside %d-%d", f.Name, f.Default, f.Min, f.Max)
		}
		for _, a := range f.Auto {
			if a < f.Min || a > f.Max {
				t.Fatalf("%s auto level %d outside %d-%d", f.Name, a, f.Min, f.Max)
			}
		}
	}
}
