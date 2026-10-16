package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/EkeMinusYou/gelf/internal/config"
	"github.com/EkeMinusYou/gelf/internal/forge"
	"github.com/EkeMinusYou/gelf/internal/gitea"
)

type prHost struct {
	Client   forge.Client
	Head     *forge.RepoInfo
	RepoName string
}

func resolvePRHost(ctx context.Context, deps dependencies, cfg *config.Config, pushURL string) (*prHost, error) {
	switch cfg.PRProvider {
	case "github":
		return githubPRHost(deps, pushURL)
	case "gitea":
		return giteaPRHost(ctx, deps, cfg.GiteaLogin, pushURL)
	case "", "auto":
	default:
		return nil, fmt.Errorf("invalid PR provider %q: expected auto, github, or gitea", cfg.PRProvider)
	}
	hostname, err := pushHostname(pushURL)
	if err != nil {
		return nil, err
	}
	if hostname == "github.com" || hostname == "ssh.github.com" {
		return githubPRHost(deps, pushURL)
	}

	// Both clients inspect the push destination; a fetch URL or gh's default repo
	// can refer to a different host in fork and pushurl workflows.
	teaHost, teaErr := giteaPRHost(ctx, deps, cfg.GiteaLogin, pushURL)
	isGitea := teaErr == nil || errors.Is(teaErr, gitea.ErrAmbiguousLogin) || errors.Is(teaErr, gitea.ErrLoginSelection)
	isGitHub := false
	var ghErr error
	if deps.GitHub != nil {
		isGitHub, ghErr = deps.GitHub.HasHost(ctx, hostname)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if isGitea && isGitHub {
		return nil, fmt.Errorf("both gh and tea have matching logins for the push destination; select --provider github or --provider gitea")
	}
	if isGitea {
		return teaHost, teaErr
	}
	if isGitHub {
		return githubPRHost(deps, pushURL)
	}
	return nil, fmt.Errorf("cannot detect the PR hosting provider; authenticate with gh or tea, or select --provider github or --provider gitea: %w", errors.Join(teaErr, ghErr))
}

func githubPRHost(deps dependencies, pushURL string) (*prHost, error) {
	if deps.GitHub == nil {
		return nil, fmt.Errorf("github client is unavailable")
	}
	gh := *deps.GitHub
	head, err := gh.ParseRemoteURL(pushURL)
	if err != nil {
		return nil, err
	}
	gh.Host = head.Host
	host := &prHost{Client: &gh, Head: head}
	if head.Host != "github.com" {
		host.RepoName = head.QualifiedName()
	}
	return host, nil
}

func giteaPRHost(ctx context.Context, deps dependencies, login, pushURL string) (*prHost, error) {
	if deps.Gitea == nil {
		return nil, fmt.Errorf("gitea client is unavailable")
	}
	tea := *deps.Gitea
	head, err := tea.ResolveRemote(ctx, pushURL, login)
	if err != nil {
		return nil, err
	}
	return &prHost{Client: &tea, Head: head, RepoName: head.FullName()}, nil
}

func pushHostname(remoteURL string) (string, error) {
	if strings.Contains(remoteURL, "://") {
		parsed, err := url.Parse(remoteURL)
		if err != nil || parsed.Hostname() == "" {
			return "", fmt.Errorf("push remote has no valid hostname")
		}
		host := parsed.Host
		if parsed.Scheme == "ssh" {
			host = parsed.Hostname()
		}
		return strings.ToLower(host), nil
	}
	info, err := forge.RepoInfoFromRemoteURL(remoteURL)
	if err != nil {
		return "", err
	}
	return info.Host, nil
}
