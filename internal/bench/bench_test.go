// Bench math and orchestration. The Runner's clock is a stepping fake,
// so every throughput figure asserted here is exact — no wall-clock
// timing, no flakiness.
package bench

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JaydenCJ/packbench/internal/codec"
)

// stepClock advances a fixed amount on every reading, so each timed
// section appears to take exactly `step`.
func stepClock(step time.Duration) func() time.Time {
	t := time.Unix(0, 0)
	return func() time.Time {
		t = t.Add(step)
		return t
	}
}

// builtin returns a ready-to-run built-in codec by resolving its spec
// through a Registry with no externals.
func builtin(t *testing.T, spec string) codec.Codec {
	t.Helper()
	r := &codec.Registry{DisableExternal: true}
	cs, err := r.Resolve(spec)
	if err != nil || len(cs) != 1 {
		t.Fatalf("resolve %q: %v, %v", spec, cs, err)
	}
	return cs[0]
}

// fakeExternal resolves spec against a shell script standing in for the
// external binary, for failure-injection tests.
func fakeExternal(t *testing.T, spec, body string) codec.Codec {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fakebin")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &codec.Registry{LookPath: func(string) (string, error) { return p, nil }}
	cs, err := r.Resolve(spec)
	if err != nil || len(cs) != 1 {
		t.Fatalf("resolve %q: %v, %v", spec, cs, err)
	}
	return cs[0]
}

func text(n int) []byte {
	return bytes.Repeat([]byte("the same line of log output over and over "), n)
}

func TestRatioAndSavedPct(t *testing.T) {
	r := Result{InBytes: 1000, OutBytes: 250}
	if r.Ratio() != 0.25 || r.SavedPct() != 75 {
		t.Fatalf("ratio %v, saved %v", r.Ratio(), r.SavedPct())
	}
	// Guard against 0/0: an empty corpus is a degenerate baseline, not a
	// division panic.
	empty := Result{}
	if empty.Ratio() != 1 || empty.SavedPct() != 0 {
		t.Fatalf("empty: ratio %v, saved %v", empty.Ratio(), empty.SavedPct())
	}
}

func TestThroughputMath(t *testing.T) {
	// 10 MB in 1 second = 10 MB/s, decimal megabytes by definition.
	r := Result{InBytes: 10_000_000, CompressNS: int64(time.Second)}
	if got := r.CompressMBps(); got != 10 {
		t.Fatalf("got %v MB/s", got)
	}
	// Zero elapsed time must yield 0, never +Inf.
	if got := (Result{InBytes: 1000}).CompressMBps(); got != 0 {
		t.Fatalf("zero duration: got %v", got)
	}
	// SavedMBps: 1000 bytes in, 400 out, in 1s = 600 bytes shed/second.
	saved := Result{InBytes: 1000, OutBytes: 400, CompressNS: int64(time.Second)}
	if got := saved.SavedMBps(); got != 0.0006 {
		t.Fatalf("saved MB/s: got %v", got)
	}
}

func TestRunMeasuresEveryCodec(t *testing.T) {
	blobs := [][]byte{text(50), text(30)}
	codecs := []codec.Codec{builtin(t, "store"), builtin(t, "gzip:6")}
	r := &Runner{Now: stepClock(time.Millisecond)}
	results := r.Run(blobs, codecs, Options{Verify: true})
	if len(results) != 2 {
		t.Fatalf("got %d results", len(results))
	}
	for _, res := range results {
		if res.Err != "" || !res.Verified {
			t.Fatalf("%s: %+v", res.Codec.ID(), res)
		}
		if res.InBytes != int64(len(blobs[0])+len(blobs[1])) {
			t.Fatalf("%s InBytes %d", res.Codec.ID(), res.InBytes)
		}
	}
}

func TestRunTimingIsDeterministicWithFakeClock(t *testing.T) {
	blobs := [][]byte{text(10), text(10), text(10)}
	r := &Runner{Now: stepClock(time.Millisecond)}
	res := r.Run(blobs, []codec.Codec{builtin(t, "store")}, Options{Verify: true})[0]
	// One step per timed section: 3 compressions and 3 decompressions.
	if res.CompressNS != 3*int64(time.Millisecond) || res.DecompressNS != 3*int64(time.Millisecond) {
		t.Fatalf("got comp=%d dec=%d", res.CompressNS, res.DecompressNS)
	}
}

func TestRunGzipBeatsStoreOnRedundantText(t *testing.T) {
	blobs := [][]byte{text(100)}
	r := &Runner{Now: stepClock(time.Microsecond)}
	results := r.Run(blobs, []codec.Codec{builtin(t, "store"), builtin(t, "gzip:6")}, Options{Verify: true})
	if results[0].Ratio() != 1 {
		t.Fatalf("store ratio %v", results[0].Ratio())
	}
	if results[1].Ratio() >= 0.2 {
		t.Fatalf("gzip:6 on pure repetition should crush it, got %v", results[1].Ratio())
	}
}

func TestRunConcatMergesBlobsIntoOneSolidArchive(t *testing.T) {
	// Per-file gzip pays the header tax per blob; concat pays it once, so
	// on many small identical files concat must win.
	small := make([][]byte, 50)
	for i := range small {
		small[i] = text(1)
	}
	r := &Runner{Now: stepClock(time.Microsecond)}
	per := r.Run(small, []codec.Codec{builtin(t, "gzip:6")}, Options{})[0]
	solid := r.Run(small, []codec.Codec{builtin(t, "gzip:6")}, Options{Concat: true})[0]
	if per.InBytes != solid.InBytes {
		t.Fatalf("concat changed the input size: %d vs %d", per.InBytes, solid.InBytes)
	}
	if solid.OutBytes >= per.OutBytes {
		t.Fatalf("solid %d should beat per-file %d", solid.OutBytes, per.OutBytes)
	}
}

func TestRunVerifyOffSkipsDecompressionAndReportsUnverified(t *testing.T) {
	// With verification off nothing was checked, so Verified must be
	// false — claiming otherwise would be lying in the report.
	r := &Runner{Now: stepClock(time.Millisecond)}
	res := r.Run([][]byte{text(10)}, []codec.Codec{builtin(t, "gzip:6")}, Options{})[0]
	if res.Verified || res.DecompressNS != 0 {
		t.Fatalf("got %+v", res)
	}
}

func TestRunVerifyCatchesLyingCodec(t *testing.T) {
	// A codec that emits wrong bytes on "decompress" must be flagged, not
	// trusted — the whole point of --verify is catching exactly this.
	c := fakeExternal(t, "zstd:3", `printf 'not what you gave me'`)
	r := &Runner{Now: stepClock(time.Millisecond)}
	res := r.Run([][]byte{text(5)}, []codec.Codec{c}, Options{Verify: true})[0]
	if res.Verified || !strings.Contains(res.Err, "round-trip mismatch") {
		t.Fatalf("got %+v", res)
	}
}

func TestRunCompressFailureIsRecordedNotFatal(t *testing.T) {
	broken := fakeExternal(t, "zstd:3", `echo "no license for level 3" >&2; exit 1`)
	r := &Runner{Now: stepClock(time.Millisecond)}
	results := r.Run([][]byte{text(5)}, []codec.Codec{broken, builtin(t, "gzip:6")}, Options{Verify: true})
	if results[0].Err == "" || results[0].Verified {
		t.Fatalf("broken codec not flagged: %+v", results[0])
	}
	if !strings.Contains(results[0].Err, "no license") {
		t.Fatalf("stderr lost: %q", results[0].Err)
	}
	if results[1].Err != "" || !results[1].Verified {
		t.Fatalf("one broken codec must not poison the rest: %+v", results[1])
	}
}

func TestRunDecompressFailureIsLabelled(t *testing.T) {
	// Pass-through on compress, hard failure on decompress: the error
	// message must say which direction died.
	p := filepath.Join(t.TempDir(), "fakebin")
	body := "#!/bin/sh\nfor a in \"$@\"; do if [ \"$a\" = \"-d\" ]; then exit 9; fi; done\ncat\n"
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	reg := &codec.Registry{LookPath: func(string) (string, error) { return p, nil }}
	cs, err := reg.Resolve("zstd:3")
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{Now: stepClock(time.Millisecond)}
	res := r.Run([][]byte{text(5)}, cs, Options{Verify: true})[0]
	if res.Verified || !strings.HasPrefix(res.Err, "decompress:") {
		t.Fatalf("got %+v", res)
	}
}

func TestRunEmptyBlobList(t *testing.T) {
	r := &Runner{Now: stepClock(time.Millisecond)}
	res := r.Run(nil, []codec.Codec{builtin(t, "store")}, Options{Verify: true})[0]
	if res.InBytes != 0 || res.OutBytes != 0 || res.Err != "" {
		t.Fatalf("got %+v", res)
	}
}

func TestRunnerDefaultsToRealClock(t *testing.T) {
	// Without an injected clock the runner must still work (used by the
	// CLI); we only assert it produced sane, non-negative durations.
	r := &Runner{}
	res := r.Run([][]byte{text(5)}, []codec.Codec{builtin(t, "gzip:1")}, Options{Verify: true})[0]
	if res.Err != "" || res.CompressNS < 0 || res.DecompressNS < 0 {
		t.Fatalf("got %+v", res)
	}
}
