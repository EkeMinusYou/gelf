package gitea

import (
	"context"

	"github.com/EkeMinusYou/gelf/internal/forge"
)

func (c *Client) FindPullRequestTemplate(ctx context.Context, repoRoot, owner string) (*forge.PullRequestTemplate, error) {
	// Gitea has no organization-wide default PR templates.
	template, err := forge.FindLocalPullRequestTemplate(repoRoot, []string{
		".gitea/PULL_REQUEST_TEMPLATE.md", ".gitea/pull_request_template.md",
	}, []string{
		".gitea/PULL_REQUEST_TEMPLATE", ".gitea/pull_request_template",
	})
	if err != nil || template != nil {
		return template, err
	}
	return forge.FindLocalPullRequestTemplate(repoRoot, []string{
		".github/PULL_REQUEST_TEMPLATE.md", ".github/pull_request_template.md",
		"PULL_REQUEST_TEMPLATE.md", "pull_request_template.md",
		"docs/PULL_REQUEST_TEMPLATE.md", "docs/pull_request_template.md",
	}, []string{
		".github/PULL_REQUEST_TEMPLATE", ".github/pull_request_template",
		"PULL_REQUEST_TEMPLATE", "pull_request_template",
		"docs/PULL_REQUEST_TEMPLATE", "docs/pull_request_template",
	})
}
