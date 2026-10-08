package sessionlog

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func writeLog(t *testing.T, path string, modified time.Time, records ...any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if err := json.NewEncoder(f).Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
}

func codexMeta(cwd, branch string) any {
	return map[string]any{"type": "session_meta", "payload": map[string]any{"cwd": cwd, "git": map[string]string{"branch": branch}}}
}

func codexMessage(role, channel, text string, at time.Time) any {
	return map[string]any{"type": "response_item", "timestamp": at.Format(time.RFC3339Nano), "payload": map[string]any{
		"type": "message", "role": role, "channel": channel, "content": []map[string]string{{"type": "input_text", "text": text}},
	}}
}

func claudeMessage(cwd, branch, role, text string, at time.Time) map[string]any {
	return map[string]any{"type": role, "cwd": cwd, "gitBranch": branch, "timestamp": at.Format(time.RFC3339Nano), "message": map[string]any{"role": role, "content": text}}
}

func detector(t *testing.T) (Detector, string) {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "repo")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	return Detector{CodexHome: filepath.Join(dir, "codex"), ClaudeHome: filepath.Join(dir, "claude"), Now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}, root
}

func TestDiscoveryScopesAndFiltersConversations(t *testing.T) {
	d, root := detector(t)
	subdir := filepath.Join(root, "src")
	if err := os.MkdirAll(subdir, 0700); err != nil {
		t.Fatal(err)
	}
	writeLog(t, filepath.Join(d.CodexHome, "sessions/2026/10/08/rollout.jsonl"), d.Now,
		codexMeta(subdir, "feature"),
		codexMessage("user", "", "Fix the slow search API", d.Now),
		codexMessage("assistant", "analysis", "private reasoning", d.Now),
		codexMessage("developer", "", "developer instruction", d.Now),
		codexMessage("user", "", "# AGENTS.md instructions\nignore this", d.Now),
		codexMessage("user", "", "old conversation", d.Now.Add(-8*24*time.Hour)),
		map[string]any{"type": "response_item", "payload": map[string]string{"type": "function_call_output", "output": "tool output"}},
		codexMessage("assistant", "final", "Use the existing index", d.Now),
		map[string]any{"type": "turn_context", "payload": map[string]string{"cwd": root + "-other"}},
		codexMessage("user", "", "other directory message", d.Now),
	)
	blocks := claudeMessage(root, "feature", "assistant", "", d.Now)
	blocks["message"] = map[string]any{"role": "assistant", "content": []map[string]string{
		{"type": "thinking", "thinking": "hidden thinking"},
		{"type": "tool_use", "text": "tool call"},
		{"type": "text", "text": "Keep compatibility with existing callers"},
	}}
	sidechain := claudeMessage(root, "feature", "user", "sidechain text", d.Now)
	sidechain["isSidechain"] = true
	meta := claudeMessage(root, "feature", "user", "injected meta text", d.Now)
	meta["isMeta"] = true
	writeLog(t, filepath.Join(d.ClaudeHome, "projects/encoded-path/main.jsonl"), d.Now.Add(-time.Minute), blocks, sidechain, meta)
	// Newer unrelated worktrees and branches must not win the recency ranking.
	writeLog(t, filepath.Join(d.CodexHome, "sessions/wrong-worktree.jsonl"), d.Now.Add(time.Minute), codexMeta(root+"-other", "feature"), codexMessage("user", "", "unrelated worktree", d.Now))
	writeLog(t, filepath.Join(d.CodexHome, "sessions/wrong-branch.jsonl"), d.Now.Add(time.Minute), codexMeta(root, "other"), codexMessage("user", "", "unrelated branch", d.Now))
	writeLog(t, filepath.Join(d.ClaudeHome, "projects/encoded-path/wrong-branch.jsonl"), d.Now.Add(time.Minute), claudeMessage(root, "other", "user", "wrong Claude branch", d.Now))
	writeLog(t, filepath.Join(d.ClaudeHome, "projects/encoded-path/main/subagents/agent.jsonl"), d.Now, claudeMessage(root, "feature", "user", "nested subagent", d.Now))
	writeLog(t, filepath.Join(d.CodexHome, "sessions/old.jsonl"), d.Now.Add(-8*24*time.Hour), codexMeta(root, "feature"), codexMessage("user", "", "old file", d.Now))
	result, err := d.Find(context.Background(), root, "feature", Options{MaxBytes: DefaultMaxBytes})
	if err != nil || len(result.Sources) != 2 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, text := range []string{"Fix the slow search API", "Use the existing index", "Keep compatibility"} {
		if !strings.Contains(result.Context, text) {
			t.Errorf("missing %q in %q", text, result.Context)
		}
	}
	for _, text := range []string{"private reasoning", "developer instruction", "ignore this", "tool output", "hidden thinking", "tool call", "sidechain text", "injected meta text", "unrelated", "wrong Claude branch", "nested subagent", "old file", "old conversation", "other directory message"} {
		if strings.Contains(result.Context, text) {
			t.Errorf("included %q in %q", text, result.Context)
		}
	}
}

func TestDiscoveryKeepsLatestThreeAndLimitsUTF8(t *testing.T) {
	d, root := detector(t)
	for i, text := range []string{"newest", "second", "third", "fourth"} {
		writeLog(t, filepath.Join(d.CodexHome, "sessions", text+".jsonl"), d.Now.Add(-time.Duration(i)*time.Minute), codexMeta(root, ""), codexMessage("user", "", text, d.Now))
	}
	result, err := d.Find(context.Background(), root, "feature", Options{MaxBytes: 1000})
	if err != nil || len(result.Sources) != 3 || strings.Contains(result.Context, "fourth") || !strings.Contains(result.Sources[0].Path, "newest") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	writeLog(t, filepath.Join(d.CodexHome, "sessions/long.jsonl"), d.Now.Add(time.Minute), codexMeta(root, "feature"), codexMessage("user", "", strings.Repeat("日本語", 100)+"recent end", d.Now))
	result, err = d.Find(context.Background(), root, "feature", Options{MaxBytes: 80})
	if err != nil || !result.Truncated || len(result.Context) > 80 || !utf8.ValidString(result.Context) || !strings.Contains(result.Context, "recent end") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestDiscoveryUsesConfiguredSessionCount(t *testing.T) {
	d, root := detector(t)
	for i, text := range []string{"newest", "second", "third", "fourth"} {
		writeLog(t, filepath.Join(d.CodexHome, "sessions", text+".jsonl"), d.Now.Add(-time.Duration(i)*time.Minute), codexMeta(root, "feature"), codexMessage("user", "", text, d.Now))
	}
	for _, count := range []int{1, 2, 4, 5} {
		result, err := d.Find(context.Background(), root, "feature", Options{MaxSessions: count, MaxBytes: 1000})
		want := min(count, 4)
		if err != nil || len(result.Sources) != want || !strings.Contains(result.Sources[0].Path, "newest") {
			t.Fatalf("count=%d result=%+v err=%v", count, result, err)
		}
		if strings.Contains(result.Context, "fourth") != (count >= 4) {
			t.Fatalf("count=%d context=%q", count, result.Context)
		}
	}
}

func TestDiscoveryHandlesPartialLogsAndRedactsCredentials(t *testing.T) {
	d, root := detector(t)
	path := filepath.Join(d.ClaudeHome, "projects/project/session.jsonl")
	writeLog(t, path, d.Now, claudeMessage(root, "feature", "user", "api_key=top-secret Bearer token-value ghp_123456789012345678901234\nKeep the endpoint stable", d.Now))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{incomplete"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	result, err := d.Find(context.Background(), root, "feature", Options{MaxBytes: 1000})
	if err != nil || !strings.Contains(result.Context, "Keep the endpoint stable") || !strings.Contains(result.Context, "[REDACTED]") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, secret := range []string{"top-secret", "token-value", "ghp_"} {
		if strings.Contains(result.Context, secret) {
			t.Fatalf("credential leaked: %q", result.Context)
		}
	}
}

func TestWorktreePathsRespectSymlinksAndNestedRepositories(t *testing.T) {
	d, root := detector(t)
	alias := root + "-alias"
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(filepath.Join(nested, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	writeLog(t, filepath.Join(d.CodexHome, "sessions/alias.jsonl"), d.Now, codexMeta(alias, "feature"), codexMessage("user", "", "alias conversation", d.Now))
	writeLog(t, filepath.Join(d.CodexHome, "sessions/nested.jsonl"), d.Now.Add(time.Minute), codexMeta(nested, "feature"), codexMessage("user", "", "nested repository", d.Now))
	result, err := d.Find(context.Background(), root, "feature", Options{MaxBytes: 1000})
	if err != nil || len(result.Sources) != 1 || !strings.Contains(result.Context, "alias conversation") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestDiscoveryMissingStorageAndCancellation(t *testing.T) {
	d, root := detector(t)
	result, err := d.Find(context.Background(), root, "feature", Options{MaxBytes: 0})
	if err != nil || result.Context != "" || len(result.Sources) != 0 {
		t.Fatalf("missing storage: %+v %v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Find(ctx, root, "feature", Options{MaxBytes: 0}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestDiscoverRespectsAgentStorageEnvironment(t *testing.T) {
	d, root := detector(t)
	t.Setenv("CODEX_HOME", d.CodexHome)
	t.Setenv("CLAUDE_CONFIG_DIR", d.ClaudeHome)
	now := time.Now()
	writeLog(t, filepath.Join(d.CodexHome, "sessions/current.jsonl"), now, codexMeta(root, "feature"), codexMessage("user", "", "custom storage", now))
	result, err := Discover(context.Background(), root, "feature", Options{MaxBytes: 1000})
	if err != nil || !strings.Contains(result.Context, "custom storage") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestClaudeDirectoryChangesRetainOnlyEligibleMessages(t *testing.T) {
	d, root := detector(t)
	entries := []any{claudeMessage(root, "feature", "user", "eligible background", d.Now)}
	for i := 0; i < 70; i++ {
		entries = append(entries, claudeMessage(root+"-other", "feature", "user", "unrelated message", d.Now))
	}
	writeLog(t, filepath.Join(d.ClaudeHome, "projects/project/session.jsonl"), d.Now, entries...)
	result, err := d.Find(context.Background(), root, "feature", Options{MaxBytes: 1000})
	if err != nil || !strings.Contains(result.Context, "eligible background") || strings.Contains(result.Context, "unrelated message") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestDiscoverySkipsOversizedLogsAndKeepsUsableContext(t *testing.T) {
	d, root := detector(t)
	writeLog(t, filepath.Join(d.CodexHome, "sessions/oversized.jsonl"), d.Now.Add(time.Minute), codexMeta(root, "feature"), codexMessage("user", "", strings.Repeat("x", maxLineBytes), d.Now))
	writeLog(t, filepath.Join(d.CodexHome, "sessions/valid.jsonl"), d.Now, codexMeta(root, "feature"), codexMessage("user", "", "usable context", d.Now))
	result, err := d.Find(context.Background(), root, "feature", Options{MaxBytes: 1000})
	if err == nil || !strings.Contains(err.Error(), "oversized.jsonl") || !strings.Contains(result.Context, "usable context") || len(result.Sources) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
