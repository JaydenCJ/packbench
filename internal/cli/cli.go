// Package cli implements the packbench command-line interface. Run takes
// argv and two writers and returns an exit code, so the entire surface is
// testable in-process without building a binary.
package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/JaydenCJ/packbench/internal/version"
)

// Exit codes. Documented in the README; `run` uses ExitFindings as its
// machine-readable verdict when a codec fails or flunks verification.
const (
	ExitOK       = 0
	ExitFindings = 1
	ExitUsage    = 2
	ExitRuntime  = 3
)

// Run dispatches argv and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stdout)
		return ExitOK
	}
	switch args[0] {
	case "run":
		return runBench(args[1:], stdout, stderr)
	case "codecs":
		return runCodecs(args[1:], stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "packbench %s\n", version.Version)
		return ExitOK
	case "help", "--help", "-h":
		usage(stdout)
		return ExitOK
	default:
		fmt.Fprintf(stderr, "packbench: unknown command %q\n\n", args[0])
		usage(stderr)
		return ExitUsage
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `packbench — benchmark compression codecs and levels on your actual data.

Usage:
  packbench run [flags] PATH...
  packbench codecs [--no-external]
  packbench version

Commands:
  run      scan PATH..., draw a seeded sample, and benchmark every
           selected codec: ratio, speed, and optional cost projection
  codecs   list the codec catalogue and what this machine can run
  version  print the version

Run 'packbench run -h' for the full flag list.

Exit codes: 0 ok, 1 a codec failed or flunked round-trip verification,
2 usage error, 3 runtime error.
`)
}

// newFlagSet builds a FlagSet whose -h output goes to stderr.
func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// parseFlags parses and maps flag errors to the usage exit code.
// The boolean is true when the caller should return code immediately.
func parseFlags(fs *flag.FlagSet, args []string) (code int, done bool) {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return ExitOK, true
		}
		return ExitUsage, true
	}
	return 0, false
}

// stringsFlag collects a repeatable string flag (--include '*.log').
type stringsFlag []string

func (f *stringsFlag) String() string { return strings.Join(*f, ",") }

func (f *stringsFlag) Set(v string) error {
	if strings.TrimSpace(v) == "" {
		return fmt.Errorf("empty pattern")
	}
	*f = append(*f, strings.TrimSpace(v))
	return nil
}
