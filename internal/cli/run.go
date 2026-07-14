package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/JaydenCJ/packbench/internal/bench"
	"github.com/JaydenCJ/packbench/internal/codec"
	"github.com/JaydenCJ/packbench/internal/report"
	"github.com/JaydenCJ/packbench/internal/scan"
)

// formats are the accepted --format values, in help order.
var formats = map[string]bool{"table": true, "md": true, "csv": true, "json": true}

// runBench implements `packbench run`: scan -> sample -> bench -> report.
func runBench(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("run", stderr)
	codecSpec := fs.String("codecs", "auto", "codec set: auto, all, or a comma list like gzip:1-9,zstd:3,lzw")
	format := fs.String("format", "table", "output format: table, md, csv, json")
	sortKey := fs.String("sort", "ratio", "row order: ratio, saved, comp, dec, cost, name")
	maxBytes := fs.String("max-bytes", "64MiB", "sample byte budget (also the RAM ceiling); 0 = whole corpus")
	maxFiles := fs.Int("max-files", 0, "sample at most this many files; 0 = no cap")
	minSize := fs.String("min-size", "0", "skip files smaller than this")
	seed := fs.Int64("seed", 1, "sampling seed; same seed, same sample, same report")
	concat := fs.Bool("concat", false, "benchmark one solid archive instead of per-file")
	noVerify := fs.Bool("no-verify", false, "skip decompress + byte-compare (drops the DEC column)")
	noTiming := fs.Bool("no-timing", false, "drop MB/s columns; output becomes byte-identical per input")
	noExternal := fs.Bool("no-external", false, "built-in codecs only; never exec zstd/xz/bzip2/lz4/brotli")
	price := fs.Float64("price", 0, "USD per GiB-month; adds cost projection columns (S3 Standard is 0.023)")
	outPath := fs.String("out", "", "write the report to a file instead of stdout")
	var include, exclude stringsFlag
	fs.Var(&include, "include", "glob to include (repeatable); empty = everything")
	fs.Var(&exclude, "exclude", "glob to exclude (repeatable); exclude wins over include")
	if code, done := parseFlags(fs, args); done {
		return code
	}
	paths := fs.Args()
	if len(paths) == 0 {
		fmt.Fprintln(stderr, "packbench run: at least one PATH is required")
		return ExitUsage
	}
	if !formats[*format] {
		fmt.Fprintf(stderr, "packbench run: unknown --format %q (table, md, csv, json)\n", *format)
		return ExitUsage
	}
	if !validSort(*sortKey) {
		fmt.Fprintf(stderr, "packbench run: unknown --sort %q (ratio, saved, comp, dec, cost, name)\n", *sortKey)
		return ExitUsage
	}
	if *price < 0 {
		fmt.Fprintln(stderr, "packbench run: --price must be >= 0")
		return ExitUsage
	}
	budget, err := scan.ParseSize(*maxBytes)
	if err != nil {
		fmt.Fprintf(stderr, "packbench run: --max-bytes: %v\n", err)
		return ExitUsage
	}
	floor, err := scan.ParseSize(*minSize)
	if err != nil {
		fmt.Fprintf(stderr, "packbench run: --min-size: %v\n", err)
		return ExitUsage
	}

	reg := &codec.Registry{DisableExternal: *noExternal}
	codecs, err := reg.Resolve(*codecSpec)
	if err != nil {
		fmt.Fprintf(stderr, "packbench run: %v\n", err)
		return ExitUsage
	}

	corpus, err := scan.Scan(paths, scan.Options{
		Include:  include,
		Exclude:  exclude,
		MinSize:  floor,
		MaxFiles: *maxFiles,
		MaxBytes: budget,
		Seed:     *seed,
	})
	if err != nil {
		fmt.Fprintf(stderr, "packbench run: %v\n", err)
		return ExitRuntime
	}
	blobs, err := scan.Load(corpus)
	if err != nil {
		fmt.Fprintf(stderr, "packbench run: %v\n", err)
		return ExitRuntime
	}

	runner := &bench.Runner{}
	results := runner.Run(blobs, codecs, bench.Options{Concat: *concat, Verify: !*noVerify})

	rep := report.Build(corpus, results, report.Options{
		Sort:   *sortKey,
		Timing: !*noTiming,
		Cost:   *price > 0,
		Price:  *price,
		Concat: *concat,
		Seed:   *seed,
		Paths:  paths,
	})

	if err := writeReport(rep, *format, *outPath, stdout); err != nil {
		fmt.Fprintf(stderr, "packbench run: %v\n", err)
		return ExitRuntime
	}
	for _, res := range results {
		if res.Err != "" {
			return ExitFindings
		}
	}
	return ExitOK
}

// writeReport renders to stdout or to --out. The file Close error is
// checked: a full disk on flush must fail the run, not pass silently.
func writeReport(rep *report.Report, format, outPath string, stdout io.Writer) error {
	if outPath == "" {
		return rep.Render(stdout, format)
	}
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	err = rep.Render(f, format)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func validSort(key string) bool {
	for _, k := range report.SortKeys {
		if k == key {
			return true
		}
	}
	return false
}
