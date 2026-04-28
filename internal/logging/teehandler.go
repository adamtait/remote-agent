package logging

import (
	"context"
	"errors"
	"log/slog"
)

// TeeHandler is a slog.Handler that forwards every record to two child
// handlers: a terminal handler (filtered to cfg.Level) and a file handler
// (always at Debug).
type TeeHandler struct {
	terminal slog.Handler // may be nil
	file     slog.Handler // may be nil
}

// Enabled returns true if either non-nil child would accept the level.
func (t *TeeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if t.terminal != nil && t.terminal.Enabled(ctx, level) {
		return true
	}
	if t.file != nil && t.file.Enabled(ctx, level) {
		return true
	}
	return false
}

// Handle calls both non-nil children; collects errors with errors.Join.
func (t *TeeHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	if t.terminal != nil && t.terminal.Enabled(ctx, r.Level) {
		if err := t.terminal.Handle(ctx, r); err != nil {
			errs = append(errs, err)
		}
	}
	if t.file != nil && t.file.Enabled(ctx, r.Level) {
		if err := t.file.Handle(ctx, r); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// WithAttrs returns a new TeeHandler with BOTH children updated.
// This is required by the slog.Handler contract.
func (t *TeeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := &TeeHandler{}
	if t.terminal != nil {
		next.terminal = t.terminal.WithAttrs(attrs)
	}
	if t.file != nil {
		next.file = t.file.WithAttrs(attrs)
	}
	return next
}

// WithGroup returns a new TeeHandler with BOTH children updated.
// This is required by the slog.Handler contract.
func (t *TeeHandler) WithGroup(name string) slog.Handler {
	next := &TeeHandler{}
	if t.terminal != nil {
		next.terminal = t.terminal.WithGroup(name)
	}
	if t.file != nil {
		next.file = t.file.WithGroup(name)
	}
	return next
}
