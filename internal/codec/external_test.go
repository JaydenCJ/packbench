// External codec plumbing: argv construction and process piping. The
// process tests use tiny local shell scripts as stand-in binaries, so
// they are deterministic and never require zstd/xz/... to be installed.
package codec

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fam(t *testing.T, name string) Family {
	t.Helper()
	f, ok := familyByName(name)
	if !ok {
		t.Fatalf("family %q missing", name)
	}
	return f
}

func TestExtArgsPinSingleThread(t *testing.T) {
	// zstd and xz auto-parallelize; a fair one-core comparison must pin
	// -T1 on both directions or their MB/s numbers are meaningless.
	for _, name := range []string{"zstd", "xz"} {
		c := strings.Join(extArgs(fam(t, name), 3, false), " ")
		d := strings.Join(extArgs(fam(t, name), 3, true), " ")
		if !strings.Contains(c, "-T1") || !strings.Contains(d, "-T1") {
			t.Fatalf("%s not pinned to one thread: c=%q d=%q", name, c, d)
		}
	}
}

func TestExtArgsCarryTheLevelOnCompressOnly(t *testing.T) {
	if got := strings.Join(extArgs(fam(t, "zstd"), 19, false), " "); !strings.Contains(got, "-19") {
		t.Fatalf("zstd argv %q lost the level", got)
	}
	if got := strings.Join(extArgs(fam(t, "brotli"), 11, false), " "); !strings.HasSuffix(got, "-q 11") {
		t.Fatalf("brotli argv %q lost the level", got)
	}
	// Decompression takes no level, ever — passing one changes semantics
	// on several of these tools.
	for _, name := range []string{"zstd", "xz", "bzip2", "lz4", "brotli"} {
		got := strings.Join(extArgs(fam(t, name), 9, true), " ")
		if !strings.Contains(got, "-d") || strings.Contains(got, "9") {
			t.Fatalf("%s decompress argv %q", name, got)
		}
	}
	if got := extArgs(fam(t, "gzip"), 6, false); got != nil {
		t.Fatalf("builtin families have no external argv, got %v", got)
	}
}

// script writes an executable shell script and returns its path.
func script(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fakes need a POSIX sh")
	}
	p := filepath.Join(t.TempDir(), "fakecodec")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunExternalPipesStdinToStdout(t *testing.T) {
	p := script(t, "cat")
	src := []byte("through the pipe, byte for byte")
	out, err := runExternal(p, nil, src)
	if err != nil || !bytes.Equal(out, src) {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestRunExternalPassesArgs(t *testing.T) {
	p := script(t, `printf '%s' "$1"`)
	out, err := runExternal(p, []string{"-19"}, nil)
	if err != nil || string(out) != "-19" {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestRunExternalFoldsFirstStderrLineIntoTheError(t *testing.T) {
	// A broken binary must produce a diagnosable one-line message, not
	// silence and not a stderr dump.
	p := script(t, `echo "boom: bad frame" >&2; echo "stack noise" >&2; exit 3`)
	_, err := runExternal(p, nil, []byte("x"))
	if err == nil || !strings.Contains(err.Error(), "boom: bad frame") {
		t.Fatalf("stderr not surfaced: %v", err)
	}
	if strings.Contains(err.Error(), "stack noise") {
		t.Fatalf("error kept more than the first stderr line: %v", err)
	}
}

func TestRunExternalFailuresWithoutStderrStillError(t *testing.T) {
	p := script(t, "exit 2")
	if _, err := runExternal(p, nil, nil); err == nil {
		t.Fatal("expected an error from a silent non-zero exit")
	}
	if _, err := runExternal("", nil, nil); err == nil {
		t.Fatal("expected an error for an unresolved binary path")
	}
}

func TestExternalCodecRoundTripThroughFakeBinary(t *testing.T) {
	// End-to-end through Codec.Compress/Decompress with a fake zstd that
	// is a plain pass-through: proves the Registry path plumbing works
	// without needing the real binary.
	p := script(t, "cat")
	r := &Registry{LookPath: func(string) (string, error) { return p, nil }}
	cs, err := r.Resolve("zstd:3")
	if err != nil || len(cs) != 1 {
		t.Fatalf("resolve: %v, %v", cs, err)
	}
	src := []byte("fake zstd round trip")
	packed, err := cs[0].Compress(src)
	if err != nil {
		t.Fatal(err)
	}
	back, err := cs[0].Decompress(packed)
	if err != nil || !bytes.Equal(back, src) {
		t.Fatalf("got %q, %v", back, err)
	}
}
