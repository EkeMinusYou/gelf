package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type PullRequestTemplate struct {
	Source  string
	Path    string
	Content string
}

var templateFileCandidates = []string{
	".github/PULL_REQUEST_TEMPLATE.md",
	".github/pull_request_template.md",
	"PULL_REQUEST_TEMPLATE.md",
	"pull_request_template.md",
	"docs/PULL_REQUEST_TEMPLATE.md",
	"docs/pull_request_template.md",
}

var templateDirCandidates = []string{
	".github/PULL_REQUEST_TEMPLATE",
	".github/pull_request_template",
	"PULL_REQUEST_TEMPLATE",
	"pull_request_template",
	"docs/PULL_REQUEST_TEMPLATE",
	"docs/pull_request_template",
}

func FindPullRequestTemplate(ctx context.Context, repoRoot, token, owner string) (*PullRequestTemplate, error) {
	localTemplate, err := findLocalPullRequestTemplate(repoRoot)
	if err != nil {
		return nil, err
	}
	if localTemplate != nil {
		return localTemplate, nil
	}

	if owner == "" || token == "" {
		return nil, nil
	}

	return findOrgPullRequestTemplate(ctx, token, owner)
}

func findLocalPullRequestTemplate(repoRoot string) (*PullRequestTemplate, error) {
	for _, relPath := range templateFileCandidates {
		path := filepath.Join(repoRoot, relPath)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.IsDir() {
			continue
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read template file %s: %w", relPath, err)
		}

		return &PullRequestTemplate{
			Source:  "repo",
			Path:    relPath,
			Content: string(content),
		}, nil
	}

	for _, relDir := range templateDirCandidates {
		dirPath := filepath.Join(repoRoot, relDir)
		info, err := os.Stat(dirPath)
		if err != nil || !info.IsDir() {
			continue
		}

		selected, content, err := selectTemplateFromDir(dirPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read template directory %s: %w", relDir, err)
		}
		if selected == "" {
			continue
		}

		return &PullRequestTemplate{
			Source:  "repo",
			Path:    filepath.ToSlash(filepath.Join(relDir, selected)),
			Content: content,
		}, nil
	}

	return nil, nil
}

func selectTemplateFromDir(dirPath string) (string, string, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return "", "", err
	}

	var candidates []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if isTemplateFile(name) {
			candidates = append(candidates, name)
		}
	}

	if len(candidates) == 0 {
		return "", "", nil
	}

	sort.Strings(candidates)
	selected := candidates[0]
	content, err := os.ReadFile(filepath.Join(dirPath, selected))
	if err != nil {
		return "", "", err
	}

	return selected, string(content), nil
}

func isTemplateFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".markdown") || strings.HasSuffix(lower, ".txt")
}

func findOrgPullRequestTemplate(ctx context.Context, token, owner string) (*PullRequestTemplate, error) {
	orgRepo := ".github"

	for _, relPath := range templateFileCandidates {
		content, found, err := fetchGitHubFile(ctx, token, owner, orgRepo, relPath)
		if err != nil {
			return nil, err
		}
		if found {
			return &PullRequestTemplate{
				Source:  "org",
				Path:    relPath,
				Content: content,
			}, nil
		}
	}

	for _, relDir := range templateDirCandidates {
		selected, err := fetchGitHubDirTemplate(ctx, token, owner, orgRepo, relDir)
		if err != nil {
			return nil, err
		}
		if selected == nil {
			continue
		}

		return &PullRequestTemplate{
			Source:  "org",
			Path:    selected.Path,
			Content: selected.Content,
		}, nil
	}

	return nil, nil
}

type dirTemplate struct {
	Path    string
	Content string
}

func fetchGitHubDirTemplate(ctx context.Context, token, owner, repo, dirPath string) (*dirTemplate, error) {
	entries, found, err := fetchGitHubDir(ctx, token, owner, repo, dirPath)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}

	var candidates []string
	for _, entry := range entries {
		if entry.Type != "file" {
			continue
		}
		if isTemplateFile(entry.Name) {
			candidates = append(candidates, entry.Path)
		}
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	sort.Strings(candidates)
	selectedPath := candidates[0]
	content, found, err := fetchGitHubFile(ctx, token, owner, repo, selectedPath)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}

	return &dirTemplate{
		Path:    selectedPath,
		Content: content,
	}, nil
}

type githubDirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"`
}

func fetchGitHubDir(ctx context.Context, token, owner, repo, path string) ([]githubDirEntry, bool, error) {
	body, status, err := fetchGitHubContent(ctx, token, owner, repo, path)
	if err != nil {
		return nil, false, err
	}
	if status == http.StatusNotFound {
		return nil, false, nil
	}
	if status != http.StatusOK {
		return nil, false, fmt.Errorf("unexpected status %d when fetching directory %s", status, path)
	}

	var entries []githubDirEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, false, fmt.Errorf("failed to parse directory listing for %s: %w", path, err)
	}

	return entries, true, nil
}

func fetchGitHubFile(ctx context.Context, token, owner, repo, path string) (string, bool, error) {
	body, status, err := fetchGitHubContent(ctx, token, owner, repo, path)
	if err != nil {
		return "", false, err
	}
	if status == http.StatusNotFound {
		return "", false, nil
	}
	if status != http.StatusOK {
		return "", false, fmt.Errorf("unexpected status %d when fetching file %s", status, path)
	}

	var file struct {
		Type     string `json:"type"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}

	if err := json.Unmarshal(body, &file); err != nil {
		return "", false, fmt.Errorf("failed to parse file response for %s: %w", path, err)
	}

	if file.Type != "file" {
		return "", false, nil
	}

	content := file.Content
	if file.Encoding == "base64" {
		decoded, err := decodeBase64(content)
		if err != nil {
			return "", false, fmt.Errorf("failed to decode base64 content for %s: %w", path, err)
		}
		content = decoded
	}

	return content, true, nil
}

func fetchGitHubContent(ctx context.Context, token, owner, repo, path string) ([]byte, int, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}

	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gelf")
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}

	return body, resp.StatusCode, nil
}

func decodeBase64(encoded string) (string, error) {
	cleaned := strings.ReplaceAll(encoded, "\n", "")
	decoded, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}
