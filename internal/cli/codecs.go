package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/JaydenCJ/packbench/internal/codec"
)

// runCodecs implements `packbench codecs`: the catalogue, annotated with
// what this machine can actually run and where each binary lives.
func runCodecs(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("codecs", stderr)
	noExternal := fs.Bool("no-external", false, "report as if external binaries were unavailable")
	if code, done := parseFlags(fs, args); done {
		return code
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(stderr, "packbench codecs: takes no arguments")
		return ExitUsage
	}
	reg := &codec.Registry{DisableExternal: *noExternal}

	grid := [][]string{{"CODEC", "LEVELS", "DEFAULT", "AUTO", "SOURCE", "STATUS"}}
	var notes []string
	for _, st := range reg.Statuses() {
		f := st.Family
		levels, def := "-", "-"
		if f.Leveled {
			levels = fmt.Sprintf("%d-%d", f.Min, f.Max)
			def = fmt.Sprintf("%d", f.Default)
		}
		auto := make([]string, len(f.Auto))
		for i, l := range f.Auto {
			auto[i] = fmt.Sprintf("%d", l)
		}
		source, status := "builtin", "available"
		if f.Bin != "" {
			source = f.Bin + " bin"
			if st.Available {
				status = st.Path
			} else {
				status = "not found"
			}
		}
		grid = append(grid, []string{f.Name, levels, def, strings.Join(auto, ","), source, status})
		notes = append(notes, fmt.Sprintf("%-7s %s", f.Name, f.Note))
	}

	widths := make([]int, len(grid[0]))
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
			b.WriteString(cell + strings.Repeat(" ", widths[i]-len(cell)))
		}
		fmt.Fprintln(stdout, strings.TrimRight(b.String(), " "))
	}
	fmt.Fprintln(stdout)
	for _, n := range notes {
		fmt.Fprintln(stdout, n)
	}
	return ExitOK
}
