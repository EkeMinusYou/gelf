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
