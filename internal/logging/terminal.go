package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
)

// StepPrintf writes the colored "==>" step marker to out AND logs
// step_start at Info via L(). Used internally by Step(); also exported
// so cmd packages can call it directly if needed.
func StepPrintf(out io.Writer, stepName string, msg string) {
	fmt.Fprintf(out, "\033[1;34m==>\033[0m %s\n", msg)
	L().LogAttrs(context.Background(), slog.LevelInfo, "step_start",
		slog.String("step", stepName),
	)
}

// WarnPrintf writes a yellow "WARN:" line to out AND logs at Warn via L().
func WarnPrintf(out io.Writer, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(out, "\033[1;33mWARN:\033[0m %s\n", msg)
	L().Warn(msg)
}
