package git

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
)

func (r *Repository) RemoteURL(ctx context.Context, remote string, push bool) (string, error) {
	args := []string{"remote", "get-url"}
	if push {
		args = append(args, "--push", "--all")
	}
	out, err := r.run(ctx, append(args, remote)...)
	if err != nil {
		return "", err
	}
	if push && len(strings.Split(strings.TrimSpace(out), "\n")) != 1 {
		return "", fmt.Errorf("remote %s has multiple push URLs; select a single PR head destination", remote)
	}
	url := strings.TrimSpace(out)
	if url == "" {
		return "", fmt.Errorf("remote URL for %s is empty", remote)
	}
	return url, nil
}

func (r *Repository) Remotes(ctx context.Context) ([]string, error) {
	out, err := r.run(ctx, "remote")
	return strings.Fields(out), err
}

// FetchBase also works with a repository URL when no matching remote is configured.
// It updates an isolated ref and returns a fixed commit for the whole generation flow.
func (r *Repository) FetchBase(ctx context.Context, remote, branch string) (string, error) {
	ref := cachedRef("base", remote, branch)
	if _, err := r.run(ctx, "check-ref-format", "refs/heads/"+branch); err != nil {
		return "", err
	}
	if _, err := r.run(ctx, "fetch", "--no-tags", "--no-write-fetch-head", "--", remote, "+refs/heads/"+branch+":"+ref); err != nil {
		return "", fmt.Errorf("failed to fetch PR base: %w", err)
	}
	return r.refSHA(ctx, ref)
}

func cachedRef(kind, remote, branch string) string {
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(remote)))
	return "refs/gelf/" + kind + "/" + key + "/" + branch
}
