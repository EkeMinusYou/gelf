package process

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

type Result struct {
	Stdout string
	Stderr string
}

type Runner interface {
	Run(context.Context, string, []string, io.Reader) (Result, error)
}

type CommandRunner struct {
	Dir string
}

func (r CommandRunner) Run(ctx context.Context, name string, args []string, stdin io.Reader) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = r.Dir
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, fmt.Errorf("%s failed: %w%s", name, err, errorDetails(result))
	}
	return result, nil
}

func errorDetails(result Result) string {
	details := strings.TrimSpace(result.Stderr)
	if details == "" {
		details = strings.TrimSpace(result.Stdout)
	}
	if details == "" {
		return ""
	}
	return "\n" + details
}
