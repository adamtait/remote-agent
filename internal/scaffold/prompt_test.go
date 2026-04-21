// cws/internal/scaffold/prompt_test.go
package scaffold_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/scaffold"
)

func TestPrompt_ReturnsInput(t *testing.T) {
	in := strings.NewReader("my-value\n")
	var out bytes.Buffer
	got, err := scaffold.Prompt(in, &out, "Label", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "my-value" {
		t.Errorf("got %q, want %q", got, "my-value")
	}
}

func TestPrompt_EmptyInputReturnsDefault(t *testing.T) {
	in := strings.NewReader("\n")
	var out bytes.Buffer
	got, err := scaffold.Prompt(in, &out, "Label", "dflt", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "dflt" {
		t.Errorf("got %q, want %q", got, "dflt")
	}
}

func TestPrompt_RepromptsOnValidationFailure(t *testing.T) {
	in := strings.NewReader("bad\ngood\n")
	var out bytes.Buffer
	validate := func(s string) error {
		if s != "good" {
			return fmt.Errorf("not good enough")
		}
		return nil
	}
	got, err := scaffold.Prompt(in, &out, "Label", "", validate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "good" {
		t.Errorf("got %q, want %q", got, "good")
	}
	if !strings.Contains(out.String(), "not good enough") {
		t.Errorf("expected validation error in output, got: %q", out.String())
	}
}

func TestPrompt_EOFReturnsErrCancelled(t *testing.T) {
	in := strings.NewReader("")
	var out bytes.Buffer
	_, err := scaffold.Prompt(in, &out, "Label", "", nil)
	if !errors.Is(err, scaffold.ErrCancelled) {
		t.Errorf("got %v, want ErrCancelled", err)
	}
}

func TestPrompt_DefaultShownInPromptString(t *testing.T) {
	in := strings.NewReader("\n")
	var out bytes.Buffer
	_, err := scaffold.Prompt(in, &out, "Label", "us-central1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "[us-central1]") {
		t.Errorf("expected [us-central1] in prompt output, got: %q", out.String())
	}
}
