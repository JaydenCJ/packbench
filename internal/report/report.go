// Package report turns raw bench results into the deliverable: an
// aligned terminal table, GitHub Markdown, CSV, or a schema-versioned
// JSON document. With --no-timing the output is byte-identical for
// identical input — a report you can commit and diff.
package report

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/JaydenCJ/packbench/internal/bench"
	"github.com/JaydenCJ/packbench/internal/scan"
	"github.com/JaydenCJ/packbench/internal/version"
)

// Options selects what the report contains and how it is ordered.
type Options struct {
	Sort   string // ratio | saved | comp | dec | cost | name
	Timing bool   // include MB/s columns (non-deterministic by nature)
	Cost   bool   // include cost projection columns
	Price  float64
	Concat bool
	Seed   int64
	Paths  []string
}

// SortKeys lists the accepted --sort values.
var SortKeys = []string{"ratio", "saved", "comp", "dec", "cost", "name"}

// Row is one rendered result line.
type Row struct {
	ID         string   `json:"id"`
	Codec      string   `json:"codec"`
	Level      int      `json:"level"`
	Source     string   `json:"source"`
	InBytes    int64    `json:"in_bytes"`
	OutBytes   int64    `json:"out_bytes"`
	Ratio      float64  `json:"ratio"`
	SavedPct   float64  `json:"saved_pct"`
	CompMBps   *float64 `json:"compress_mbps,omitempty"`
	DecMBps    *float64 `json:"decompress_mbps,omitempty"`
	MonthlyUSD *float64 `json:"monthly_usd,omitempty"`
	SavingUSD  *float64 `json:"monthly_saving_usd,omitempty"`
	Verified   bool     `json:"verified"`
	Err        string   `json:"error,omitempty"`

	savedMBps float64 // internal: drives the balanced recommendation
}

// Corpus is the sampled-data summary in the JSON envelope.
type Corpus struct {
	Paths        []string `json:"paths"`
	FilesScanned int      `json:"files_scanned"`
	BytesScanned int64    `json:"bytes_scanned"`
	FilesSampled int      `json:"files_sampled"`
	BytesSampled int64    `json:"bytes_sampled"`
	Truncated    bool     `json:"truncated"`
	Seed         int64    `json:"seed"`
}

// Cost is the projection block of the JSON envelope.
type Cost struct {
	PricePerGiBMonth float64 `json:"price_usd_per_gib_month"`
	ProjectedOnBytes int64   `json:"projected_on_bytes"`
	RawMonthlyUSD    float64 `json:"raw_monthly_usd"`
	RawYearlyUSD     float64 `json:"raw_yearly_usd"`
}

// Recs names the three headline picks. Empty when nothing qualifies.
type Recs struct {
	BestRatio string `json:"best_ratio,omitempty"`
	Fastest   string `json:"fastest,omitempty"`
	Balanced  string `json:"balanced,omitempty"`
}

// Report is the full document; field order is the JSON key order.
type Report struct {
	SchemaVersion int    `json:"schema_version"`
	Tool          string `json:"tool"`
	Version       string `json:"version"`
	Mode          string `json:"mode"`
	Corpus        Corpus `json:"corpus"`
	Results       []Row  `json:"results"`
	Cost          *Cost  `json:"cost,omitempty"`
	Recs          *Recs  `json:"recommendations,omitempty"`

	opt Options // render-time switches; not part of the JSON schema
}

// Build assembles a Report from a corpus and its bench results.
func Build(c *scan.Corpus, results []bench.Result, opt Options) *Report {
	mode := "per-file"
	if opt.Concat {
		mode = "concat"
	}
	r := &Report{
		SchemaVersion: 1,
		Tool:          "packbench",
		Version:       version.Version,
		Mode:          mode,
		Corpus: Corpus{
			Paths:        opt.Paths,
			FilesScanned: c.ScannedFiles,
			BytesScanned: c.ScannedBytes,
			FilesSampled: len(c.Files),
			BytesSampled: c.SampledBytes,
			Truncated:    c.Truncated,
			Seed:         opt.Seed,
		},
		opt: opt,
	}
	if opt.Cost {
		r.Cost = &Cost{
			PricePerGiBMonth: opt.Price,
			ProjectedOnBytes: c.ScannedBytes,
			RawMonthlyUSD:    round2(monthlyUSD(float64(c.ScannedBytes), opt.Price)),
			RawYearlyUSD:     round2(12 * monthlyUSD(float64(c.ScannedBytes), opt.Price)),
		}
	}
	for _, res := range results {
		row := Row{
			ID:       res.Codec.ID(),
			Codec:    res.Codec.Family.Name,
			Level:    res.Codec.Level,
			Source:   res.Codec.Source(),
			InBytes:  res.InBytes,
			OutBytes: res.OutBytes,
			Ratio:    round4(res.Ratio()),
			SavedPct: round1(res.SavedPct()),
			Verified: res.Verified,
			Err:      res.Err,
		}
		if opt.Timing && res.Err == "" {
			row.CompMBps = f(round1(res.CompressMBps()))
			if res.DecompressNS > 0 {
				row.DecMBps = f(round1(res.DecompressMBps()))
			}
			row.savedMBps = res.SavedMBps()
		}
		if opt.Cost && res.Err == "" {
			proj := projectedBytes(res.Ratio(), c.ScannedBytes)
			row.MonthlyUSD = f(round2(monthlyUSD(proj, opt.Price)))
			row.SavingUSD = f(round2(monthlyUSD(float64(c.ScannedBytes)-proj, opt.Price)))
		}
		r.Results = append(r.Results, row)
	}
	r.sortRows()
	r.Recs = recommend(r.Results, opt.Timing)
	return r
}

func f(v float64) *float64 { return &v }

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }

// sortRows orders results by the chosen key; errored rows sink to the
// bottom and every comparison tie-breaks on ID, so output is stable.
func (r *Report) sortRows() {
	key := r.opt.Sort
	rows := r.Results
	val := func(row Row) float64 {
		switch key {
		case "saved":
			return -row.SavedPct
		case "comp":
			return -deref(row.CompMBps)
		case "dec":
			return -deref(row.DecMBps)
		case "cost":
			return deref(row.MonthlyUSD)
		default: // ratio
			return row.Ratio
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if (rows[i].Err == "") != (rows[j].Err == "") {
			return rows[i].Err == ""
		}
		if key == "name" {
			return rows[i].ID < rows[j].ID
		}
		vi, vj := val(rows[i]), val(rows[j])
		if vi != vj {
			return vi < vj
		}
		return rows[i].ID < rows[j].ID
	})
}

func deref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// recommend picks the headline codecs among error-free rows, ignoring
// the store baseline. Verification failures carry an error, so flunked
// rows never win; under --no-verify the user opted out of the check and
// still gets picks. Balanced maximizes savings velocity: bytes shed per
// CPU-second of compression.
func recommend(rows []Row, timing bool) *Recs {
	recs := &Recs{}
	var bestRatio, fastest, balanced *Row
	for i := range rows {
		row := &rows[i]
		if row.Err != "" || row.Codec == "store" {
			continue
		}
		if bestRatio == nil || row.Ratio < bestRatio.Ratio {
			bestRatio = row
		}
		if timing && (fastest == nil || deref(row.CompMBps) > deref(fastest.CompMBps)) {
			fastest = row
		}
		if timing && (balanced == nil || row.savedMBps > balanced.savedMBps) {
			balanced = row
		}
	}
	if bestRatio == nil {
		return nil
	}
	recs.BestRatio = bestRatio.ID
	if fastest != nil {
		recs.Fastest = fastest.ID
	}
	if balanced != nil {
		recs.Balanced = balanced.ID
	}
	return recs
}

// Render writes the report in the requested format.
func (r *Report) Render(w io.Writer, format string) error {
	switch format {
	case "table":
		return r.renderTable(w)
	case "md":
		return r.renderMarkdown(w)
	case "csv":
		return r.renderCSV(w)
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	default:
		return fmt.Errorf("unknown format %q (table, md, csv, json)", format)
	}
}

func (r *Report) headers() []string {
	h := []string{"CODEC", "SIZE", "RATIO", "SAVED"}
	if r.opt.Timing {
		h = append(h, "COMP MB/s", "DEC MB/s")
	}
	if r.Cost != nil {
		h = append(h, "USD/MO", "SAVE/MO")
	}
	return h
}

func (r *Report) cells(row Row) []string {
	if row.Err != "" {
		c := []string{row.ID, "-", "-", "-"}
		if r.opt.Timing {
			c = append(c, "-", "-")
		}
		if r.Cost != nil {
			c = append(c, "-", "-")
		}
		return c
	}
	c := []string{
		row.ID,
		scan.FormatSize(row.OutBytes),
		fmt.Sprintf("%.4f", row.Ratio),
		fmt.Sprintf("%.1f%%", row.SavedPct),
	}
	if r.opt.Timing {
		c = append(c, num1(row.CompMBps), num1(row.DecMBps))
	}
	if r.Cost != nil {
		c = append(c, usd(row.MonthlyUSD), usd(row.SavingUSD))
	}
	return c
}

func num1(p *float64) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprintf("%.1f", *p)
}

func usd(p *float64) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprintf("$%.2f", *p)
}

// files pluralizes the header noun so a single-file sample reads
// "sampled 1 file", not "1 files".
func files(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

func (r *Report) renderTable(w io.Writer) error {
	c := r.Corpus
	fmt.Fprintf(w, "packbench %s — %s scanned (%s); sampled %s (%s), %s mode, seed %d\n",
		r.Version, files(c.FilesScanned), scan.FormatSize(c.BytesScanned),
		files(c.FilesSampled), scan.FormatSize(c.BytesSampled), r.Mode, c.Seed)
	if r.Cost != nil {
		fmt.Fprintf(w, "cost: $%.4f per GiB-month, projected onto the full %s corpus (raw: $%.2f/mo)\n",
			r.Cost.PricePerGiBMonth, scan.FormatSize(r.Cost.ProjectedOnBytes), r.Cost.RawMonthlyUSD)
	}
	fmt.Fprintln(w)

	headers := r.headers()
	grid := [][]string{headers}
	for _, row := range r.Results {
		grid = append(grid, r.cells(row))
	}
	widths := make([]int, len(headers))
	for _, line := range grid {
		for i, cell := range line {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	for _, line := range grid {
		var b strings.Builder
		for i, cell := range line {
			if i > 0 {
				b.WriteString("  ")
			}
			if i == 0 {
				b.WriteString(cell + strings.Repeat(" ", widths[i]-len(cell)))
			} else {
				b.WriteString(strings.Repeat(" ", widths[i]-len(cell)) + cell)
			}
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
	r.renderFooter(w)
	return nil
}

func (r *Report) renderFooter(w io.Writer) {
	var bad []string
	for _, row := range r.Results {
		if row.Err != "" {
			bad = append(bad, fmt.Sprintf("%s: %s", row.ID, row.Err))
		}
	}
	if len(bad) > 0 || r.Recs != nil {
		fmt.Fprintln(w)
	}
	if r.Recs != nil {
		fmt.Fprintf(w, "best ratio    %s\n", r.Recs.BestRatio)
		if r.Recs.Fastest != "" {
			fmt.Fprintf(w, "fastest       %s\n", r.Recs.Fastest)
		}
		if r.Recs.Balanced != "" {
			fmt.Fprintf(w, "balanced      %s   (most bytes shed per CPU-second)\n", r.Recs.Balanced)
		}
	}
	for _, msg := range bad {
		fmt.Fprintf(w, "failed        %s\n", msg)
	}
}

func (r *Report) renderMarkdown(w io.Writer) error {
	headers := r.headers()
	fmt.Fprintln(w, "| "+strings.Join(headers, " | ")+" |")
	sep := make([]string, len(headers))
	sep[0] = "---"
	for i := 1; i < len(sep); i++ {
		sep[i] = "---:"
	}
	fmt.Fprintln(w, "| "+strings.Join(sep, " | ")+" |")
	for _, row := range r.Results {
		fmt.Fprintln(w, "| "+strings.Join(r.cells(row), " | ")+" |")
	}
	return nil
}

func (r *Report) renderCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	head := []string{"id", "codec", "level", "source", "in_bytes", "out_bytes", "ratio", "saved_pct"}
	if r.opt.Timing {
		head = append(head, "compress_mbps", "decompress_mbps")
	}
	if r.Cost != nil {
		head = append(head, "monthly_usd", "monthly_saving_usd")
	}
	head = append(head, "verified", "error")
	if err := cw.Write(head); err != nil {
		return err
	}
	for _, row := range r.Results {
		rec := []string{
			row.ID, row.Codec, fmt.Sprintf("%d", row.Level), row.Source,
			fmt.Sprintf("%d", row.InBytes), fmt.Sprintf("%d", row.OutBytes),
			fmt.Sprintf("%.4f", row.Ratio), fmt.Sprintf("%.1f", row.SavedPct),
		}
		if r.opt.Timing {
			rec = append(rec, csvNum(row.CompMBps), csvNum(row.DecMBps))
		}
		if r.Cost != nil {
			rec = append(rec, csvNum(row.MonthlyUSD), csvNum(row.SavingUSD))
		}
		rec = append(rec, fmt.Sprintf("%t", row.Verified), row.Err)
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func csvNum(p *float64) string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf("%.2f", *p)
}
