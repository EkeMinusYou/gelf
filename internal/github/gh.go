package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
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

type pullRequestListItem struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	State   string `json:"state"`
	IsDraft bool   `json:"isDraft"`

	HeadRefName         string `json:"headRefName"`
	HeadRepositoryOwner struct {
		Login string `json:"login"`
	} `json:"headRepositoryOwner"`
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
	current, parent, err := RepoInfoFromGHWithParent(ctx)
	if err != nil {
		return nil, err
	}
	if parent != nil {
		return parent, nil
	}
	return current, nil
}

func RepoInfoFromGHWithParent(ctx context.Context) (*RepoInfo, *RepoInfo, error) {
	cmd := exec.CommandContext(ctx, "gh", "repo", "view", "--json", "owner,name,parent")
	output, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get repository info: %w", err)
	}

	var result struct {
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		Name   string `json:"name"`
		Parent *struct {
			Owner struct {
				Login string `json:"login"`
			} `json:"owner"`
			Name string `json:"name"`
		} `json:"parent"`
	}

	if err := json.Unmarshal(output, &result); err != nil {
		return nil, nil, fmt.Errorf("failed to parse repository info: %w", err)
	}

	if result.Owner.Login == "" || result.Name == "" {
		return nil, nil, fmt.Errorf("repository info is incomplete")
	}

	current := &RepoInfo{
		Owner: result.Owner.Login,
		Name:  result.Name,
	}

	if result.Parent != nil && result.Parent.Owner.Login != "" && result.Parent.Name != "" {
		parent := &RepoInfo{
			Owner: result.Parent.Owner.Login,
			Name:  result.Parent.Name,
		}
		return current, parent, nil
	}

	return current, nil, nil
}

func FindPullRequest(ctx context.Context, repoFullName, headBranch string, headOwners []string) (*PullRequestInfo, error) {
	headBranch = strings.TrimSpace(headBranch)
	if headBranch == "" {
		return nil, fmt.Errorf("head branch is empty")
	}

	owners := normalizeOwners(headOwners)

	listByHead := func(head string, limit int) ([]pullRequestListItem, error) {
		args := []string{"pr", "list", "--state", "all", "--json", "number,title,url,state,isDraft,headRefName,headRepositoryOwner", "--limit", fmt.Sprintf("%d", limit), "--head", head}
		if strings.TrimSpace(repoFullName) != "" {
			args = append(args, "--repo", repoFullName)
		}

		cmd := exec.CommandContext(ctx, "gh", args...)
		output, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("failed to list pull requests: %w", err)
		}

		var prs []pullRequestListItem
		if err := json.Unmarshal(output, &prs); err != nil {
			return nil, fmt.Errorf("failed to parse pull request list: %w", err)
		}
		return prs, nil
	}

	selectMatch := func(prs []pullRequestListItem, owner string) *PullRequestInfo {
		owner = strings.ToLower(strings.TrimSpace(owner))
		for _, pr := range prs {
			if strings.TrimSpace(pr.HeadRefName) != headBranch {
				continue
			}
			login := strings.ToLower(strings.TrimSpace(pr.HeadRepositoryOwner.Login))
			if owner != "" && login != owner {
				continue
			}
			if owner == "" && len(owners) > 0 && login != "" {
				if _, ok := owners[login]; !ok {
					continue
				}
			}
			return &PullRequestInfo{
				Number:  pr.Number,
				Title:   pr.Title,
				URL:     pr.URL,
				State:   pr.State,
				IsDraft: pr.IsDraft,
			}
		}
		return nil
	}

	for owner := range owners {
		head := fmt.Sprintf("%s:%s", owner, headBranch)
		prs, err := listByHead(head, 5)
		if err != nil {
			return nil, err
		}
		if match := selectMatch(prs, owner); match != nil {
			return match, nil
		}
	}

	prs, err := listByHead(headBranch, 20)
	if err != nil {
		return nil, err
	}
	if match := selectMatch(prs, ""); match != nil {
		return match, nil
	}

	return nil, nil
}

func normalizeOwners(headOwners []string) map[string]struct{} {
	owners := make(map[string]struct{}, len(headOwners))
	for _, owner := range headOwners {
		owner = strings.ToLower(strings.TrimSpace(owner))
		if owner == "" {
			continue
		}
		owners[owner] = struct{}{}
	}
	return owners
}

func RepoInfoFromRemoteURL(remoteURL string) (*RepoInfo, error) {
	remoteURL = strings.TrimSpace(remoteURL)
	if remoteURL == "" {
		return nil, nil
	}

	if strings.Contains(remoteURL, "://") {
		parsed, err := url.Parse(remoteURL)
		if err != nil {
			return nil, nil
		}
		return repoInfoFromPath(parsed.Path), nil
	}

	if idx := strings.Index(remoteURL, ":"); idx != -1 {
		return repoInfoFromPath(remoteURL[idx+1:]), nil
	}

	return nil, nil
}

func repoInfoFromPath(path string) *RepoInfo {
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return nil
	}
	owner := strings.TrimSpace(parts[0])
	repo := strings.TrimSpace(parts[1])
	if owner == "" || repo == "" {
		return nil
	}

	return &RepoInfo{
		Owner: owner,
		Name:  repo,
	}
}

func FindPullRequest(ctx context.Context, repoFullName, headBranch string, headOwners []string) (*PullRequestInfo, error) {
	headBranch = strings.TrimSpace(headBranch)
	if headBranch == "" {
		return nil, fmt.Errorf("head branch is empty")
	}

	owners := normalizeOwners(headOwners)

	listByHead := func(head string, limit int) ([]pullRequestListItem, error) {
		args := []string{"pr", "list", "--state", "all", "--json", "number,title,url,state,isDraft,headRefName,headRepositoryOwner", "--limit", fmt.Sprintf("%d", limit), "--head", head}
		if strings.TrimSpace(repoFullName) != "" {
			args = append(args, "--repo", repoFullName)
		}

		cmd := exec.CommandContext(ctx, "gh", args...)
		output, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("failed to list pull requests: %w", err)
		}

		var prs []pullRequestListItem
		if err := json.Unmarshal(output, &prs); err != nil {
			return nil, fmt.Errorf("failed to parse pull request list: %w", err)
		}
		return prs, nil
	}

	selectMatch := func(prs []pullRequestListItem, owner string) *PullRequestInfo {
		owner = strings.ToLower(strings.TrimSpace(owner))
		for _, pr := range prs {
			if strings.TrimSpace(pr.HeadRefName) != headBranch {
				continue
			}
			login := strings.ToLower(strings.TrimSpace(pr.HeadRepositoryOwner.Login))
			if owner != "" && login != owner {
				continue
			}
			if owner == "" && len(owners) > 0 && login != "" {
				if _, ok := owners[login]; !ok {
					continue
				}
			}
			return &PullRequestInfo{
				Number:  pr.Number,
				Title:   pr.Title,
				URL:     pr.URL,
				State:   pr.State,
				IsDraft: pr.IsDraft,
			}
		}
		return nil
	}

	for owner := range owners {
		head := fmt.Sprintf("%s:%s", owner, headBranch)
		prs, err := listByHead(head, 5)
		if err != nil {
			return nil, err
		}
		if match := selectMatch(prs, owner); match != nil {
			return match, nil
		}
	}

	prs, err := listByHead(headBranch, 20)
	if err != nil {
		return nil, err
	}
	if match := selectMatch(prs, ""); match != nil {
		return match, nil
	}

	return nil, nil
}

func normalizeOwners(headOwners []string) map[string]struct{} {
	owners := make(map[string]struct{}, len(headOwners))
	for _, owner := range headOwners {
		owner = strings.ToLower(strings.TrimSpace(owner))
		if owner == "" {
			continue
		}
		owners[owner] = struct{}{}
	}
	return owners
}

func RepoInfoFromRemoteURL(remoteURL string) (*RepoInfo, error) {
	remoteURL = strings.TrimSpace(remoteURL)
	if remoteURL == "" {
		return nil, nil
	}

	if strings.Contains(remoteURL, "://") {
		parsed, err := url.Parse(remoteURL)
		if err != nil {
			return nil, nil
		}
		return repoInfoFromPath(parsed.Path), nil
	}

	if idx := strings.Index(remoteURL, ":"); idx != -1 {
		return repoInfoFromPath(remoteURL[idx+1:]), nil
	}

	return nil, nil
}

func repoInfoFromPath(path string) *RepoInfo {
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return nil
	}
	owner := strings.TrimSpace(parts[0])
	repo := strings.TrimSpace(parts[1])
	if owner == "" || repo == "" {
		return nil
	}

	return &RepoInfo{
		Owner: owner,
		Name:  repo,
	}
}
