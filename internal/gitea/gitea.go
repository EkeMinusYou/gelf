package gitea

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/EkeMinusYou/gelf/internal/ai"
	"github.com/EkeMinusYou/gelf/internal/forge"
	"github.com/EkeMinusYou/gelf/internal/process"
)

type Client struct {
	Runner  process.Runner
	Login   string
	base    *url.URL
	sshHost string
}

var _ forge.Client = (*Client)(nil)

var ErrNoMatchingLogin = errors.New("no matching tea login for the push remote")
var ErrAmbiguousLogin = errors.New("multiple tea logins match the push remote")
var ErrLoginSelection = errors.New("configured tea login does not match the push remote")

func NewClient(runner process.Runner) *Client { return &Client{Runner: runner} }

// ResolveRemote selects a tea login from public login metadata, never reading tokens.
func (c *Client) ResolveRemote(ctx context.Context, remoteURL, loginName string) (*forge.RepoInfo, error) {
	result, err := c.Runner.Run(ctx, "tea", []string{"logins", "list", "--output", "json"}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list tea logins: %w", err)
	}
	var logins []struct {
		Name    string `json:"name"`
		URL     string `json:"url"`
		SSHHost string `json:"ssh_host"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &logins); err != nil {
		return nil, fmt.Errorf("failed to parse tea logins: %w", err)
	}
	var match *Client
	matchingServer := false
	for _, login := range logins {
		if login.Name == "" {
			continue
		}
		base, err := url.Parse(strings.TrimRight(login.URL, "/"))
		if err != nil || base.Hostname() == "" || (base.Scheme != "https" && base.Scheme != "http") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
			continue
		}
		candidate := *c
		candidate.Login, candidate.base, candidate.sshHost = login.Name, base, login.SSHHost
		if _, err := candidate.ParseRemoteURL(remoteURL); err != nil {
			continue
		}
		matchingServer = true
		if loginName != "" && login.Name != loginName {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("%w; set gitea.login to select one", ErrAmbiguousLogin)
		}
		match = &candidate
	}
	if match == nil {
		if matchingServer {
			return nil, fmt.Errorf("%w; check gitea.login", ErrLoginSelection)
		}
		return nil, fmt.Errorf("%w; run tea login add or check gitea.login", ErrNoMatchingLogin)
	}
	*c = *match
	return c.ParseRemoteURL(remoteURL)
}

func (c *Client) ParseRemoteURL(remoteURL string) (*forge.RepoInfo, error) {
	if c.base == nil {
		return nil, fmt.Errorf("gitea remote must be resolved before use")
	}
	parsed, err := url.Parse(remoteURL)
	if err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		if !strings.EqualFold(parsed.Scheme, c.base.Scheme) || !strings.EqualFold(parsed.Host, c.base.Host) || !strings.HasPrefix(parsed.Path, strings.TrimRight(c.base.Path, "/")+"/") {
			return nil, fmt.Errorf("remote URL does not belong to the configured Gitea server")
		}
		parsed.Path = strings.TrimPrefix(parsed.Path, strings.TrimRight(c.base.Path, "/"))
		parsed.RawPath = ""
		remoteURL = parsed.String()
	}
	info, err := forge.RepoInfoFromRemoteURL(remoteURL)
	if err != nil {
		return nil, err
	}
	sshURL := &url.URL{Host: c.sshHost}
	if !strings.EqualFold(info.Host, c.base.Host) && !strings.EqualFold(info.Host, c.base.Hostname()) && !strings.EqualFold(info.Host, sshURL.Hostname()) {
		return nil, fmt.Errorf("remote host does not match the configured Gitea server; check the SSH host in the tea login")
	}
	// SSH ports describe Git transport, not the API server identity.
	info.Host = strings.ToLower(c.base.Host)
	return info, nil
}

type repository struct {
	Owner struct {
		Login string `json:"login"`
	} `json:"owner"`
	Name          string      `json:"name"`
	DefaultBranch string      `json:"default_branch"`
	HTMLURL       string      `json:"html_url"`
	CloneURL      string      `json:"clone_url"`
	Parent        *repository `json:"parent"`
}

func (c *Client) Repo(ctx context.Context, name string) (*forge.RepoInfo, *forge.RepoInfo, error) {
	name = strings.TrimPrefix(name, c.base.Host+"/")
	parts := strings.Split(name, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, nil, fmt.Errorf("invalid Gitea repository name")
	}
	var data repository
	if err := c.request(ctx, http.MethodGet, "repos/"+url.PathEscape(parts[0])+"/"+url.PathEscape(parts[1]), nil, &data); err != nil {
		return nil, nil, err
	}
	convert := func(data repository) (*forge.RepoInfo, error) {
		if data.Owner.Login == "" || data.Name == "" {
			return nil, fmt.Errorf("gitea repository info is incomplete")
		}
		return &forge.RepoInfo{Host: strings.ToLower(c.base.Host), Owner: data.Owner.Login, Name: data.Name, DefaultBranch: data.DefaultBranch, URL: data.HTMLURL, CloneURL: data.CloneURL}, nil
	}
	current, err := convert(data)
	if err != nil || data.Parent == nil {
		return current, nil, err
	}
	parent, err := convert(*data.Parent)
	return current, parent, err
}

func repoPath(repo forge.RepoInfo) string {
	return "repos/" + url.PathEscape(repo.Owner) + "/" + url.PathEscape(repo.Name)
}

func (c *Client) FindPullRequest(ctx context.Context, base, head forge.RepoInfo, branch string) (*forge.PullRequestInfo, error) {
	if !strings.EqualFold(base.Host, head.Host) {
		return nil, fmt.Errorf("base and head repositories must use the same Gitea server")
	}
	var match *forge.PullRequestInfo
	for page := 1; ; page++ {
		var prs []forge.PullRequestInfo
		endpoint := fmt.Sprintf("%s/pulls?state=open&limit=100&page=%d", repoPath(base), page)
		if err := c.request(ctx, http.MethodGet, endpoint, nil, &prs); err != nil {
			return nil, err
		}
		if len(prs) == 0 {
			return match, nil
		}
		for _, pr := range prs {
			if pr.State != "open" || pr.Head.Ref != branch || pr.Head.Repo == nil || !strings.EqualFold(pr.Head.Repo.FullName, head.FullName()) {
				continue
			}
			if match != nil {
				return nil, fmt.Errorf("multiple open PRs match %s:%s; resolve the ambiguity before creating or updating", head.FullName(), branch)
			}
			match = &pr
		}
	}
}

func (c *Client) Create(ctx context.Context, base, head forge.RepoInfo, branch, baseBranch string, content *ai.PullRequestContent, draft bool) (*forge.PullRequestInfo, error) {
	title := content.Title
	if draft {
		title = draftTitle(title)
	}
	headRef := branch
	if !base.Equal(head) {
		// Gitea accepts owner/repo:branch, including renamed forks.
		headRef = head.FullName() + ":" + branch
	}
	return c.writePR(ctx, http.MethodPost, repoPath(base)+"/pulls", map[string]any{
		"title": title, "body": content.Body, "head": headRef, "base": baseBranch, "allow_maintainer_edit": true,
	})
}

func draftTitle(title string) string {
	for _, prefix := range []string{"WIP:", "[WIP]"} {
		if strings.HasPrefix(strings.ToUpper(title), prefix) {
			return title
		}
	}
	return "WIP: " + title
}

func (c *Client) Update(ctx context.Context, base forge.RepoInfo, number int, content *ai.PullRequestContent) (*forge.PullRequestInfo, error) {
	endpoint := fmt.Sprintf("%s/pulls/%d", repoPath(base), number)
	var existing forge.PullRequestInfo
	if err := c.request(ctx, http.MethodGet, endpoint, nil, &existing); err != nil {
		return nil, err
	}
	title := content.Title
	if existing.IsDraft {
		title = draftTitle(title)
	}
	return c.writePR(ctx, http.MethodPatch, endpoint, map[string]any{"title": title, "body": content.Body})
}

func (c *Client) writePR(ctx context.Context, method, endpoint string, payload map[string]any) (*forge.PullRequestInfo, error) {
	var pr forge.PullRequestInfo
	if err := c.request(ctx, method, endpoint, payload, &pr); err != nil {
		return nil, err
	}
	if pr.Number <= 0 || pr.URL == "" {
		return nil, fmt.Errorf("gitea PR publication response is missing its number or URL")
	}
	return &pr, nil
}

func (c *Client) request(ctx context.Context, method, endpoint string, payload, result any) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := []string{"api", "--login", c.Login, "--method", method, "--include", "--output", "-"}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
		args = append(args, "--data", "@-")
	}
	args = append(args, endpoint)
	response, err := c.Runner.Run(ctx, "tea", args, body)
	if err != nil {
		return fmt.Errorf("tea API request failed: %w", err)
	}
	// tea api can exit successfully for HTTP errors; --include reports status on stderr.
	status := 0
	for _, line := range strings.Split(response.Stderr, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.HasPrefix(fields[0], "HTTP/") {
			status, _ = strconv.Atoi(fields[1])
		}
	}
	if status == 0 {
		return fmt.Errorf("tea api did not report an HTTP status; use tea 0.16.0 or later")
	}
	if status < 200 || status >= 300 {
		var data struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal([]byte(response.Stdout), &data)
		return fmt.Errorf("tea API %s %s returned HTTP %d: %s", method, strings.SplitN(endpoint, "?", 2)[0], status, data.Message)
	}
	if err := json.Unmarshal([]byte(response.Stdout), result); err != nil {
		return fmt.Errorf("failed to parse tea API response: %w", err)
	}
	return nil
}
