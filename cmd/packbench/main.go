// Command packbench benchmarks compression codecs and levels on your
// actual data: ratio, speed, and storage-cost projections in one
// deterministic report.
package main

import (
	"os"

	"github.com/JaydenCJ/packbench/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
