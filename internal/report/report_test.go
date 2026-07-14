// Report assembly, ordering, recommendations, and all four renderers.
// Results are hand-built bench.Result values with fixed numbers, so
// every rendered byte asserted here is fully deterministic.
package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JaydenCJ/packbench/internal/bench"
	"github.com/JaydenCJ/packbench/internal/codec"
	"github.com/JaydenCJ/packbench/internal/scan"
)

// resolve builds concrete codecs without touching the host PATH.
func resolve(t *testing.T, spec string) []codec.Codec {
	t.Helper()
	r := &codec.Registry{DisableExternal: true}
	cs, err := r.Resolve(spec)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

// fixedResults models one store baseline and two gzip levels over a
// 1000-byte sample with exact, hand-picked timings.
func fixedResults(t *testing.T) []bench.Result {
	t.Helper()
	cs := resolve(t, "store,gzip:1,gzip:9")
	ms := int64(time.Millisecond)
	return []bench.Result{
		{Codec: cs[0], InBytes: 1000, OutBytes: 1000, CompressNS: 1 * ms, DecompressNS: 1 * ms, Verified: true},
		{Codec: cs[1], InBytes: 1000, OutBytes: 400, CompressNS: 2 * ms, DecompressNS: 1 * ms, Verified: true},
		{Codec: cs[2], InBytes: 1000, OutBytes: 300, CompressNS: 10 * ms, DecompressNS: 1 * ms, Verified: true},
	}
}

func corpus() *scan.Corpus {
	return &scan.Corpus{
		Files:        []scan.File{{Path: "data/app.log", Size: 4000, Take: 1000}},
		ScannedFiles: 4,
		ScannedBytes: 4000,
		SampledBytes: 1000,
		Truncated:    true,
	}
}

func opts() Options {
	return Options{Sort: "ratio", Timing: true, Seed: 7, Paths: []string{"data"}}
}

func rowIDs(r *Report) []string {
	out := make([]string, len(r.Results))
	for i, row := range r.Results {
		out[i] = row.ID
	}
	return out
}

func TestBuildEnvelopeFields(t *testing.T) {
	r := Build(corpus(), fixedResults(t), opts())
	if r.SchemaVersion != 1 || r.Tool != "packbench" || r.Version == "" {
		t.Fatalf("envelope %+v", r)
	}
	if r.Mode != "per-file" {
		t.Fatalf("mode %q", r.Mode)
	}
	c := r.Corpus
	if c.FilesScanned != 4 || c.BytesScanned != 4000 || c.FilesSampled != 1 ||
		c.BytesSampled != 1000 || !c.Truncated || c.Seed != 7 {
		t.Fatalf("corpus %+v", c)
	}
}

func TestBuildConcatMode(t *testing.T) {
	o := opts()
	o.Concat = true
	if r := Build(corpus(), fixedResults(t), o); r.Mode != "concat" {
		t.Fatalf("mode %q", r.Mode)
	}
}

func TestBuildRowMath(t *testing.T) {
	r := Build(corpus(), fixedResults(t), opts())
	var gz1 Row
	for _, row := range r.Results {
		if row.ID == "gzip:1" {
			gz1 = row
		}
	}
	if gz1.Ratio != 0.4 || gz1.SavedPct != 60 {
		t.Fatalf("ratio/saved: %+v", gz1)
	}
	// 1000 bytes in 2 ms = 0.5 MB/s; decompress 1000 bytes in 1 ms = 1 MB/s.
	if *gz1.CompMBps != 0.5 || *gz1.DecMBps != 1 {
		t.Fatalf("throughput: %v / %v", *gz1.CompMBps, *gz1.DecMBps)
	}
}

func TestSortByRatioDefaultPutsBestFirst(t *testing.T) {
	r := Build(corpus(), fixedResults(t), opts())
	want := "gzip:9 gzip:1 store"
	if got := strings.Join(rowIDs(r), " "); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSortKeysOrderAsDocumented(t *testing.T) {
	// comp: store copies fastest. name: lexicographic. cost: cheapest
	// bill first (100 GiB corpus keeps the rounded dollars distinct).
	cases := map[string]string{
		"comp": "store gzip:1 gzip:9",
		"name": "gzip:1 gzip:9 store",
		"cost": "gzip:9 gzip:1 store",
	}
	for key, want := range cases {
		o := opts()
		o.Sort = key
		if key == "cost" {
			o.Cost = true
			o.Price = 0.023
		}
		c := corpus()
		c.ScannedBytes = 100 << 30
		r := Build(c, fixedResults(t), o)
		if got := strings.Join(rowIDs(r), " "); got != want {
			t.Fatalf("sort=%s: got %q, want %q", key, got, want)
		}
	}
}

func TestErroredRowsSinkToTheBottom(t *testing.T) {
	results := fixedResults(t)
	results[2].Err = "boom"
	results[2].Verified = false
	r := Build(corpus(), results, opts())
	last := r.Results[len(r.Results)-1]
	if last.ID != "gzip:9" || last.Err != "boom" {
		t.Fatalf("got %v", rowIDs(r))
	}
}

func TestTieBreaksOnIDKeepOutputStable(t *testing.T) {
	cs := resolve(t, "gzip:1,gzip:2")
	results := []bench.Result{
		{Codec: cs[0], InBytes: 1000, OutBytes: 500, Verified: true},
		{Codec: cs[1], InBytes: 1000, OutBytes: 500, Verified: true},
	}
	r := Build(corpus(), results, opts())
	if rowIDs(r)[0] != "gzip:1" {
		t.Fatalf("equal ratios must tie-break on ID: %v", rowIDs(r))
	}
}

func TestRecommendationsPickTheRightWinners(t *testing.T) {
	r := Build(corpus(), fixedResults(t), opts())
	if r.Recs == nil {
		t.Fatal("no recommendations")
	}
	// gzip:9 has the best ratio; gzip:1 sheds the most bytes per second
	// (600B/2ms = 0.3 MB/s vs gzip:9's 700B/10ms = 0.07 MB/s); store is
	// the fastest compressor but is excluded, so gzip:1 wins that too.
	if r.Recs.BestRatio != "gzip:9" || r.Recs.Balanced != "gzip:1" || r.Recs.Fastest != "gzip:1" {
		t.Fatalf("recs %+v", r.Recs)
	}
}

func TestRecommendationsIgnoreStoreAndFailures(t *testing.T) {
	results := fixedResults(t)
	results[2].Err = "boom"
	results[2].Verified = false
	r := Build(corpus(), results, opts())
	if r.Recs.BestRatio != "gzip:1" {
		t.Fatalf("recs %+v", r.Recs)
	}
}

func TestNoRecommendationsWhenNothingQualifies(t *testing.T) {
	cs := resolve(t, "store")
	results := []bench.Result{{Codec: cs[0], InBytes: 10, OutBytes: 10, Verified: true}}
	if r := Build(corpus(), results, opts()); r.Recs != nil {
		t.Fatalf("store alone must yield no recs: %+v", r.Recs)
	}
}

func TestNoTimingDropsThroughputAndFastest(t *testing.T) {
	o := opts()
	o.Timing = false
	r := Build(corpus(), fixedResults(t), o)
	for _, row := range r.Results {
		if row.CompMBps != nil || row.DecMBps != nil {
			t.Fatalf("timing leaked into %+v", row)
		}
	}
	if r.Recs == nil || r.Recs.Fastest != "" || r.Recs.Balanced != "" || r.Recs.BestRatio != "gzip:9" {
		t.Fatalf("recs %+v", r.Recs)
	}
}

func TestCostProjectionExtrapolatesToTheFullCorpus(t *testing.T) {
	// The sample is 1000 bytes but the scanned corpus is 100 GiB: ratios
	// measured on the sample must be priced against the whole corpus.
	o := opts()
	o.Cost = true
	o.Price = 0.023 // S3 Standard
	c := corpus()
	c.ScannedBytes = 100 << 30
	r := Build(c, fixedResults(t), o)
	if r.Cost == nil || r.Cost.ProjectedOnBytes != 100<<30 {
		t.Fatalf("cost %+v", r.Cost)
	}
	if r.Cost.RawMonthlyUSD != 2.3 || r.Cost.RawYearlyUSD != 27.6 {
		t.Fatalf("raw bill: $%v/mo, $%v/yr", r.Cost.RawMonthlyUSD, r.Cost.RawYearlyUSD)
	}
	var store, gz9 Row
	for _, row := range r.Results {
		switch row.ID {
		case "store":
			store = row
		case "gzip:9":
			gz9 = row
		}
	}
	// gzip:9 ratio 0.3 -> 30 GiB stored -> $0.69/mo, saving $1.61/mo.
	if *store.MonthlyUSD != 2.3 || *gz9.MonthlyUSD != 0.69 || *gz9.SavingUSD != 1.61 {
		t.Fatalf("store $%v, gzip:9 $%v save $%v", *store.MonthlyUSD, *gz9.MonthlyUSD, *gz9.SavingUSD)
	}
}

func TestCostPrimitives(t *testing.T) {
	if got := projectedBytes(0.25, 1<<30); got != float64(1<<28) {
		t.Fatalf("projectedBytes: got %v", got)
	}
	// An expanding codec (ratio > 1) can make "bytes saved" negative;
	// the price clamp keeps the bill at zero instead of negative dollars.
	if got := monthlyUSD(-500, 10); got != 0 {
		t.Fatalf("monthlyUSD clamp: got %v", got)
	}
}

func TestRenderJSONRoundTrips(t *testing.T) {
	o := opts()
	o.Cost = true
	o.Price = 0.023
	r := Build(corpus(), fixedResults(t), o)
	var buf bytes.Buffer
	if err := r.Render(&buf, "json"); err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if back["schema_version"] != float64(1) || back["tool"] != "packbench" {
		t.Fatalf("envelope %v", back)
	}
	for _, key := range []string{"corpus", "results", "cost", "recommendations"} {
		if _, ok := back[key]; !ok {
			t.Fatalf("JSON missing %q", key)
		}
	}
	// Without --price the cost block must be absent, not zero-filled.
	var plain bytes.Buffer
	Build(corpus(), fixedResults(t), opts()).Render(&plain, "json")
	if strings.Contains(plain.String(), "\"cost\"") || strings.Contains(plain.String(), "monthly_usd") {
		t.Fatal("cost block leaked into a run without --price")
	}
}

func TestRenderTableAlignsAndSummarizes(t *testing.T) {
	r := Build(corpus(), fixedResults(t), opts())
	var buf bytes.Buffer
	if err := r.Render(&buf, "table"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"4 files scanned (3.9 KiB)", "sampled 1 file (1000 B)", "seed 7",
		"CODEC", "RATIO", "COMP MB/s",
		"best ratio    gzip:9", "balanced      gzip:1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("table missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, " \n") {
		t.Fatal("table has trailing spaces")
	}
}

func TestRenderTableFooterListsFailures(t *testing.T) {
	results := fixedResults(t)
	results[1].Err = "gzip exploded"
	results[1].Verified = false
	r := Build(corpus(), results, opts())
	var buf bytes.Buffer
	r.Render(&buf, "table")
	if !strings.Contains(buf.String(), "failed        gzip:1: gzip exploded") {
		t.Fatalf("failure line missing:\n%s", buf.String())
	}
}

func TestRenderMarkdownIsAPipeTable(t *testing.T) {
	r := Build(corpus(), fixedResults(t), opts())
	var buf bytes.Buffer
	if err := r.Render(&buf, "md"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 5 { // header + separator + 3 rows
		t.Fatalf("got %d lines:\n%s", len(lines), buf.String())
	}
	if !strings.HasPrefix(lines[0], "| CODEC |") || !strings.HasPrefix(lines[1], "| --- | ---:") {
		t.Fatalf("markdown header wrong:\n%s", buf.String())
	}
}

func TestRenderCSVHasStableHeaderAndRows(t *testing.T) {
	o := opts()
	o.Cost = true
	o.Price = 0.023
	r := Build(corpus(), fixedResults(t), o)
	var buf bytes.Buffer
	if err := r.Render(&buf, "csv"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	wantHead := "id,codec,level,source,in_bytes,out_bytes,ratio,saved_pct,compress_mbps,decompress_mbps,monthly_usd,monthly_saving_usd,verified,error"
	if lines[0] != wantHead {
		t.Fatalf("header %q", lines[0])
	}
	if len(lines) != 4 {
		t.Fatalf("got %d lines", len(lines))
	}
	if !strings.HasPrefix(lines[1], "gzip:9,gzip,9,builtin,1000,300,0.3000,70.0,") {
		t.Fatalf("first row %q", lines[1])
	}
}

func TestRenderUnknownFormatFails(t *testing.T) {
	r := Build(corpus(), fixedResults(t), opts())
	if err := r.Render(&bytes.Buffer{}, "xml"); err == nil {
		t.Fatal("expected an error for an unknown format")
	}
}

func TestRenderDeterministicWithoutTiming(t *testing.T) {
	// The README promise: with --no-timing, identical input produces
	// byte-identical reports you can commit and diff.
	o := opts()
	o.Timing = false
	var a, b bytes.Buffer
	Build(corpus(), fixedResults(t), o).Render(&a, "json")
	Build(corpus(), fixedResults(t), o).Render(&b, "json")
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("two builds over identical input diverged")
	}
}

func TestRoundingHelpers(t *testing.T) {
	// Values picked to be exactly representable, so this never hinges on
	// float printing quirks.
	if round1(1.25) != 1.3 || round1(-1.25) != -1.3 {
		t.Fatalf("round1: %v %v", round1(1.25), round1(-1.25))
	}
	if round2(2.375) != 2.38 || round4(0.062565) != 0.0626 {
		t.Fatalf("round2/4: %v %v", round2(2.375), round4(0.062565))
	}
}
