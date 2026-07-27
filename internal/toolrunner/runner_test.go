package toolrunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRunnerCapturesAndLimitsOutput(t *testing.T) {
	t.Parallel()

	runner, err := New(1)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), Command{
		Name:      os.Args[0],
		Args:      []string{"-test.run=TestRunnerHelperProcess", "--", "output"},
		Env:       []string{"GO_WANT_HELPER_PROCESS=1"},
		MaxOutput: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "0123" || !result.StdoutTruncated {
		t.Fatalf("stdout = %q truncated=%v", result.Stdout, result.StdoutTruncated)
	}
	if result.Stderr != "abcd" || !result.StderrTruncated {
		t.Fatalf("stderr = %q truncated=%v", result.Stderr, result.StderrTruncated)
	}
}

func TestRunnerTimeout(t *testing.T) {
	t.Parallel()

	runner, err := New(1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.Run(context.Background(), Command{
		Name:    os.Args[0],
		Args:    []string{"-test.run=TestRunnerHelperProcess", "--", "wait"},
		Env:     []string{"GO_WANT_HELPER_PROCESS=1"},
		Timeout: 20 * time.Millisecond,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want DeadlineExceeded", err)
	}
}

func TestRunnerHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	separator := 0
	for index, arg := range os.Args {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator == 0 || separator+1 >= len(os.Args) {
		os.Exit(2)
	}
	switch os.Args[separator+1] {
	case "output":
		fmt.Fprint(os.Stdout, strings.Repeat("0123456789", 4))
		fmt.Fprint(os.Stderr, strings.Repeat("abcdefghij", 4))
		os.Exit(0)
	case "wait":
		time.Sleep(5 * time.Second)
		os.Exit(0)
	default:
		os.Exit(2)
	}
}
