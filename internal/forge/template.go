package forge

import (
	"errors"
	"fmt"
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

func FindLocalPullRequestTemplate(repoRoot string, templateFileCandidates, templateDirCandidates []string) (*PullRequestTemplate, error) {
	for _, relPath := range templateFileCandidates {
		path := filepath.Join(repoRoot, relPath)
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("failed to inspect template %s: %w", relPath, err)
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
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("failed to inspect template directory %s: %w", relDir, err)
		}
		if !info.IsDir() {
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
