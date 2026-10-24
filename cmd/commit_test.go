package cmd

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/EkeMinusYou/gelf/internal/ai"
	"github.com/EkeMinusYou/gelf/internal/config"
	"github.com/EkeMinusYou/gelf/internal/git"
	"github.com/EkeMinusYou/gelf/internal/process"
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
	if cfg.FlashModel.Name != "flash-model" || cfg.CommitLanguage != "english" {
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

type countedCommitAI struct {
	commandAI
	calls int
}

func (c *countedCommitAI) GenerateCommitMessage(ctx context.Context, input ai.CommitInput) (string, error) {
	c.calls++
	return c.commandAI.GenerateCommitMessage(ctx, input)
}

func TestCommitCacheReuseBypassAndStagedChange(t *testing.T) {
	dir := testutil.Repo(t)
	testutil.Write(t, dir, "file", "change\n")
	testutil.Git(t, dir, "add", ".")
	cfg := testConfig()
	cfg.CommitCache = true
	client := &countedCommitAI{}
	deps := dependencies{Git: git.NewRepository(dir), CommitCache: &ai.CommitCache{Dir: t.TempDir()}, LoadConfig: func() (*config.Config, error) { return cfg, nil }, NewAI: func(context.Context, *config.Config, string) (ai.Client, error) { return client, nil }}
	run := func(extra ...string) {
		t.Helper()
		out, _, err := executeCommand(t, deps, "", append([]string{"commit", "--dry-run", "--quiet"}, extra...)...)
		if err != nil || out != "fix: generated message" {
			t.Fatalf("output=%q err=%v", out, err)
		}
	}
	run()
	run()
	if client.calls != 1 {
		t.Fatalf("unchanged input regenerated: %d", client.calls)
	}
	run("--no-cache")
	if client.calls != 2 {
		t.Fatal("--no-cache did not force generation")
	}
	testutil.Write(t, dir, "file", "different staged change\n")
	testutil.Git(t, dir, "add", ".")
	run()
	if client.calls != 3 || !strings.Contains(client.Commit.Diff, "+different staged change") {
		t.Fatal("changed index reused a stale message")
	}
	cfg.CommitCache = false
	run()
	if client.calls != 4 {
		t.Fatal("disabled cache was used")
	}
}

func TestCommitPreparationRunsIndependentReadsConcurrently(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 4)
	release := make(chan struct{})
	runner := testutil.RunnerFunc(func(ctx context.Context, name string, args []string, input io.Reader) (process.Result, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "-U5") {
			return process.Result{Stdout: "diff --git a/file b/file\n+change\n"}, nil
		}
		var out string
		switch {
		case strings.Contains(joined, "--numstat"):
			out = "1\t0\tfile\x00"
		case strings.Contains(joined, "--stat"):
			out = "file | 1 +"
		case strings.HasPrefix(joined, "rev-parse"):
			out = "HEAD"
		case strings.HasPrefix(joined, "log"):
			return process.Result{Stdout: "fix: style"}, nil
		case strings.HasPrefix(joined, "symbolic-ref"):
			out = "topic"
		default:
			return process.Result{}, errors.New("unexpected command: " + joined)
		}
		ready <- joined
		select {
		case <-release:
			return process.Result{Stdout: out}, nil
		case <-ctx.Done():
			return process.Result{}, ctx.Err()
		}
	})
	client := &commandAI{}
	deps := dependencies{Git: &git.Repository{Runner: runner}, LoadConfig: func() (*config.Config, error) { return testConfig(), nil }, NewAI: func(context.Context, *config.Config, string) (ai.Client, error) { return client, nil }}
	done := make(chan error, 1)
	go func() {
		root := newRootCommand(deps)
		root.SetArgs([]string{"commit", "--dry-run"})
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		done <- root.ExecuteContext(ctx)
	}()
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for i := 0; i < 4; i++ {
		select {
		case <-ready:
		case <-timeout.C:
			cancel()
			t.Fatal("independent Git reads did not start concurrently")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if client.Commit.Branch != "topic" || client.Commit.RecentCommits != "fix: style" || client.Commit.DiffStat != "file | 1 +" {
		t.Fatalf("parallel preparation changed context: %+v", client.Commit)
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

func TestCommitEndpointDoesNotChangeSharedConfiguration(t *testing.T) {
	dir := testutil.Repo(t)
	testutil.Write(t, dir, "file", "change")
	testutil.Git(t, dir, "add", ".")
	cfg := testConfig()
	cfg.FlashModel.Location = "us"
	cfg.ProModel = config.Model{Name: "gemini-3.1-pro-preview", Location: "global"}
	var location string
	deps := dependencies{Git: git.NewRepository(dir), LoadConfig: func() (*config.Config, error) { return cfg, nil }, NewAI: func(ctx context.Context, resolved *config.Config, model string) (ai.Client, error) {
		location = resolved.Location
		return &commandAI{}, nil
	}}
	for _, tc := range []struct{ model, location string }{{"flash", "us"}, {"pro", "global"}} {
		_, _, err := executeCommand(t, deps, "", "commit", "--dry-run", "--quiet", "--model", tc.model)
		if err != nil || location != tc.location || cfg.Location != "global" || cfg.FlashModel.Location != "us" {
			t.Fatalf("model=%s location=%s shared_location=%s err=%v", tc.model, location, cfg.Location, err)
		}
	}
}
