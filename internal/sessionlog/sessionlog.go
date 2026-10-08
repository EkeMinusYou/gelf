// Package sessionlog discovers local coding-agent conversations for PR context.
package sessionlog

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DefaultMaxBytes    = 20_000
	DefaultMaxSessions = 3
	maxAge             = 7 * 24 * time.Hour
	maxLineBytes       = 2 * 1024 * 1024
	maxReadBytes       = 32 * 1024 * 1024
)

type Source struct {
	Agent, Path string
}

type Result struct {
	Context   string
	Sources   []Source
	Truncated bool
}

type Options struct {
	MaxSessions int
	MaxBytes    int
}

// Detector accepts explicit storage roots and a clock to keep discovery testable.
type Detector struct {
	CodexHome, ClaudeHome string
	Now                   time.Time
}

func Discover(ctx context.Context, root, branch string, opts Options) (Result, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Result{}, err
	}
	codexHome, claudeHome := os.Getenv("CODEX_HOME"), os.Getenv("CLAUDE_CONFIG_DIR")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	if claudeHome == "" {
		claudeHome = filepath.Join(home, ".claude")
	}
	return (Detector{CodexHome: codexHome, ClaudeHome: claudeHome}).Find(ctx, root, branch, opts)
}

type candidate struct {
	Source
	modified time.Time
}

func (d Detector) Find(ctx context.Context, root, branch string, opts Options) (Result, error) {
	root = canonical(root)
	maxBytes, maxSessions := opts.MaxBytes, opts.MaxSessions
	if maxSessions < 0 {
		return Result{}, fmt.Errorf("session count must be greater than zero")
	}
	if maxSessions == 0 {
		maxSessions = DefaultMaxSessions
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	now := d.Now
	if now.IsZero() {
		now = time.Now()
	}
	cutoff := now.Add(-maxAge)
	var files []candidate
	var warnings []error
	for _, storage := range []struct{ agent, path string }{
		{"Codex", filepath.Join(d.CodexHome, "sessions")},
		{"Claude", filepath.Join(d.ClaudeHome, "projects")},
	} {
		err := filepath.WalkDir(storage.path, func(path string, entry fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					warnings = append(warnings, fmt.Errorf("%s logs: %w", storage.agent, err))
				}
				return nil
			}
			// Claude subagent transcripts are nested below the main session.
			if entry.IsDir() && storage.agent == "Claude" {
				rel, _ := filepath.Rel(storage.path, path)
				if strings.Contains(rel, string(filepath.Separator)) {
					return filepath.SkipDir
				}
			}
			if !entry.Type().IsRegular() || filepath.Ext(path) != ".jsonl" {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				warnings = append(warnings, err)
				return nil
			}
			if !info.ModTime().Before(cutoff) {
				files = append(files, candidate{Source{storage.agent, path}, info.ModTime()})
			}
			return nil
		})
		if err != nil {
			return Result{}, err
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].modified.Equal(files[j].modified) {
			return files[i].Path < files[j].Path
		}
		return files[i].modified.After(files[j].modified)
	})
	result := Result{}
	for i, file := range files {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		// Bound discovery work even if an agent has a very large local history.
		if i >= 1000 || len(result.Sources) >= maxSessions || len(result.Context) >= maxBytes {
			break
		}
		text, truncated, err := read(ctx, file, root, branch, cutoff, maxBytes)
		if err != nil {
			warnings = append(warnings, fmt.Errorf("session log %q: %w", file.Path, err))
			continue
		}
		if text == "" {
			continue
		}
		header := fmt.Sprintf("\nSESSION (%s):\n", file.Agent)
		if maxBytes-len(result.Context) <= len(header) {
			result.Truncated = true
			break
		}
		text, clipped := tail(text, maxBytes-len(result.Context)-len(header))
		result.Context += header + text
		result.Sources = append(result.Sources, file.Source)
		result.Truncated = result.Truncated || truncated || clipped
	}
	return result, errors.Join(warnings...)
}

type record struct {
	Type      string  `json:"type"`
	Timestamp string  `json:"timestamp"`
	Cwd       string  `json:"cwd"`
	Branch    string  `json:"gitBranch"`
	Sidechain bool    `json:"isSidechain"`
	Meta      bool    `json:"isMeta"`
	Payload   payload `json:"payload"`
	Message   message `json:"message"`
}

type payload struct {
	Type, Cwd, Role, Channel string
	Content                  json.RawMessage
	Git                      struct{ Branch string }
	Source                   json.RawMessage
}

type message struct {
	Role    string
	Content json.RawMessage
}

func read(ctx context.Context, file candidate, root, branch string, cutoff time.Time, maxBytes int) (string, bool, error) {
	f, err := os.Open(file.Path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	limited := &io.LimitedReader{R: f, N: maxReadBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 64*1024), maxLineBytes)
	cwd, sessionBranch := "", ""
	scopes := make(map[string]bool)
	inScope := func(cwd string) bool {
		if scoped, ok := scopes[cwd]; ok {
			return scoped
		}
		scoped := inWorktree(root, cwd)
		scopes[cwd] = scoped
		return scoped
	}
	matched, truncated := false, false
	text := ""
	for lines := 0; scanner.Scan(); lines++ {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		var r record
		if json.Unmarshal(scanner.Bytes(), &r) != nil {
			continue // Active logs may end with an incomplete JSON record.
		}
		role, content := "", json.RawMessage(nil)
		if file.Agent == "Codex" {
			switch r.Type {
			case "session_meta":
				cwd, sessionBranch = r.Payload.Cwd, r.Payload.Git.Branch
				// Subagent conversations are not main user sessions.
				if strings.Contains(strings.ReplaceAll(string(r.Payload.Source), "_", ""), "subagent") {
					return "", false, nil
				}
				matched = inScope(cwd) && (sessionBranch == "" || sessionBranch == branch)
				if !matched {
					return "", false, nil
				}
			case "turn_context":
				if r.Payload.Cwd != "" {
					cwd = r.Payload.Cwd
				}
			case "response_item":
				if r.Payload.Type == "message" && r.Payload.Channel != "analysis" {
					role, content = r.Payload.Role, r.Payload.Content
				}
			}
		} else {
			if r.Cwd != "" {
				cwd, sessionBranch = r.Cwd, r.Branch
				matched = inScope(cwd) && (sessionBranch == "" || sessionBranch == branch)
			}
			if (r.Type == "user" || r.Type == "assistant") && !r.Sidechain && !r.Meta {
				role, content = r.Message.Role, r.Message.Content
			}
		}
		// Avoid reading unrelated multi-megabyte histories beyond their header.
		if !matched && text == "" && lines >= 63 {
			return "", false, nil
		}
		if !matched || !inScope(cwd) || (role != "user" && role != "assistant") {
			continue
		}
		if timestamp, err := time.Parse(time.RFC3339Nano, r.Timestamp); err == nil && timestamp.Before(cutoff) {
			continue
		}
		value := messageText(content)
		if value == "" || injectedContext(value) {
			continue
		}
		value = redact(value)
		var clipped bool
		text, clipped = tail(text+fmt.Sprintf("%s: %s\n", role, value), maxBytes)
		truncated = truncated || clipped
	}
	if err := scanner.Err(); err != nil {
		return "", false, err
	}
	if limited.N <= 0 {
		return "", false, fmt.Errorf("exceeds the %d-byte read limit", maxReadBytes)
	}
	return text, truncated, nil
}

func messageText(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.TrimSpace(value)
	}
	var blocks []struct{ Type, Text string }
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" || b.Type == "input_text" || b.Type == "output_text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func injectedContext(text string) bool {
	for _, prefix := range []string{"# AGENTS.md instructions", "<environment_context>", "<environment_details>", "<INSTRUCTIONS>", "<local-command", "<command-name>", "<system-reminder>"} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:api[_-]?key|access[_-]?token|secret|password)["']?\s*[:=]\s*["']?[^\s"',;]+`),
	regexp.MustCompile(`(?i)Bearer\s+[a-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?:sk-[a-zA-Z0-9_-]{16,}|gh[pousr]_[a-zA-Z0-9]{20,}|github_pat_[a-zA-Z0-9_]{20,})`),
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
}

func redact(text string) string {
	for _, pattern := range secretPatterns {
		text = pattern.ReplaceAllString(text, "[REDACTED]")
	}
	return text
}

// tail retains recent messages and never splits a UTF-8 code point.
func tail(text string, limit int) (string, bool) {
	if len(text) <= limit {
		return text, false
	}
	text = text[len(text)-limit:]
	for len(text) > 0 && !utf8.RuneStart(text[0]) {
		text = text[1:]
	}
	return text, true
}

func canonical(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

func inWorktree(root, cwd string) bool {
	if cwd == "" || !filepath.IsAbs(cwd) {
		return false
	}
	cwd = canonical(cwd)
	rel, err := filepath.Rel(root, cwd)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	// A nested repository belongs to its own worktree.
	for dir := cwd; dir != root; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return false
		}
	}
	return true
}
