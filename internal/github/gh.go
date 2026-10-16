package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/EkeMinusYou/gelf/internal/ai"
	"github.com/EkeMinusYou/gelf/internal/forge"
	"github.com/EkeMinusYou/gelf/internal/process"
)

type Client struct {
	Runner     process.Runner
	HTTP       *http.Client
	Host       string
	APIBaseURL string
}

func NewClient(runner process.Runner) *Client {
	return &Client{Runner: runner, HTTP: &http.Client{Timeout: 30 * time.Second}, Host: "github.com"}
}

type RepoInfo = forge.RepoInfo
type PullRequestInfo = forge.PullRequestInfo

func RepoInfoFromRemoteURL(remoteURL string) (*RepoInfo, error) {
	info, err := forge.RepoInfoFromRemoteURL(remoteURL)
	if err == nil && info.Host == "ssh.github.com" {
		info.Host = "github.com"
	}
	return info, err
}

func (c *Client) ParseRemoteURL(remoteURL string) (*RepoInfo, error) {
	return RepoInfoFromRemoteURL(remoteURL)
}

// HasHost identifies configured GitHub hosts without exporting authentication tokens.
// Entries remain useful for detection when their credentials are expired or offline.
func (c *Client) HasHost(ctx context.Context, host string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := c.Runner.Run(ctx, "gh", []string{"auth", "status", "--hostname", host, "--active", "--json", "hosts"}, nil)
	if err != nil {
		return false, err
	}
	var data struct {
		Hosts map[string][]json.RawMessage `json:"hosts"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &data); err != nil {
		return false, fmt.Errorf("failed to parse GitHub authentication hosts: %w", err)
	}
	for name, entries := range data.Hosts {
		if strings.EqualFold(name, host) && len(entries) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (c *Client) AuthToken(ctx context.Context) (string, error) {
	result, err := c.Runner.Run(ctx, "gh", []string{"auth", "token", "--hostname", c.Host}, nil)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(result.Stdout)
	if token == "" {
		return "", fmt.Errorf("gh auth token returned empty output")
	}
	return token, nil
}

func (c *Client) Repo(ctx context.Context, name string) (*RepoInfo, *RepoInfo, error) {
	args := []string{"repo", "view"}
	if name != "" {
		args = append(args, name)
	}
	args = append(args, "--json", "owner,name,parent,defaultBranchRef,url")
	result, err := c.Runner.Run(ctx, "gh", args, nil)
	if err != nil {
		return nil, nil, err
	}
	type repoJSON struct {
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		Name             string `json:"name"`
		URL              string `json:"url"`
		DefaultBranchRef *struct {
			Name string `json:"name"`
		} `json:"defaultBranchRef"`
	}
	var data struct {
		repoJSON
		Parent *repoJSON `json:"parent"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &data); err != nil {
		return nil, nil, err
	}
	host := c.Host
	convert := func(data repoJSON) (*RepoInfo, error) {
		if data.Owner.Login == "" || data.Name == "" {
			return nil, fmt.Errorf("repository info is incomplete")
		}
		// gh's parent field contains owner/name but does not include URL or branch data.
		if data.URL == "" {
			data.URL = "https://" + host + "/" + data.Owner.Login + "/" + data.Name
		}
		info, err := RepoInfoFromRemoteURL(data.URL)
		if err != nil {
			return nil, err
		}
		info.Owner, info.Name = data.Owner.Login, data.Name
		if data.DefaultBranchRef != nil {
			info.DefaultBranch = data.DefaultBranchRef.Name
		}
		info.URL = data.URL
		return info, nil
	}
	current, err := convert(data.repoJSON)
	if err != nil {
		return nil, nil, err
	}
	if data.Parent == nil {
		return current, nil, nil
	}
	host = current.Host
	parent, err := convert(*data.Parent)
	return current, parent, err
}

func (c *Client) FindPullRequest(ctx context.Context, base, head RepoInfo, branch string) (*PullRequestInfo, error) {
	if base.Host != head.Host {
		return nil, fmt.Errorf("base and head repositories must use the same GitHub host")
	}
	query := url.Values{"state": {"open"}, "head": {head.Owner + ":" + branch}, "per_page": {"100"}}
	endpoint := "repos/" + base.FullName() + "/pulls?" + query.Encode()
	result, err := c.Runner.Run(ctx, "gh", []string{"api", "--hostname", base.Host, "--paginate", "--slurp", endpoint}, nil)
	if err != nil {
		return nil, err
	}
	var pages [][]PullRequestInfo
	if err := json.Unmarshal([]byte(result.Stdout), &pages); err != nil {
		return nil, fmt.Errorf("failed to parse pull request list: %w", err)
	}
	var match *PullRequestInfo
	for _, page := range pages {
		for _, pr := range page {
			if pr.State != "open" || pr.Head.Ref != branch || pr.Head.Repo == nil || !strings.EqualFold(pr.Head.Repo.FullName, head.FullName()) {
				continue
			}
			if match != nil {
				return nil, fmt.Errorf("multiple open PRs match %s:%s; resolve the ambiguity before creating or updating", head.FullName(), branch)
			}
			copy := pr
			match = &copy
		}
	}
	return match, nil
}

// Create uses the REST API so organization-owned forks and head_repo are explicit.
func (c *Client) Create(ctx context.Context, base, head RepoInfo, branch, baseBranch string, content *ai.PullRequestContent, draft bool) (*PullRequestInfo, error) {
	payload := map[string]any{"title": content.Title, "body": content.Body, "head": head.Owner + ":" + branch, "base": baseBranch, "draft": draft, "maintainer_can_modify": true}
	if !base.Equal(head) {
		payload["head_repo"] = head.Name
	}
	return c.writePR(ctx, base, "POST", "repos/"+base.FullName()+"/pulls", payload)
}

func (c *Client) Update(ctx context.Context, base RepoInfo, number int, content *ai.PullRequestContent) (*PullRequestInfo, error) {
	return c.writePR(ctx, base, "PATCH", fmt.Sprintf("repos/%s/pulls/%d", base.FullName(), number), map[string]any{"title": content.Title, "body": content.Body})
}

func (c *Client) writePR(ctx context.Context, base RepoInfo, method, endpoint string, payload map[string]any) (*PullRequestInfo, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	args := []string{"api", "--hostname", base.Host, "--method", method, "--input", "-", "--header", "Accept: application/vnd.github+json", endpoint}
	result, err := c.Runner.Run(ctx, "gh", args, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	var pr PullRequestInfo
	if err := json.Unmarshal([]byte(result.Stdout), &pr); err != nil {
		return nil, fmt.Errorf("failed to parse PR publication response: %w", err)
	}
	if pr.Number <= 0 {
		return nil, fmt.Errorf("PR publication response has no pull request number")
	}
	if pr.URL == "" {
		pr.URL = fmt.Sprintf("https://%s/%s/pull/%d", base.Host, base.FullName(), pr.Number)
	}
	return &pr, nil
}
