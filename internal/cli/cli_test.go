// In-process CLI integration tests: the same Run(argv) the binary calls,
// against real temp-dir corpora. External-codec paths are exercised by
// pointing PATH at fake shell-script binaries, so nothing here depends
// on what the host has installed.
package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// run executes the CLI in-process and captures both streams.
func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = Run(args, &out, &errb)
	return code, out.String(), errb.String()
}

// corpusDir builds a small mixed corpus: compressible logs plus one
// already-random binary file.
func corpusDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	line := "2026-07-01T12:00:00Z INFO request served path=/api/v1/items status=200\n"
	if err := os.WriteFile(filepath.Join(dir, "app.log"), []byte(strings.Repeat(line, 200)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "audit.log"), []byte(strings.Repeat(line, 100)), 0o644); err != nil {
		t.Fatal(err)
	}
	// Deterministic "incompressible" bytes: a fixed xorshift stream.
	rnd := make([]byte, 4096)
	x := uint64(0x9E3779B97F4A7C15)
	for i := range rnd {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		rnd[i] = byte(x)
	}
	if err := os.WriteFile(filepath.Join(dir, "blob.bin"), rnd, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestNoArgsPrintsUsageAndExitsZero(t *testing.T) {
	code, out, _ := run(t)
	if code != ExitOK || !strings.Contains(out, "Usage:") {
		t.Fatalf("code %d, out %q", code, out)
	}
}

func TestVersionAndHelp(t *testing.T) {
	for _, argv := range [][]string{{"version"}, {"--version"}, {"-v"}} {
		code, out, _ := run(t, argv...)
		if code != ExitOK || out != "packbench 0.1.0\n" {
			t.Fatalf("%v: code %d, out %q", argv, code, out)
		}
	}
	code, out, _ := run(t, "help")
	if code != ExitOK || !strings.Contains(out, "packbench run [flags] PATH...") {
		t.Fatalf("help: code %d, out %q", code, out)
	}
}

func TestUsageErrorsExitTwoWithAPointedMessage(t *testing.T) {
	dir := corpusDir(t)
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"frobnicate"}, `unknown command "frobnicate"`},
		{[]string{"run"}, "at least one PATH"},
		{[]string{"run", "--format", "xml", dir}, `--format "xml"`},
		{[]string{"run", "--sort", "vibes", dir}, `--sort "vibes"`},
		{[]string{"run", "--max-bytes", "many", dir}, "--max-bytes"},
		{[]string{"run", "--price", "-1", dir}, "--price"},
		{[]string{"run", "--codecs", "snappy", dir}, "unknown codec"},
		{[]string{"run", "--no-external", "--codecs", "zstd:3", dir}, "--no-external"},
	}
	for _, tc := range cases {
		code, _, errb := run(t, tc.argv...)
		if code != ExitUsage || !strings.Contains(errb, tc.want) {
			t.Fatalf("%v: code %d, err %q (want %q)", tc.argv, code, errb, tc.want)
		}
	}
}

func TestRunMissingPathExitsRuntime(t *testing.T) {
	code, _, _ := run(t, "run", "--no-external", filepath.Join(t.TempDir(), "gone"))
	if code != ExitRuntime {
		t.Fatalf("code %d", code)
	}
}

func TestRunTableOnRealCorpus(t *testing.T) {
	code, out, errb := run(t, "run", "--no-external", corpusDir(t))
	if code != ExitOK {
		t.Fatalf("code %d, err %q", code, errb)
	}
	for _, want := range []string{"3 files scanned", "store", "gzip:1", "gzip:6", "gzip:9", "lzw", "best ratio"} {
		if !strings.Contains(out, want) {
			t.Fatalf("table missing %q:\n%s", want, out)
		}
	}
}

func TestRunJSONIsParseableAndHonest(t *testing.T) {
	code, out, _ := run(t, "run", "--no-external", "--format", "json", corpusDir(t))
	if code != ExitOK {
		t.Fatalf("code %d", code)
	}
	var rep struct {
		SchemaVersion int    `json:"schema_version"`
		Tool          string `json:"tool"`
		Corpus        struct {
			FilesScanned int   `json:"files_scanned"`
			BytesSampled int64 `json:"bytes_sampled"`
		} `json:"corpus"`
		Results []struct {
			ID       string  `json:"id"`
			Ratio    float64 `json:"ratio"`
			Verified bool    `json:"verified"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.SchemaVersion != 1 || rep.Tool != "packbench" || rep.Corpus.FilesScanned != 3 {
		t.Fatalf("envelope %+v", rep)
	}
	for _, res := range rep.Results {
		if !res.Verified {
			t.Fatalf("%s not verified", res.ID)
		}
		if res.ID == "gzip:9" && res.Ratio >= 0.2 {
			t.Fatalf("gzip:9 on repetitive logs should crush them, got %v", res.Ratio)
		}
	}
}

func TestRunNoVerifyIsHonestAboutIt(t *testing.T) {
	// --no-verify must not vouch for a check that never ran: every row
	// says "verified": false and carries no decompression throughput,
	// yet recommendations still appear — the user opted out knowingly.
	code, out, _ := run(t, "run", "--no-external", "--no-verify", "--format", "json", corpusDir(t))
	if code != ExitOK {
		t.Fatalf("code %d", code)
	}
	if strings.Contains(out, `"verified": true`) {
		t.Fatalf("--no-verify claimed a verified round trip:\n%s", out)
	}
	if strings.Contains(out, "decompress_mbps") {
		t.Fatalf("--no-verify produced decompression timing:\n%s", out)
	}
	if !strings.Contains(out, `"recommendations"`) {
		t.Fatalf("--no-verify dropped recommendations:\n%s", out)
	}
}

func TestRunCSVAndMarkdownFormats(t *testing.T) {
	dir := corpusDir(t)
	code, csvOut, _ := run(t, "run", "--no-external", "--format", "csv", dir)
	if code != ExitOK || !strings.HasPrefix(csvOut, "id,codec,level,source,") {
		t.Fatalf("csv: code %d, %q", code, csvOut[:60])
	}
	code, mdOut, _ := run(t, "run", "--no-external", "--format", "md", dir)
	if code != ExitOK || !strings.HasPrefix(mdOut, "| CODEC |") {
		t.Fatalf("md: code %d, %q", code, mdOut[:40])
	}
}

func TestRunPriceTogglesCostColumns(t *testing.T) {
	dir := corpusDir(t)
	code, out, _ := run(t, "run", "--no-external", "--price", "0.023", dir)
	if code != ExitOK || !strings.Contains(out, "USD/MO") || !strings.Contains(out, "$0.0230 per GiB-month") {
		t.Fatalf("code %d:\n%s", code, out)
	}
	code, out, _ = run(t, "run", "--no-external", dir)
	if code != ExitOK || strings.Contains(out, "USD/MO") {
		t.Fatalf("cost columns leaked into a run without --price:\n%s", out)
	}
}

func TestRunOutWritesFile(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "report.json")
	code, out, _ := run(t, "run", "--no-external", "--format", "json", "--out", dst, corpusDir(t))
	if code != ExitOK || out != "" {
		t.Fatalf("code %d, stdout %q", code, out)
	}
	data, err := os.ReadFile(dst)
	if err != nil || !strings.Contains(string(data), `"schema_version": 1`) {
		t.Fatalf("report file: %v", err)
	}
}

func TestRunNoTimingIsByteIdenticalAcrossRuns(t *testing.T) {
	dir := corpusDir(t)
	args := []string{"run", "--no-external", "--no-timing", "--format", "json", dir}
	_, a, _ := run(t, args...)
	_, b, _ := run(t, args...)
	if a != b {
		t.Fatal("two --no-timing runs over identical input diverged")
	}
	if strings.Contains(a, "compress_mbps") {
		t.Fatal("--no-timing leaked throughput fields")
	}
}

func TestRunIncludeExcludeFilters(t *testing.T) {
	code, out, _ := run(t, "run", "--no-external", "--include", "*.log", "--exclude", "audit.*",
		"--format", "json", corpusDir(t))
	if code != ExitOK {
		t.Fatalf("code %d", code)
	}
	var rep struct {
		Corpus struct {
			FilesScanned int `json:"files_scanned"`
		} `json:"corpus"`
	}
	json.Unmarshal([]byte(out), &rep)
	if rep.Corpus.FilesScanned != 1 {
		t.Fatalf("filters selected %d files, want 1", rep.Corpus.FilesScanned)
	}
}

func TestRunSeededSamplingIsStable(t *testing.T) {
	dir := corpusDir(t)
	args := []string{"run", "--no-external", "--no-timing", "--max-files", "2", "--seed", "9",
		"--format", "json", dir}
	_, a, _ := run(t, args...)
	_, b, _ := run(t, args...)
	if a != b {
		t.Fatal("same seed produced different samples")
	}
}

func TestRunExplicitLevelRange(t *testing.T) {
	code, out, _ := run(t, "run", "--no-external", "--codecs", "gzip:1-3", "--format", "csv", corpusDir(t))
	if code != ExitOK {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{"gzip:1,", "gzip:2,", "gzip:3,"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "store,") {
		t.Fatal("explicit spec must not smuggle in the auto set")
	}
}

func TestRunBrokenExternalCodecExitsFindings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake binaries need a POSIX sh")
	}
	// A PATH with only a broken zstd: the row must fail, the report must
	// still print, and the exit code must flag it.
	bins := t.TempDir()
	fake := filepath.Join(bins, "zstd")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho \"corrupt install\" >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bins)
	code, out, _ := run(t, "run", "--codecs", "zstd:3,gzip:6", corpusDir(t))
	if code != ExitFindings {
		t.Fatalf("code %d:\n%s", code, out)
	}
	if !strings.Contains(out, "failed        zstd:3") || !strings.Contains(out, "corrupt install") {
		t.Fatalf("failure not reported:\n%s", out)
	}
	if !strings.Contains(out, "best ratio    gzip:6") {
		t.Fatalf("healthy codecs must still be reported:\n%s", out)
	}
}

func TestCodecsListsCatalogueWithAvailability(t *testing.T) {
	code, out, _ := run(t, "codecs", "--no-external")
	if code != ExitOK {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{"CODEC", "store", "gzip", "1-9", "zstd", "not found", "builtin"} {
		if !strings.Contains(out, want) {
			t.Fatalf("codecs output missing %q:\n%s", want, out)
		}
	}
}

func TestCodecsShowsResolvedBinaryPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake binaries need a POSIX sh")
	}
	bins := t.TempDir()
	fake := filepath.Join(bins, "lz4")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bins)
	code, out, _ := run(t, "codecs")
	if code != ExitOK || !strings.Contains(out, fake) {
		t.Fatalf("resolved path missing:\n%s", out)
	}
}

func TestCodecsRejectsArguments(t *testing.T) {
	code, _, errb := run(t, "codecs", "extra")
	if code != ExitUsage || !strings.Contains(errb, "takes no arguments") {
		t.Fatalf("code %d, err %q", code, errb)
	}
}
