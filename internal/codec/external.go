package codec

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// extArgs builds the argv for one external codec invocation. Every
// invocation is stdin -> stdout, quiet, and pinned to one thread where
// the tool would otherwise auto-parallelize (xz, zstd), so speed numbers
// compare one core against one core.
func extArgs(f Family, level int, decompress bool) []string {
	switch f.Name {
	case "zstd":
		if decompress {
			return []string{"-q", "-d", "-c", "-T1"}
		}
		return []string{"-q", "-c", "-T1", "-" + strconv.Itoa(level)}
	case "xz":
		if decompress {
			return []string{"-q", "-d", "-c", "-T1"}
		}
		return []string{"-q", "-c", "-T1", "-" + strconv.Itoa(level)}
	case "bzip2":
		if decompress {
			return []string{"-q", "-d", "-c"}
		}
		return []string{"-q", "-c", "-" + strconv.Itoa(level)}
	case "lz4":
		if decompress {
			return []string{"-q", "-d", "-c"}
		}
		return []string{"-q", "-c", "-" + strconv.Itoa(level)}
	case "brotli":
		if decompress {
			return []string{"-d", "-c"}
		}
		return []string{"-c", "-q", strconv.Itoa(level)}
	default:
		return nil
	}
}

// runExternal pipes src through one external codec process and returns
// its stdout. Stderr is captured and folded into the error so a broken
// binary produces a diagnosable message instead of silence.
func runExternal(path string, args []string, src []byte) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("external codec binary not resolved")
	}
	cmd := exec.Command(path, args...)
	cmd.Stdin = bytes.NewReader(src)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		if msg != "" {
			return nil, fmt.Errorf("%s: %v: %s", filepath.Base(path), err, msg)
		}
		return nil, fmt.Errorf("%s: %v", filepath.Base(path), err)
	}
	return out.Bytes(), nil
}
