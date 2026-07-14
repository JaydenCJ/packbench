package codec

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/lzw"
	"compress/zlib"
	"fmt"
	"io"
)

// builtinCompress encodes src with one of the standard-library codecs.
// Every writer is fully closed before the buffer is read: DEFLATE
// writers hold back the final block until Close.
func builtinCompress(name string, level int, src []byte) ([]byte, error) {
	switch name {
	case "store":
		return append([]byte(nil), src...), nil
	case "gzip":
		var buf bytes.Buffer
		w, err := gzip.NewWriterLevel(&buf, level)
		if err != nil {
			return nil, err
		}
		return finishWrite(&buf, w, src)
	case "zlib":
		var buf bytes.Buffer
		w, err := zlib.NewWriterLevel(&buf, level)
		if err != nil {
			return nil, err
		}
		return finishWrite(&buf, w, src)
	case "flate":
		var buf bytes.Buffer
		w, err := flate.NewWriter(&buf, level)
		if err != nil {
			return nil, err
		}
		return finishWrite(&buf, w, src)
	case "lzw":
		var buf bytes.Buffer
		w := lzw.NewWriter(&buf, lzw.LSB, 8)
		return finishWrite(&buf, w, src)
	default:
		return nil, fmt.Errorf("no builtin codec %q", name)
	}
}

// builtinDecompress decodes data produced by builtinCompress.
func builtinDecompress(name string, src []byte) ([]byte, error) {
	switch name {
	case "store":
		return append([]byte(nil), src...), nil
	case "gzip":
		r, err := gzip.NewReader(bytes.NewReader(src))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return io.ReadAll(r)
	case "zlib":
		r, err := zlib.NewReader(bytes.NewReader(src))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return io.ReadAll(r)
	case "flate":
		r := flate.NewReader(bytes.NewReader(src))
		defer r.Close()
		return io.ReadAll(r)
	case "lzw":
		r := lzw.NewReader(bytes.NewReader(src), lzw.LSB, 8)
		defer r.Close()
		return io.ReadAll(r)
	default:
		return nil, fmt.Errorf("no builtin codec %q", name)
	}
}

func finishWrite(buf *bytes.Buffer, w io.WriteCloser, src []byte) ([]byte, error) {
	if _, err := w.Write(src); err != nil {
		w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
