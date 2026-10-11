package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/EkeMinusYou/gelf/internal/ai"
	"github.com/EkeMinusYou/gelf/internal/config"
	"github.com/EkeMinusYou/gelf/internal/git"
	"github.com/EkeMinusYou/gelf/internal/testutil"
)

func TestCommitLimitsOnlyAIInputAndResolvesModelAlias(t *testing.T) {
	dir := testutil.Repo(t)
	testutil.Write(t, dir, "a.txt", strings.Repeat("large content\n", 100))
	testutil.Write(t, dir, "later-日本語.txt", "\n+new\n")
	testutil.Git(t, dir, "add", ".")
	cfg := testConfig()
	cfg.CommitMaxDiffBytes = 120
	client := &commandAI{}
	deps := dependencies{Git: git.NewRepository(dir), LoadConfig: func() (*config.Config, error) { return cfg, nil }, NewAI: func(ctx context.Context, cfg *config.Config, model string) (ai.Client, error) {
		client.Model = model
		return client, nil
	}}
	out, stderr, err := executeCommand(t, deps, "", "commit", "--dry-run", "--model", "pro", "--language", "japanese")
	if err != nil || out != "fix: generated message" || !strings.Contains(stderr, "later-日本語.txt (+2, -0)") || len(client.Commit.Diff) > 120 || !strings.Contains(client.Commit.DiffStat, "a.txt") || client.Commit.Branch != "main" || client.Commit.RecentCommits != "initial" || client.Model != "pro-model" || client.Commit.Language != "japanese" {
		t.Fatalf("out=%q stderr=%q client=%+v err=%v", out, stderr, client, err)
	}
	if cfg.FlashModel != "flash-model" || cfg.CommitLanguage != "english" {
		t.Fatal("command overrides mutated shared configuration")
	}
	out, stderr, err = executeCommand(t, deps, "", "commit", "--dry-run", "--quiet")
	if err != nil || out != "fix: generated message" || strings.Contains(stderr, "later-日本語.txt") || client.Model != "flash-model" {
		t.Fatalf("fresh command state: %q %q %+v %v", out, stderr, client, err)
	}
	out, _, err = executeCommand(t, deps, "", "commit", "--yes")
	if err != nil || !strings.Contains(out, "Commit successful") || testutil.Git(t, dir, "log", "-1", "--format=%s") != "fix: generated message" || testutil.Git(t, dir, "status", "--porcelain") != "" {
		t.Fatalf("commit=%q %v", out, err)
	}
}

func TestCommitPropagatesGenerationFailure(t *testing.T) {
	dir := testutil.Repo(t)
	testutil.Write(t, dir, "file", "change")
	testutil.Git(t, dir, "add", ".")
	failure := errors.New("AI unavailable")
	client := &commandAI{Err: failure}
	deps := dependencies{Git: git.NewRepository(dir), LoadConfig: func() (*config.Config, error) { return testConfig(), nil }, NewAI: func(context.Context, *config.Config, string) (ai.Client, error) { return client, nil }}
	if _, _, err := executeCommand(t, deps, "", "commit", "--yes"); !errors.Is(err, failure) {
		t.Fatalf("error=%v", err)
	}
	if got := testutil.Git(t, dir, "log", "-1", "--format=%s"); got != "initial" {
		t.Fatalf("unexpected commit: %s", got)
	}
}
