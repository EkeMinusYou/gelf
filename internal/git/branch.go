package git

import (
	"context"
	"fmt"
	"strings"
)

func (r *Repository) Root(ctx context.Context) (string, error) {
	out, err := r.run(ctx, "rev-parse", "--show-toplevel")
	return strings.TrimSpace(out), err
}

func (r *Repository) CurrentBranch(ctx context.Context) (string, error) {
	out, err := r.run(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("a named branch is required (detached HEAD is unsupported): %w", err)
	}
	return strings.TrimSpace(out), nil
}

func (r *Repository) CommittedDiff(ctx context.Context, baseRef, headRef string) (string, error) {
	out, err := r.run(ctx, "--no-pager", "diff", "--no-ext-diff", "--no-color", "-U5", baseRef+"..."+headRef, "--")
	return strings.TrimSpace(out), err
}

func (r *Repository) CommittedDiffStat(ctx context.Context, baseRef, headRef string) (string, error) {
	out, err := r.run(ctx, "--no-pager", "diff", "--no-ext-diff", "--no-color", "--stat", baseRef+"..."+headRef, "--")
	return strings.TrimSpace(out), err
}

func (r *Repository) CommitLog(ctx context.Context, baseRef, headRef string) (string, error) {
	out, err := r.run(ctx, "log", "--reverse", "--format=%h %s", baseRef+".."+headRef, "--")
	return strings.TrimSpace(out), err
}
