// cws/internal/scaffold/prompt.go
package scaffold

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrCancelled is returned when the user cancels input (EOF / Ctrl-D).
var ErrCancelled = errors.New("cancelled")

// Prompt writes a prompt to out, reads one line from in, and returns the
// trimmed value. Empty input returns defaultValue. If validate is non-nil,
// the prompt is repeated until validate returns nil. EOF returns ErrCancelled.
func Prompt(in io.Reader, out io.Writer, label, defaultValue string, validate func(string) error) (string, error) {
	reader := bufio.NewReader(in)
	for {
		if defaultValue != "" {
			fmt.Fprintf(out, "%s [%s]: ", label, defaultValue)
		} else {
			fmt.Fprintf(out, "%s: ", label)
		}

		line, err := reader.ReadString('\n')
		if errors.Is(err, io.EOF) && line == "" {
			return "", ErrCancelled
		}
		// A final line without trailing newline reaches here with err==io.EOF
		// and a non-empty line; treat that as normal input.
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("reading input: %w", err)
		}

		value := strings.TrimSpace(line)
		if value == "" {
			value = defaultValue
		}
		if validate != nil {
			if verr := validate(value); verr != nil {
				fmt.Fprintf(out, "  %v\n", verr)
				continue
			}
		}
		return value, nil
	}
}
