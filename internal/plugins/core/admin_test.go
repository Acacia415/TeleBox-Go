package core

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/Acacia415/TeleBox-Go/internal/toolrunner"
)

func TestCoreLogLevel(t *testing.T) {
	t.Parallel()

	level, name, ok := coreLogLevel("warn")
	if !ok || level != slog.LevelWarn || name != "warning" {
		t.Fatalf("coreLogLevel() = %v, %q, %v", level, name, ok)
	}
	if _, _, ok := coreLogLevel("verbose"); ok {
		t.Fatal("invalid level was accepted")
	}
}

func TestFormatShellResultIncludesNonZeroFailure(t *testing.T) {
	t.Parallel()

	got := formatShellResult(toolrunner.Result{
		Stdout:   "output",
		Stderr:   "failure",
		ExitCode: 2,
	}, errors.New("exit status 2"))
	for _, expected := range []string{"output", "failure", "exit status 2", "退出码：2"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("formatShellResult() = %q, missing %q", got, expected)
		}
	}
}
