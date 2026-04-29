package gcloud

import (
	"bytes"
	"testing"
)

const brokenPipeTraceback = "ERROR: error from callback <function Workstations._AcceptConnection.<locals>.<lambda> at 0x10a212ca0>: [Errno 32] Broken pipe\n" +
	"  File \"/path/websocket/_app.py\", line 345, in _callback\n" +
	"    callback(self, *args)\n" +
	"    ~~~~~~~~^^^^^^^^^^^^^\n" +
	"  File \"/path/workstations.py\", line 323, in <lambda>\n" +
	"    on_data=lambda ws, data, op, finished: client.send(data),\n" +
	"                                           ~~~~~~~~~~~^^^^^^\n"

func TestBrokenPipeFilter_SuppressesEntireTraceback(t *testing.T) {
	var dst bytes.Buffer
	f := newBrokenPipeFilter(&dst)
	f.Write([]byte(brokenPipeTraceback))
	f.Flush()
	if dst.Len() != 0 {
		t.Errorf("expected nothing written, got: %q", dst.String())
	}
}

func TestBrokenPipeFilter_PassesThroughUnrelatedOutput(t *testing.T) {
	var dst bytes.Buffer
	f := newBrokenPipeFilter(&dst)
	input := "Connection to localhost closed.\n"
	f.Write([]byte(input))
	f.Flush()
	if got := dst.String(); got != input {
		t.Errorf("got %q, want %q", got, input)
	}
}

func TestBrokenPipeFilter_PassesThroughOutputBeforeError(t *testing.T) {
	var dst bytes.Buffer
	f := newBrokenPipeFilter(&dst)
	good := "Connection to localhost closed.\n"
	f.Write([]byte(good + brokenPipeTraceback))
	f.Flush()
	if got := dst.String(); got != good {
		t.Errorf("got %q, want %q", got, good)
	}
}

func TestBrokenPipeFilter_ResumesAfterTraceback(t *testing.T) {
	var dst bytes.Buffer
	f := newBrokenPipeFilter(&dst)
	after := "some other output\n"
	f.Write([]byte(brokenPipeTraceback + after))
	f.Flush()
	if got := dst.String(); got != after {
		t.Errorf("got %q, want %q", got, after)
	}
}

func TestBrokenPipeFilter_HandlesPartialWrites(t *testing.T) {
	var dst bytes.Buffer
	f := newBrokenPipeFilter(&dst)
	good := "Connection to localhost closed.\n"
	// Write good output one byte at a time
	for i := 0; i < len(good); i++ {
		f.Write([]byte{good[i]})
	}
	f.Flush()
	if got := dst.String(); got != good {
		t.Errorf("got %q, want %q", got, good)
	}
}
