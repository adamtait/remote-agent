package gcloud

import (
	"bytes"
	"io"
	"strings"
)

// brokenPipeFilter drops the benign "Broken pipe" callback error and its
// Python traceback that gcloud prints during normal SSH session teardown,
// passing all other output through to dst unchanged.
type brokenPipeFilter struct {
	dst      io.Writer
	buf      []byte
	suppress bool
}

func newBrokenPipeFilter(dst io.Writer) *brokenPipeFilter {
	return &brokenPipeFilter{dst: dst}
}

func (f *brokenPipeFilter) Write(p []byte) (int, error) {
	f.buf = append(f.buf, p...)
	for {
		idx := bytes.IndexByte(f.buf, '\n')
		if idx < 0 {
			break
		}
		f.writeLine(f.buf[:idx+1])
		f.buf = f.buf[idx+1:]
	}
	return len(p), nil
}

func (f *brokenPipeFilter) writeLine(line []byte) {
	s := string(line)
	if strings.HasPrefix(s, "ERROR: error from callback") && strings.Contains(s, "Broken pipe") {
		f.suppress = true
		return
	}
	if f.suppress {
		if len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
			return
		}
		f.suppress = false
	}
	f.dst.Write(line) //nolint:errcheck
}

// Flush writes any remaining buffered (unterminated) content to dst.
func (f *brokenPipeFilter) Flush() {
	if len(f.buf) > 0 && !f.suppress {
		f.dst.Write(f.buf) //nolint:errcheck
	}
	f.buf = nil
}
