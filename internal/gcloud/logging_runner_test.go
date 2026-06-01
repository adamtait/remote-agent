package gcloud

import (
	"context"
	"log/slog"
	"os/exec"
	"sync"
	"testing"
)

// requireGcloud skips the test if the gcloud binary is not on PATH. The
// integration tests below shell out to gcloud, so on minimal CI runners
// (or dev machines without the SDK) we'd otherwise fail spuriously.
func requireGcloud(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gcloud"); err != nil {
		t.Skip("gcloud not on PATH; skipping integration test")
	}
}

// levelRecorder is a slog.Handler that records the level of every record
// it sees, so tests can assert which severity was used.
type levelRecorder struct {
	mu     sync.Mutex
	levels []slog.Level
}

func (l *levelRecorder) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (l *levelRecorder) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.levels = append(l.levels, r.Level)
	return nil
}
func (l *levelRecorder) WithAttrs(_ []slog.Attr) slog.Handler { return l }
func (l *levelRecorder) WithGroup(_ string) slog.Handler      { return l }

func (l *levelRecorder) hasLevel(want slog.Level) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, lv := range l.levels {
		if lv == want {
			return true
		}
	}
	return false
}

func TestLoggingRunner_RunSoft_DowngradesFailureLog(t *testing.T) {
	requireGcloud(t)
	rec := &levelRecorder{}
	r := NewLoggingRunner(slog.New(rec))
	// "--this-flag-does-not-exist" reliably makes gcloud return a non-zero
	// exit. The test only cares that the runner did not log at Error.
	_ = r.RunSoft("--this-flag-does-not-exist")
	if rec.hasLevel(slog.LevelError) {
		t.Errorf("RunSoft logged at Error; expected Debug-only on failure. Levels: %v", rec.levels)
	}
}

func TestLoggingRunner_Run_LogsErrorOnFailure(t *testing.T) {
	requireGcloud(t)
	rec := &levelRecorder{}
	r := NewLoggingRunner(slog.New(rec))
	_ = r.Run("--this-flag-does-not-exist")
	if !rec.hasLevel(slog.LevelError) {
		t.Errorf("Run did not log at Error on failure. Levels: %v", rec.levels)
	}
}

func TestGcloudSubcmd(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "two plain args",
			args: []string{"services", "enable", "foo.api"},
			want: "services enable",
		},
		{
			name: "flags before subcommands",
			args: []string{"--project", "p", "workstations", "configs", "create"},
			want: "workstations configs",
		},
		{
			name: "empty",
			args: []string{},
			want: "",
		},
		{
			name: "single non-flag arg",
			args: []string{"version"},
			want: "version",
		},
		{
			name: "only flags",
			args: []string{"--help", "--verbosity=debug"},
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := gcloudSubcmd(tc.args)
			if got != tc.want {
				t.Errorf("gcloudSubcmd(%v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}
