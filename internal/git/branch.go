package git

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"
)

func GetRepoRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get repository root: %w", err)
	}

	root := strings.TrimSpace(string(output))
	if root == "" {
		return "", fmt.Errorf("repository root is empty")
	}

	return root, nil
}

func GetCurrentBranch() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get current branch: %w", err)
	}

	branch := strings.TrimSpace(string(output))
	if branch == "" {
		return "", fmt.Errorf("current branch is empty")
	}

	return branch, nil
}

func GetDefaultBaseBranch() (string, error) {
	if branch, err := originHeadBranch(); err == nil && branch != "" {
		return branch, nil
	}

	branch, err := remoteShowHeadBranch()
	if err != nil {
		return "", err
	}

	if branch == "" {
		return "", fmt.Errorf("failed to determine base branch")
	}

	return branch, nil
}

func originHeadBranch() (string, error) {
	cmd := exec.Command("git", "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	ref := strings.TrimSpace(string(output))
	if ref == "" {
		return "", fmt.Errorf("origin/HEAD is empty")
	}

	if strings.HasPrefix(ref, "origin/") {
		return strings.TrimPrefix(ref, "origin/"), nil
	}

	return "", fmt.Errorf("unexpected origin/HEAD ref: %s", ref)
}

func remoteShowHeadBranch() (string, error) {
	cmd := exec.Command("git", "remote", "show", "origin")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to inspect origin remote: %w", err)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	const prefix = "HEAD branch: "
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix)), nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	return "", fmt.Errorf("HEAD branch not found in origin remote info")
}

func GetCommittedDiff(baseRef, headRef string) (string, error) {
	cmd := exec.Command("git", "--no-pager", "diff", "-U5", fmt.Sprintf("%s...%s", baseRef, headRef))
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

func GetCommittedDiffStat(baseRef, headRef string) (string, error) {
	cmd := exec.Command("git", "--no-pager", "diff", "--stat", fmt.Sprintf("%s...%s", baseRef, headRef))
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

func GetCommitLog(baseRef, headRef string) (string, error) {
	rangeSpec := fmt.Sprintf("%s..%s", baseRef, headRef)
	cmd := exec.Command("git", "log", "--reverse", "--format=%h %s", rangeSpec)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}
