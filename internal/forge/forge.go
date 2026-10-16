package forge

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/EkeMinusYou/gelf/internal/ai"
)

// Client provides the hosting operations required by the PR workflow.
type Client interface {
	Repo(context.Context, string) (*RepoInfo, *RepoInfo, error)
	ParseRemoteURL(string) (*RepoInfo, error)
	FindPullRequest(context.Context, RepoInfo, RepoInfo, string) (*PullRequestInfo, error)
	Create(context.Context, RepoInfo, RepoInfo, string, string, *ai.PullRequestContent, bool) (*PullRequestInfo, error)
	Update(context.Context, RepoInfo, int, *ai.PullRequestContent) (*PullRequestInfo, error)
	FindPullRequestTemplate(context.Context, string, string) (*PullRequestTemplate, error)
}

type RepoInfo struct {
	Host          string
	Owner         string
	Name          string
	DefaultBranch string
	URL           string
	CloneURL      string
}

func (r RepoInfo) FullName() string      { return r.Owner + "/" + r.Name }
func (r RepoInfo) QualifiedName() string { return r.Host + "/" + r.FullName() }
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

func RepoInfoFromRemoteURL(remoteURL string) (*RepoInfo, error) {
	remoteURL = strings.TrimSpace(remoteURL)
	var host, path string
	if strings.Contains(remoteURL, "://") {
		parsed, err := url.Parse(remoteURL)
		if err != nil {
			return nil, err
		}
		host, path = parsed.Hostname(), parsed.Path
		if parsed.Scheme == "http" || parsed.Scheme == "https" {
			host = parsed.Host
		}
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
		return nil, fmt.Errorf("unsupported Git remote URL %q", remoteURL)
	}
	return &RepoInfo{Host: strings.ToLower(host), Owner: parts[0], Name: parts[1]}, nil
}
