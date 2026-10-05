package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/EkeMinusYou/gelf/internal/process"
)

type Repository struct{ Runner process.Runner }

func NewRepository(dir string) *Repository {
	return &Repository{Runner: process.CommandRunner{Dir: dir}}
}

func (r *Repository) run(ctx context.Context, args ...string) (string, error) {
	result, err := r.Runner.Run(ctx, "git", args, nil)
	return result.Stdout, err
}

func exitCode(err error, code int) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == code
}

func (r *Repository) config(ctx context.Context, key string) (string, error) {
	out, err := r.run(ctx, "config", "--get", key)
	if exitCode(err, 1) {
		return "", nil
	}
	return strings.TrimSpace(out), err
}

func (r *Repository) refSHA(ctx context.Context, ref string) (string, error) {
	out, err := r.run(ctx, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("failed to resolve %s: %w", ref, err)
	}
	return strings.TrimSpace(out), nil
}
