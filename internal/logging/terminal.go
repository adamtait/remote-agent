package logging

import (
	"fmt"
	"io"
)

// StepPrintf prints the colored "==>" step marker to out. Does not log —
// use Step for structured logging with timing and step_num.
func StepPrintf(out io.Writer, msg string) {
	fmt.Fprintf(out, "\033[1;34m==>\033[0m %s\n", msg)
}

// WarnPrintf writes a yellow "WARN:" line to out AND logs at Warn via L().
func WarnPrintf(out io.Writer, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(out, "\033[1;33mWARN:\033[0m %s\n", msg)
	L().Warn(msg)
}
