package testutil

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EkeMinusYou/gelf/internal/process"
)

type RunnerFunc func(context.Context, string, []string, io.Reader) (process.Result, error)

func (f RunnerFunc) Run(ctx context.Context, name string, args []string, input io.Reader) (process.Result, error) {
	return f(ctx, name, args, input)
}

func Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	options := []string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}
	cmd := exec.Command("git", append(options, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func Repo(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	dir := t.TempDir()
	Git(t, dir, "init", "-q", "-b", "main")
	Git(t, dir, "config", "user.name", "Test")
	Git(t, dir, "config", "user.email", "test@example.invalid")
	Git(t, dir, "config", "commit.gpgsign", "false")
	Git(t, dir, "config", "core.hooksPath", "/dev/null")
	Git(t, dir, "commit", "--allow-empty", "-qm", "initial")
	return dir
}

func Write(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
