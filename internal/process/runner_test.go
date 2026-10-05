package process

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRunnerCapturesDiagnosticsAndDirectory(t *testing.T) {
	dir := t.TempDir()
	runner := CommandRunner{Dir: dir}
	result, err := runner.Run(context.Background(), "git", []string{"rev-parse", "--show-toplevel"}, nil)
	if err == nil || result.Stderr == "" || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("diagnostics=%+v err=%v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runner.Run(ctx, "git", []string{"status"}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
}
