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

type RepoInfo struct {
	Host          string
	Owner         string
	Name          string
	DefaultBranch string
	URL           string
}

func (r RepoInfo) FullName() string { return r.Owner + "/" + r.Name }
func (r RepoInfo) GHName() string   { return r.Host + "/" + r.FullName() }
func (r RepoInfo) Equal(other RepoInfo) bool {
	return strings.EqualFold(r.Host, other.Host) && strings.EqualFold(r.FullName(), other.FullName())
}

type PullRequestInfo struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	URL     string `json:"html_url"`
	State   string `json:"state"`
	IsDraft bool   `json:"draft"`
	Head    struct {
		Ref  string `json:"ref"`
		Repo *struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
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

func RepoInfoFromRemoteURL(remoteURL string) (*RepoInfo, error) {
	remoteURL = strings.TrimSpace(remoteURL)
	var host, path string
	if strings.Contains(remoteURL, "://") {
		parsed, err := url.Parse(remoteURL)
		if err != nil {
			return nil, err
		}
		host, path = parsed.Hostname(), parsed.Path
	} else if at := strings.LastIndex(remoteURL, "@"); at >= 0 {
		if colon := strings.Index(remoteURL[at+1:], ":"); colon >= 0 {
			host = remoteURL[at+1 : at+1+colon]
			path = remoteURL[at+2+colon:]
		}
	} else if colon := strings.Index(remoteURL, ":"); colon >= 0 {
		host, path = remoteURL[:colon], remoteURL[colon+1:]
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	parts := strings.Split(path, "/")
	if host == "" || len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("unsupported GitHub remote URL %q", remoteURL)
	}
	return &RepoInfo{Host: strings.ToLower(host), Owner: parts[0], Name: parts[1]}, nil
}
