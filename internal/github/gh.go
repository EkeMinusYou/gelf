package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type RepoInfo struct {
	Owner string
	Name  string
}

type PullRequestInfo struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	State   string `json:"state"`
	IsDraft bool   `json:"isDraft"`
}

func AuthToken(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", "auth", "token")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get GitHub auth token: %w", err)
	}

	token := strings.TrimSpace(string(output))
	if token == "" {
		return "", fmt.Errorf("gh auth token returned empty output")
	}

	return token, nil
}

func RepoInfoFromGH(ctx context.Context) (*RepoInfo, error) {
	cmd := exec.CommandContext(ctx, "gh", "repo", "view", "--json", "owner,name")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get repository info: %w", err)
	}

	var result struct {
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		Name string `json:"name"`
	}

	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("failed to parse repository info: %w", err)
	}

	if result.Owner.Login == "" || result.Name == "" {
		return nil, fmt.Errorf("repository info is incomplete")
	}

	return &RepoInfo{
		Owner: result.Owner.Login,
		Name:  result.Name,
	}, nil
}

func FindOpenPullRequest(ctx context.Context, repoFullName, headRef string) (*PullRequestInfo, error) {
	headRef = strings.TrimSpace(headRef)
	if headRef == "" {
		return nil, fmt.Errorf("head ref is empty")
	}

	args := []string{"pr", "list", "--state", "open", "--json", "number,title,url,state,isDraft", "--limit", "1", "--head", headRef}
	if strings.TrimSpace(repoFullName) != "" {
		args = append(args, "--repo", repoFullName)
	}

	cmd := exec.CommandContext(ctx, "gh", args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list pull requests: %w", err)
	}

	var prs []PullRequestInfo
	if err := json.Unmarshal(output, &prs); err != nil {
		return nil, fmt.Errorf("failed to parse pull request list: %w", err)
	}
	if len(prs) == 0 {
		return nil, nil
	}

	return &prs[0], nil
}
