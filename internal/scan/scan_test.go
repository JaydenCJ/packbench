// Directory walking, filtering, and seeded sampling. Every test builds
// its corpus in a temp dir, so nothing depends on the host filesystem
// and the same seed always draws the same sample.
package scan

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// write creates a file of n bytes under root, making parents as needed.
func write(t *testing.T, root, rel string, n int) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func names(c *Corpus) []string {
	out := make([]string, len(c.Files))
	for i, f := range c.Files {
		out[i] = filepath.Base(f.Path)
	}
	return out
}

func TestScanWalksNestedDirectories(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.log", 10)
	write(t, dir, "sub/deep/b.log", 20)
	c, err := Scan([]string{dir}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if c.ScannedFiles != 2 || c.ScannedBytes != 30 || c.SampledBytes != 30 {
		t.Fatalf("got %d files, %d/%d bytes", c.ScannedFiles, c.ScannedBytes, c.SampledBytes)
	}
}

func TestScanSingleFileAndDeduplication(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "one.bin", 7)
	c, err := Scan([]string{p}, Options{})
	if err != nil || len(c.Files) != 1 || c.Files[0].Take != 7 {
		t.Fatalf("got %+v, %v", c, err)
	}
	// The same file named twice, plus its parent dir, is still one file.
	c, err = Scan([]string{p, p, dir}, Options{})
	if err != nil || c.ScannedFiles != 1 {
		t.Fatalf("expected 1 file after dedupe, got %d (%v)", c.ScannedFiles, err)
	}
}

func TestScanErrorCases(t *testing.T) {
	if _, err := Scan(nil, Options{}); err == nil {
		t.Fatal("expected an error for no paths")
	}
	if _, err := Scan([]string{filepath.Join(t.TempDir(), "nope")}, Options{}); err == nil {
		t.Fatal("expected an error for a missing path")
	}
	dir := t.TempDir()
	write(t, dir, "a.log", 10)
	if _, err := Scan([]string{dir}, Options{Include: []string{"*.parquet"}}); err == nil {
		t.Fatal("expected an error when filters match nothing")
	}
}

func TestScanPrunesVCSDirectories(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".git/objects/pack/big.pack", 1000)
	write(t, dir, ".hg/store/data", 500)
	write(t, dir, "real.log", 10)
	c, err := Scan([]string{dir}, Options{})
	if err != nil || c.ScannedFiles != 1 {
		t.Fatalf("VCS internals leaked into the corpus: %v files (%v)", c.ScannedFiles, err)
	}
}

func TestScanSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := write(t, dir, "real.log", 10)
	if err := os.Symlink(target, filepath.Join(dir, "alias.log")); err != nil {
		t.Skip("symlinks not supported here")
	}
	c, err := Scan([]string{dir}, Options{})
	if err != nil || c.ScannedFiles != 1 {
		t.Fatalf("symlink was counted: %d files (%v)", c.ScannedFiles, err)
	}
}

func TestScanFilters(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "tiny.log", 3)
	write(t, dir, "sub/keep.log", 300)
	write(t, dir, "sub/drop.log", 300)
	write(t, dir, "sub/data.csv", 300)
	// Min-size drops tiny.log; the include glob matches base names in
	// subdirectories; exclude wins over include.
	c, err := Scan([]string{dir}, Options{
		MinSize: 100,
		Include: []string{"*.log"},
		Exclude: []string{"drop.*"},
	})
	if err != nil || !reflect.DeepEqual(names(c), []string{"keep.log"}) {
		t.Fatalf("got %v, %v", names(c), err)
	}
}

func TestScanDirSlashStarPatternReachesDeepFiles(t *testing.T) {
	// "cache/*" should exclude cache/x/y.tmp too, not only direct children —
	// that is what users mean by the pattern.
	dir := t.TempDir()
	write(t, dir, "cache/x/y.tmp", 10)
	write(t, dir, "data.log", 10)
	c, err := Scan([]string{dir}, Options{Exclude: []string{"cache/*"}})
	if err != nil || !reflect.DeepEqual(names(c), []string{"data.log"}) {
		t.Fatalf("got %v, %v", names(c), err)
	}
}

func TestScanFilesSortedByPath(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "z.log", 1)
	write(t, dir, "a.log", 1)
	write(t, dir, "m/x.log", 1)
	c, err := Scan([]string{dir}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(c.Files); i++ {
		if c.Files[i-1].Path >= c.Files[i].Path {
			t.Fatalf("corpus not sorted: %v", names(c))
		}
	}
}

func TestSampleMaxFilesCap(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		write(t, dir, n+".log", 10)
	}
	c, err := Scan([]string{dir}, Options{MaxFiles: 2, Seed: 1})
	if err != nil || len(c.Files) != 2 {
		t.Fatalf("got %d sampled files (%v)", len(c.Files), err)
	}
	if c.ScannedFiles != 5 || c.ScannedBytes != 50 {
		t.Fatalf("scanned totals must still cover the full set: %+v", c)
	}
}

func TestSampleByteBudgetTruncatesLastFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.log", 100)
	write(t, dir, "b.log", 100)
	c, err := Scan([]string{dir}, Options{MaxBytes: 150, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	if c.SampledBytes != 150 || !c.Truncated {
		t.Fatalf("budget not honored: sampled %d, truncated %v", c.SampledBytes, c.Truncated)
	}
}

func TestSampleSeedDeterminism(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 26; i++ {
		write(t, dir, string(rune('a'+i))+".log", 10)
	}
	// The same seed must draw the same sample forever; a different seed
	// must be able to draw a different one (3 of 26 makes a collision
	// astronomically unlikely, and these two seeds are pinned).
	c1, err1 := Scan([]string{dir}, Options{MaxFiles: 3, Seed: 1})
	c2, err2 := Scan([]string{dir}, Options{MaxFiles: 3, Seed: 1})
	c3, err3 := Scan([]string{dir}, Options{MaxFiles: 3, Seed: 2})
	if err1 != nil || err2 != nil || err3 != nil {
		t.Fatal(err1, err2, err3)
	}
	if !reflect.DeepEqual(names(c1), names(c2)) {
		t.Fatalf("same seed drew different samples: %v vs %v", names(c1), names(c2))
	}
	if reflect.DeepEqual(names(c1), names(c3)) {
		t.Fatalf("seeds 1 and 2 drew identical 3-of-26 samples: %v", names(c1))
	}
}

func TestSampleNoBudgetTakesEverything(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.log", 10)
	write(t, dir, "b.log", 20)
	c, err := Scan([]string{dir}, Options{})
	if err != nil || c.SampledBytes != 30 || c.Truncated {
		t.Fatalf("got sampled=%d truncated=%v (%v)", c.SampledBytes, c.Truncated, err)
	}
}

func TestLoadReadsExactlyTake(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "data.log")
	if err := os.WriteFile(p, []byte(strings.Repeat("x", 100)), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Scan([]string{dir}, Options{MaxBytes: 40})
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := Load(c)
	if err != nil || len(blobs) != 1 || len(blobs[0]) != 40 {
		t.Fatalf("got %d blobs, first %d bytes (%v)", len(blobs), len(blobs[0]), err)
	}
}

func TestLoadMissingFileIsAnError(t *testing.T) {
	c := &Corpus{Files: []File{{Path: filepath.Join(t.TempDir(), "gone.log"), Size: 5, Take: 5}}}
	if _, err := Load(c); err == nil {
		t.Fatal("expected an error for a vanished file")
	}
}
