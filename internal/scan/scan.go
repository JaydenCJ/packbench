// Package scan walks the user's real data, applies include/exclude
// filters, and draws a deterministic, seeded sample bounded by a byte
// and file budget. Everything downstream (bench, report) sees only the
// Corpus it produces, so a fixed seed means a fixed sample forever.
package scan

import (
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// vcsDirs are pruned unconditionally: benchmarking .git packfiles tells
// you about git's zlib, not about your data.
var vcsDirs = map[string]bool{".git": true, ".hg": true, ".svn": true}

// Options controls filtering and sampling.
type Options struct {
	Include  []string // glob patterns; empty = everything
	Exclude  []string // glob patterns; exclude wins over include
	MinSize  int64    // skip files smaller than this many bytes
	MaxFiles int      // sample at most this many files (0 = no cap)
	MaxBytes int64    // sample at most this many bytes  (0 = no cap)
	Seed     int64    // sampling seed; same seed, same sample
}

// File is one sampled file. Take is how many leading bytes the bench
// loads — normally Size, smaller only when the byte budget cuts the
// final file short.
type File struct {
	Path string
	Size int64
	Take int64
}

// Corpus is the scan result: the sampled files plus the totals for the
// full matched set, which cost projection extrapolates onto.
type Corpus struct {
	Files        []File
	ScannedFiles int   // files that matched the filters
	ScannedBytes int64 // their total size
	SampledBytes int64 // sum of Take over Files
	Truncated    bool  // true if the last sampled file was cut short
}

// Scan walks each path (file or directory), filters, and samples.
func Scan(paths []string, opt Options) (*Corpus, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("no input paths")
	}
	var matched []File
	seen := map[string]bool{}
	for _, root := range paths {
		info, err := os.Stat(root)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if keep(filepath.Base(root), filepath.Base(root), info.Size(), opt) && !seen[root] {
				seen[root] = true
				matched = append(matched, File{Path: root, Size: info.Size()})
			}
			continue
		}
		err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			base := d.Name()
			if d.IsDir() {
				if vcsDirs[base] {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil // symlinks, sockets, devices: never follow
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				rel = p
			}
			if keep(filepath.ToSlash(rel), base, info.Size(), opt) && !seen[p] {
				seen[p] = true
				matched = append(matched, File{Path: p, Size: info.Size()})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Path < matched[j].Path })

	c := &Corpus{ScannedFiles: len(matched)}
	for _, f := range matched {
		c.ScannedBytes += f.Size
	}
	c.Files = sample(matched, opt, c)
	for i := range c.Files {
		c.SampledBytes += c.Files[i].Take
	}
	if len(c.Files) == 0 {
		return nil, fmt.Errorf("no files matched; check the paths, or loosen --include/--exclude/--min-size")
	}
	return c, nil
}

// keep applies min-size and glob filters. Patterns match either the
// slash-relative path or the bare file name, whichever the user meant.
func keep(rel, base string, size int64, opt Options) bool {
	if size < opt.MinSize {
		return false
	}
	if matchAny(opt.Exclude, rel, base) {
		return false
	}
	if len(opt.Include) == 0 {
		return true
	}
	return matchAny(opt.Include, rel, base)
}

func matchAny(patterns []string, rel, base string) bool {
	for _, pat := range patterns {
		if ok, _ := path.Match(pat, rel); ok {
			return true
		}
		if ok, _ := path.Match(pat, base); ok {
			return true
		}
		// "dir/*" style prefixes should also catch deeper files.
		if strings.HasSuffix(pat, "/*") {
			if strings.HasPrefix(rel, strings.TrimSuffix(pat, "*")) {
				return true
			}
		}
	}
	return false
}

// sample picks files under the budgets. The pick order is a seeded
// shuffle so the sample is unbiased across directories, then the picks
// are re-sorted by path so reports are stable to read. A file that
// overflows the byte budget is truncated rather than skipped, so small
// budgets still measure something.
func sample(matched []File, opt Options, c *Corpus) []File {
	needBytes := opt.MaxBytes > 0 && c.ScannedBytes > opt.MaxBytes
	needFiles := opt.MaxFiles > 0 && len(matched) > opt.MaxFiles
	picked := make([]File, 0, len(matched))
	if !needBytes && !needFiles {
		for _, f := range matched {
			f.Take = f.Size
			picked = append(picked, f)
		}
		return picked
	}
	rnd := rand.New(rand.NewSource(opt.Seed))
	remaining := opt.MaxBytes
	for _, idx := range rnd.Perm(len(matched)) {
		if opt.MaxFiles > 0 && len(picked) >= opt.MaxFiles {
			break
		}
		f := matched[idx]
		f.Take = f.Size
		if opt.MaxBytes > 0 {
			if remaining <= 0 {
				break
			}
			if f.Size > remaining {
				f.Take = remaining
				c.Truncated = true
			}
			remaining -= f.Take
		}
		picked = append(picked, f)
	}
	sort.Slice(picked, func(i, j int) bool { return picked[i].Path < picked[j].Path })
	return picked
}

// Load reads the sampled bytes of every corpus file into memory, in
// corpus order. The byte budget (Options.MaxBytes) is the RAM ceiling.
func Load(c *Corpus) ([][]byte, error) {
	blobs := make([][]byte, 0, len(c.Files))
	for _, f := range c.Files {
		fh, err := os.Open(f.Path)
		if err != nil {
			return nil, err
		}
		buf := make([]byte, f.Take)
		_, err = io.ReadFull(fh, buf)
		fh.Close()
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f.Path, err)
		}
		blobs = append(blobs, buf)
	}
	return blobs, nil
}
