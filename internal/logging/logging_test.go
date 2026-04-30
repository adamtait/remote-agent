package logging

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// --- NewSessionID ---

func TestNewSessionID_Format(t *testing.T) {
	id := NewSessionID()
	parts := strings.SplitN(id, "-", 2)
	if len(parts) != 2 {
		t.Fatalf("expected one '-' separator, got %q", id)
	}
	ms, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		t.Fatalf("unix-ms part %q is not numeric: %v", parts[0], err)
	}
	// Must be a plausible millisecond timestamp (after 2020-01-01).
	if ms < 1577836800000 {
		t.Errorf("unix-ms part %d is too small (before 2020)", ms)
	}
	// Suffix must be exactly 4 hex characters.
	suffix := parts[1]
	if len(suffix) != 4 {
		t.Errorf("hex suffix %q: want 4 chars, got %d", suffix, len(suffix))
	}
	for _, c := range suffix {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("hex suffix %q contains non-hex char %q", suffix, c)
		}
	}
}

// --- TeeHandler ---

// bufHandler is a minimal slog.Handler that records whether Handle was called
// and captures output via a TextHandler on a bytes.Buffer.
type bufHandler struct {
	buf     *bytes.Buffer
	inner   slog.Handler
	minLevel slog.Level
}

func newBufHandler(level slog.Level) *bufHandler {
	buf := &bytes.Buffer{}
	return &bufHandler{
		buf:     buf,
		inner:   slog.NewTextHandler(buf, &slog.HandlerOptions{Level: level}),
		minLevel: level,
	}
}

func (b *bufHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= b.minLevel
}

func (b *bufHandler) Handle(ctx context.Context, r slog.Record) error {
	return b.inner.Handle(ctx, r)
}

func (b *bufHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &bufHandler{buf: b.buf, inner: b.inner.WithAttrs(attrs), minLevel: b.minLevel}
}

func (b *bufHandler) WithGroup(name string) slog.Handler {
	return &bufHandler{buf: b.buf, inner: b.inner.WithGroup(name), minLevel: b.minLevel}
}

func TestTeeHandler_Enabled(t *testing.T) {
	// Both nil → false.
	tee := &TeeHandler{}
	if tee.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("both nil: expected Enabled=false")
	}

	// Terminal at Info, level too low.
	tee2 := &TeeHandler{terminal: newBufHandler(slog.LevelInfo)}
	if tee2.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("terminal@Info, level=Debug: expected Enabled=false")
	}

	// Terminal at Info, level matches.
	if !tee2.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("terminal@Info, level=Info: expected Enabled=true")
	}

	// Only file handler enabled.
	tee3 := &TeeHandler{file: newBufHandler(slog.LevelDebug)}
	if !tee3.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("file@Debug, level=Debug: expected Enabled=true")
	}

	// Terminal too high, file covers it.
	tee4 := &TeeHandler{
		terminal: newBufHandler(slog.LevelWarn),
		file:     newBufHandler(slog.LevelDebug),
	}
	if !tee4.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("terminal@Warn + file@Debug, level=Info: expected Enabled=true")
	}
}

func TestTeeHandler_Handle_WritesBothChildren(t *testing.T) {
	th := newBufHandler(slog.LevelDebug)
	fh := newBufHandler(slog.LevelDebug)
	tee := &TeeHandler{terminal: th, file: fh}

	r := slog.NewRecord(time.Now(), slog.LevelInfo, "hello tee", 0)
	if err := tee.Handle(context.Background(), r); err != nil {
		t.Fatalf("Handle returned error: %v", err)
	}

	if !strings.Contains(th.buf.String(), "hello tee") {
		t.Errorf("terminal handler did not receive record; got: %q", th.buf.String())
	}
	if !strings.Contains(fh.buf.String(), "hello tee") {
		t.Errorf("file handler did not receive record; got: %q", fh.buf.String())
	}
}

func TestTeeHandler_WithAttrs_PropagatesBoth(t *testing.T) {
	th := newBufHandler(slog.LevelDebug)
	fh := newBufHandler(slog.LevelDebug)
	tee := &TeeHandler{terminal: th, file: fh}

	child := tee.WithAttrs([]slog.Attr{slog.String("key", "value")})

	// child must be a *TeeHandler with both children set.
	tc, ok := child.(*TeeHandler)
	if !ok {
		t.Fatalf("WithAttrs returned %T, want *TeeHandler", child)
	}
	if tc.terminal == nil {
		t.Error("WithAttrs: terminal child is nil")
	}
	if tc.file == nil {
		t.Error("WithAttrs: file child is nil")
	}

	// Write a record via the child and verify both buffers captured it.
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "with-attrs-test", 0)
	if err := tc.Handle(context.Background(), r); err != nil {
		t.Fatalf("Handle on WithAttrs child returned error: %v", err)
	}
	if !strings.Contains(th.buf.String(), "with-attrs-test") {
		t.Errorf("terminal handler did not receive WithAttrs record; got: %q", th.buf.String())
	}
	if !strings.Contains(fh.buf.String(), "with-attrs-test") {
		t.Errorf("file handler did not receive WithAttrs record; got: %q", fh.buf.String())
	}
}

func TestTeeHandler_WithGroup_PropagatesBoth(t *testing.T) {
	th := newBufHandler(slog.LevelDebug)
	fh := newBufHandler(slog.LevelDebug)
	tee := &TeeHandler{terminal: th, file: fh}

	child := tee.WithGroup("mygroup")

	tc, ok := child.(*TeeHandler)
	if !ok {
		t.Fatalf("WithGroup returned %T, want *TeeHandler", child)
	}
	if tc.terminal == nil {
		t.Error("WithGroup: terminal child is nil")
	}
	if tc.file == nil {
		t.Error("WithGroup: file child is nil")
	}

	// Write a record with a grouped attr and verify both buffers capture the group prefix.
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "with-group-test", 0)
	r.AddAttrs(slog.String("attr", "val"))
	if err := tc.Handle(context.Background(), r); err != nil {
		t.Fatalf("Handle on WithGroup child returned error: %v", err)
	}
	if !strings.Contains(th.buf.String(), "mygroup") {
		t.Errorf("terminal handler did not capture group prefix; got: %q", th.buf.String())
	}
	if !strings.Contains(fh.buf.String(), "mygroup") {
		t.Errorf("file handler did not capture group prefix; got: %q", fh.buf.String())
	}
}

// --- Step ---

func TestStep_DoneFuncNilError(t *testing.T) {
	var buf bytes.Buffer
	done := Step("test-step", 1, 0, "doing something", &buf)

	if !strings.Contains(buf.String(), "doing something") {
		t.Errorf("Step did not write message to out; got: %q", buf.String())
	}

	// Must not panic.
	done(nil)
}

func TestStep_DoneFuncWithError(t *testing.T) {
	var buf bytes.Buffer
	done := Step("err-step", 2, 0, "failing step", &buf)

	// Must not panic.
	done(errors.New("something went wrong"))
}

func TestStep_ColorCode(t *testing.T) {
	var buf bytes.Buffer
	Step("color-step", 3, 0, "colorful", &buf)
	// The ANSI blue marker must appear in the output.
	if !strings.Contains(buf.String(), "\033[1;34m==>\033[0m") {
		t.Errorf("expected ANSI color code in output; got: %q", buf.String())
	}
}

func TestStep_RendersTotalCounter(t *testing.T) {
	var buf bytes.Buffer
	Step("counter-step", 5, 10, "something", &buf)
	if !strings.Contains(buf.String(), "[5/10] something") {
		t.Errorf("expected '[5/10] something' in output; got: %q", buf.String())
	}
}

func TestStep_OmitsCounterWhenTotalZero(t *testing.T) {
	var buf bytes.Buffer
	Step("plain-step", 1, 0, "something", &buf)
	if strings.Contains(buf.String(), "[1/0]") || strings.Contains(buf.String(), "[1/") {
		t.Errorf("expected no counter prefix when total=0; got: %q", buf.String())
	}
}

// --- OpenLogFile ---

func TestOpenLogFile_CreatesFileAtExpectedPath(t *testing.T) {
	dir := t.TempDir()
	f, closeFn, err := OpenLogFile(dir, "testcmd")
	if err != nil {
		t.Fatalf("OpenLogFile: %v", err)
	}
	defer closeFn()

	name := filepath.Base(f.Name())
	// Must match pattern: YYYY-MM-DDTHHMMSS-testcmd.log
	if !strings.HasSuffix(name, "-testcmd.log") {
		t.Errorf("filename %q: expected suffix -testcmd.log", name)
	}
	// Timestamp prefix must be numeric (basic sanity).
	ts := strings.TrimSuffix(name, "-testcmd.log")
	if len(ts) != len("2006-01-02T150405") {
		t.Errorf("timestamp prefix %q: unexpected length %d", ts, len(ts))
	}

	// Write to file; verify it exists and has content.
	if _, werr := f.WriteString("hello log\n"); werr != nil {
		t.Fatalf("write to log file: %v", werr)
	}
	closeFn() // close before reading

	content, rerr := os.ReadFile(f.Name())
	if rerr != nil {
		t.Fatalf("read log file: %v", rerr)
	}
	if !strings.Contains(string(content), "hello log") {
		t.Errorf("log file content: expected 'hello log'; got: %q", string(content))
	}
}

func TestOpenLogFile_CloseFnIdempotent(t *testing.T) {
	dir := t.TempDir()
	_, closeFn, err := OpenLogFile(dir, "idempotent")
	if err != nil {
		t.Fatalf("OpenLogFile: %v", err)
	}
	// Calling multiple times must not panic.
	closeFn()
	closeFn()
	closeFn()
}

// --- Init (integration smoke test) ---

func TestLogPath_NoFile(t *testing.T) {
	t.Cleanup(func() {
		discard := slog.New(slog.NewTextHandler(io.Discard, nil))
		pkgLogger.Store(discard)
		prevCleanup.Store((func())(nil))
		pkgLogPath.Store(nil)
	})
	cleanup, err := Init(Config{NoFile: true, Command: "test"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer cleanup()
	if got := LogPath(); got != "" {
		t.Errorf("LogPath() with NoFile=true = %q, want empty", got)
	}
}

func TestLogPath_WithFile(t *testing.T) {
	t.Cleanup(func() {
		discard := slog.New(slog.NewTextHandler(io.Discard, nil))
		pkgLogger.Store(discard)
		prevCleanup.Store((func())(nil))
		pkgLogPath.Store(nil)
	})
	dir := t.TempDir()
	path := filepath.Join(dir, "session.log")
	cleanup, err := Init(Config{File: path, Command: "test"})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer cleanup()
	if got := LogPath(); got != path {
		t.Errorf("LogPath() = %q, want %q", got, path)
	}
}

func TestTerminalHandler_DropsBelowWarn(t *testing.T) {
	cases := []struct {
		level slog.Level
		want  bool // true = should appear in terminal output
	}{
		{slog.LevelDebug, false},
		{slog.LevelInfo, false},
		{slog.LevelWarn, true},
		{slog.LevelError, true},
	}
	for _, tc := range cases {
		t.Run(tc.level.String(), func(t *testing.T) {
			buf := new(bytes.Buffer)
			h := newTerminalHandler(buf)
			logger := slog.New(h)
			logger.Log(context.Background(), tc.level, "step_start", "step", "test")
			gotMsg := buf.Len() > 0
			if gotMsg != tc.want {
				t.Errorf("level=%s: wrote=%v, want=%v (output=%q)", tc.level, gotMsg, tc.want, buf.String())
			}
		})
	}
}

func TestInit_SmokeSetsLogger(t *testing.T) {
	t.Cleanup(func() {
		// Restore the package-level logger to a discard logger so this test
		// doesn't interfere with others.
		discard := slog.New(slog.NewTextHandler(io.Discard, nil))
		pkgLogger.Store(discard)
		prevCleanup.Store((func())(nil))
	})

	cfg := Config{
		Level:   slog.LevelDebug,
		NoFile:  true,
		Command: "test",
		Project: "my-project",
	}
	cleanup, err := Init(cfg)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if cleanup == nil {
		t.Fatal("Init returned nil cleanup")
	}
	if L() == nil {
		t.Error("L() returned nil after Init")
	}
	// Cleanup must be idempotent.
	cleanup()
	cleanup()
}
