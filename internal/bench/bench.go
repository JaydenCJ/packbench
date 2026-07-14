// Package bench runs every selected codec over the sampled blobs and
// records sizes, wall time, and round-trip verification. The clock is
// injectable, so speed math is unit-testable without real sleeps.
package bench

import (
	"bytes"
	"time"

	"github.com/JaydenCJ/packbench/internal/codec"
)

// Result is the raw measurement for one codec over the whole sample.
type Result struct {
	Codec        codec.Codec
	InBytes      int64
	OutBytes     int64
	CompressNS   int64
	DecompressNS int64
	Verified     bool // round-trip output was byte-identical to input
	Err          string
}

// Ratio is compressed/original — smaller is better; store is 1.0.
func (r Result) Ratio() float64 {
	if r.InBytes == 0 {
		return 1
	}
	return float64(r.OutBytes) / float64(r.InBytes)
}

// SavedPct is the percentage of bytes shed relative to the original.
func (r Result) SavedPct() float64 { return (1 - r.Ratio()) * 100 }

// CompressMBps is compression throughput in decimal MB/s of input.
func (r Result) CompressMBps() float64 { return mbps(r.InBytes, r.CompressNS) }

// DecompressMBps is decompression throughput in decimal MB/s of output.
func (r Result) DecompressMBps() float64 { return mbps(r.InBytes, r.DecompressNS) }

// SavedMBps is bytes shed per second spent compressing — the "savings
// velocity" that powers the balanced recommendation: how much storage a
// CPU-second of this codec buys you.
func (r Result) SavedMBps() float64 { return mbps(r.InBytes-r.OutBytes, r.CompressNS) }

func mbps(n int64, ns int64) float64 {
	if ns <= 0 || n < 0 {
		return 0
	}
	return float64(n) / 1e6 / (float64(ns) / 1e9)
}

// Options controls a run.
type Options struct {
	Concat bool // treat the sample as one solid archive instead of per-file
	Verify bool // decompress and byte-compare (also produces decompress timing)
}

// Runner executes benchmarks. Now defaults to time.Now; tests inject a
// stepping fake to make throughput math deterministic.
type Runner struct {
	Now func() time.Time
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Run measures every codec against blobs. A codec that fails on any
// blob gets Err set and drops out of recommendations, but never aborts
// the run — the rest of the report still prints.
func (r *Runner) Run(blobs [][]byte, codecs []codec.Codec, opt Options) []Result {
	if opt.Concat {
		blobs = concat(blobs)
	}
	var total int64
	for _, b := range blobs {
		total += int64(len(b))
	}
	results := make([]Result, 0, len(codecs))
	for _, c := range codecs {
		results = append(results, r.runOne(c, blobs, total, opt))
	}
	return results
}

func (r *Runner) runOne(c codec.Codec, blobs [][]byte, total int64, opt Options) Result {
	// Verified starts false and only flips once a round trip actually
	// passed — with --no-verify the report says false honestly instead
	// of vouching for a check that never ran.
	res := Result{Codec: c, InBytes: total}
	packed := make([][]byte, 0, len(blobs))
	for _, b := range blobs {
		start := r.now()
		out, err := c.Compress(b)
		res.CompressNS += r.now().Sub(start).Nanoseconds()
		if err != nil {
			res.Err = err.Error()
			return res
		}
		res.OutBytes += int64(len(out))
		packed = append(packed, out)
	}
	if !opt.Verify {
		return res
	}
	res.Verified = true
	for i, p := range packed {
		start := r.now()
		back, err := c.Decompress(p)
		res.DecompressNS += r.now().Sub(start).Nanoseconds()
		if err != nil {
			res.Err = "decompress: " + err.Error()
			res.Verified = false
			return res
		}
		if !bytes.Equal(back, blobs[i]) {
			res.Err = "round-trip mismatch: decompressed bytes differ from input"
			res.Verified = false
			return res
		}
	}
	return res
}

// concat merges all blobs into one, modelling a solid archive
// (tar-then-compress) instead of per-object compression.
func concat(blobs [][]byte) [][]byte {
	var n int
	for _, b := range blobs {
		n += len(b)
	}
	one := make([]byte, 0, n)
	for _, b := range blobs {
		one = append(one, b...)
	}
	return [][]byte{one}
}
