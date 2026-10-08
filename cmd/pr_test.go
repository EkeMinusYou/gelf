package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EkeMinusYou/gelf/internal/ai"
	"github.com/EkeMinusYou/gelf/internal/config"
	"github.com/EkeMinusYou/gelf/internal/git"
	"github.com/EkeMinusYou/gelf/internal/github"
	"github.com/EkeMinusYou/gelf/internal/process"
	"github.com/EkeMinusYou/gelf/internal/sessionlog"
	"github.com/EkeMinusYou/gelf/internal/testutil"
	"github.com/EkeMinusYou/gelf/internal/ui"
)

type commandAI struct {
	Input                 ai.PullRequestInput
	Diff, Language, Model string
	Err                   error
}

func (c *commandAI) GenerateCommitMessage(ctx context.Context, diff, language string) (string, error) {
	c.Diff, c.Language = diff, language
	return "fix: generated message", c.Err
}
func (c *commandAI) GeneratePullRequestContent(ctx context.Context, input ai.PullRequestInput) (*ai.PullRequestContent, error) {
	c.Input = input
	return &ai.PullRequestContent{Title: "generated title", Body: "generated body"}, c.Err
}
func (c *commandAI) RevisePullRequestContent(ctx context.Context, input ai.PullRequestInput, previous *ai.PullRequestContent, instructions string) (*ai.PullRequestContent, error) {
	return c.GeneratePullRequestContent(ctx, input)
}

func testConfig() *config.Config {
	return &config.Config{ProjectID: "test", Location: "global", FlashModel: "flash-model", ProModel: "pro-model", CommitModel: "flash-model", PRModel: "pro-model", CommitLanguage: "english", PRLanguage: "english", PRTitleLanguage: "english", PRBodyLanguage: "english", CommitMaxDiffBytes: 100000, PRMaxDiffBytes: 100000, PRSessionLogCount: 3, PRMaxSessionLogBytes: 20000, Color: "never"}
}

func TestPRDryRunUsesSessionContextWhenEnabled(t *testing.T) {
	f := newPRFixture(t)
	storage := t.TempDir()
	path := filepath.Join(storage, "projects/project/session.jsonl")
	now := time.Now()
	entry, err := json.Marshal(map[string]any{"type": "user", "cwd": f.Dir, "gitBranch": "feature", "timestamp": now.Format(time.RFC3339Nano), "message": map[string]string{"role": "user", "content": "Preserve compatibility with older clients"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(entry, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	f.Deps.FindSessions = (sessionlog.Detector{CodexHome: filepath.Join(storage, "codex"), ClaudeHome: storage, Now: now}).Find
	_, stderr, err := executeCommand(t, f.Deps, "", "pr", "create", "--dry-run", "--session-logs")
	if err != nil || !strings.Contains(f.AI.Input.SessionContext, "Preserve compatibility") || !strings.Contains(stderr, "Using Claude session log:") || !strings.Contains(stderr, path) {
		t.Fatalf("input=%+v stderr=%q err=%v", f.AI.Input, stderr, err)
	}
}

func TestPRSessionDiscoveryCanBeDisabled(t *testing.T) {
	for _, flag := range []bool{false, true} {
		t.Run(fmt.Sprint(flag), func(t *testing.T) {
			f := newPRFixture(t)
			f.Config.PRSessionLogs = flag
			f.Deps.FindSessions = func(context.Context, string, string, sessionlog.Options) (sessionlog.Result, error) {
				t.Fatal("disabled session discovery was called")
				return sessionlog.Result{}, nil
			}
			args := []string{"pr", "create", "--dry-run"}
			if flag {
				args = append(args, "--no-session-logs")
			}
			if _, _, err := executeCommand(t, f.Deps, "", args...); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPRSessionFlagsOverrideConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name       string
		enabled    bool
		count      int
		flags      []string
		wantCalled bool
		wantCount  int
	}{
		{name: "default disabled", count: 3},
		{name: "enabled by flag", count: 3, flags: []string{"--session-logs"}, wantCalled: true, wantCount: 3},
		{name: "enabled by configuration", enabled: true, count: 5, wantCalled: true, wantCount: 5},
		{name: "count flag overrides configuration", enabled: true, count: 5, flags: []string{"--session-log-count", "2"}, wantCalled: true, wantCount: 2},
		{name: "count alone keeps discovery disabled", count: 3, flags: []string{"--session-log-count", "5"}},
		{name: "false flag overrides enabled configuration", enabled: true, count: 3, flags: []string{"--session-logs=false"}},
		{name: "disable flag takes precedence", count: 3, flags: []string{"--session-logs", "--no-session-logs"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPRFixture(t)
			f.Config.PRSessionLogs, f.Config.PRSessionLogCount = tc.enabled, tc.count
			called := false
			f.Deps.FindSessions = func(ctx context.Context, root, branch string, opts sessionlog.Options) (sessionlog.Result, error) {
				called = true
				if opts.MaxSessions != tc.wantCount || opts.MaxBytes != 20000 {
					t.Fatalf("unexpected discovery options: %+v", opts)
				}
				return sessionlog.Result{Context: "session background"}, nil
			}
			args := append([]string{"pr", "create", "--dry-run"}, tc.flags...)
			if _, _, err := executeCommand(t, f.Deps, "", args...); err != nil {
				t.Fatal(err)
			}
			if called != tc.wantCalled || (f.AI.Input.SessionContext != "") != tc.wantCalled {
				t.Fatalf("called=%t input=%+v", called, f.AI.Input)
			}
			if f.Config.PRSessionLogs != tc.enabled || f.Config.PRSessionLogCount != tc.count {
				t.Fatal("CLI flags mutated shared configuration")
			}
		})
	}
}

func TestPRRejectsInvalidSessionCountBeforeGitOperations(t *testing.T) {
	for _, value := range []string{"0", "-1"} {
		deps := dependencies{LoadConfig: func() (*config.Config, error) { return testConfig(), nil }}
		// Git and GitHub are intentionally absent: validation must happen first.
		_, _, err := executeCommand(t, deps, "", "pr", "create", "--session-log-count", value)
		if err == nil || !strings.Contains(err.Error(), "--session-log-count must be greater than zero") {
			t.Fatalf("value=%s err=%v", value, err)
		}
	}
}

func TestPRSessionDiscoveryWarningsAreNonfatalAndUseLocalBranch(t *testing.T) {
	f := newPRFixture(t)
	f.Config.PRSessionLogs = true
	// The remote branch name can differ from the branch recorded by the agent.
	testutil.Git(t, f.Dir, "config", "branch.feature.remote", "origin")
	testutil.Git(t, f.Dir, "config", "branch.feature.merge", "refs/heads/remote-feature")
	canonicalRoot, err := filepath.EvalSymlinks(f.Dir)
	if err != nil {
		t.Fatal(err)
	}
	f.Deps.FindSessions = func(ctx context.Context, root, branch string, opts sessionlog.Options) (sessionlog.Result, error) {
		if root != canonicalRoot || branch != "feature" || opts.MaxBytes != 20000 || opts.MaxSessions != 3 {
			t.Fatalf("discovery root=%q branch=%q options=%+v", root, branch, opts)
		}
		return sessionlog.Result{Context: "background intent", Truncated: true}, fmt.Errorf("unreadable log")
	}
	_, stderr, err := executeCommand(t, f.Deps, "", "pr", "create", "--dry-run")
	if err != nil || f.AI.Input.SessionContext != "background intent" || !strings.Contains(stderr, "unreadable log") || !strings.Contains(stderr, "context was truncated") {
		t.Fatalf("input=%+v stderr=%q err=%v", f.AI.Input, stderr, err)
	}
}

func executeCommand(t *testing.T, deps dependencies, input string, args ...string) (string, string, error) {
	t.Helper()
	root := newRootCommand(deps)
	var out, stderr bytes.Buffer
	root.SetIn(strings.NewReader(input))
	root.SetOut(&out)
	root.SetErr(&stderr)
	root.SetArgs(args)
	root.SilenceErrors = true
	err := root.ExecuteContext(context.Background())
	return out.String(), stderr.String(), err
}

type prFixture struct {
	Deps              dependencies
	AI                *commandAI
	Config            *config.Config
	Dir, Parent, Fork string
	PRs               string
	Commands          []string
	Published         []string
	PublishedBody     string
}

func newPRFixture(t *testing.T) *prFixture {
	t.Helper()
	seed := testutil.Repo(t)
	testutil.Write(t, seed, ".github/PULL_REQUEST_TEMPLATE.md", "## Purpose")
	testutil.Git(t, seed, "add", ".")
	testutil.Git(t, seed, "commit", "-qm", "template")
	initial := testutil.Git(t, seed, "rev-parse", "HEAD")
	f := &prFixture{AI: &commandAI{}, Config: testConfig(), PRs: "[]"}
	f.Fork, f.Parent = filepath.Join(t.TempDir(), "fork.git"), filepath.Join(t.TempDir(), "parent.git")
	testutil.Git(t, seed, "clone", "--bare", "-q", seed, f.Fork)
	testutil.Git(t, seed, "clone", "--bare", "-q", seed, f.Parent)
	parentWork := filepath.Join(t.TempDir(), "parent")
	testutil.Git(t, seed, "clone", "-q", f.Parent, parentWork)
	testutil.Write(t, parentWork, "parent-only.txt", "parent change")
	testutil.Git(t, parentWork, "add", ".")
	testutil.Git(t, parentWork, "commit", "-qm", "parent advancement")
	testutil.Git(t, parentWork, "push", "-q", "origin", "main")
	testutil.Git(t, parentWork, "push", "-q", "origin", initial+":refs/heads/release")
	f.Dir = filepath.Join(t.TempDir(), "local")
	testutil.Git(t, seed, "clone", "-q", f.Fork, f.Dir)
	testutil.Git(t, f.Dir, "remote", "add", "upstream", f.Parent)
	testutil.Git(t, f.Dir, "fetch", "-q", "upstream")
	testutil.Git(t, f.Dir, "checkout", "-qb", "feature", "upstream/main")
	// The branch pulls from upstream but should publish to the fork.
	testutil.Git(t, f.Dir, "config", "branch.feature.pushRemote", "origin")
	testutil.Write(t, f.Dir, "feature.txt", strings.Repeat("new content\n", 100))
	testutil.Git(t, f.Dir, "add", ".")
	testutil.Git(t, f.Dir, "commit", "-qm", "feature change")
	actualGit := process.CommandRunner{Dir: f.Dir}
	repo := &git.Repository{Runner: testutil.RunnerFunc(func(ctx context.Context, name string, args []string, stdin io.Reader) (process.Result, error) {
		f.Commands = append(f.Commands, name+" "+strings.Join(args, " "))
		if len(args) >= 3 && args[0] == "remote" && args[1] == "get-url" {
			owner := "fork"
			if args[len(args)-1] == "upstream" {
				owner = "parent"
			}
			return process.Result{Stdout: "https://github.com/" + owner + "/repo.git\n"}, nil
		}
		mapped := append([]string(nil), args...)
		for i, arg := range mapped {
			if arg == "https://github.com/fork/repo.git" {
				mapped[i] = f.Fork
			}
			if arg == "https://github.com/parent/repo.git" {
				mapped[i] = f.Parent
			}
		}
		return actualGit.Run(ctx, name, mapped, stdin)
	})}
	gh := github.NewClient(testutil.RunnerFunc(func(ctx context.Context, name string, args []string, stdin io.Reader) (process.Result, error) {
		if args[0] == "repo" {
			if args[2] == "--json" {
				return process.Result{Stdout: `{"owner":{"login":"fork"},"name":"repo","url":"https://github.com/fork/repo","defaultBranchRef":{"name":"main"},"parent":{"id":"parent-id","owner":{"login":"parent"},"name":"repo"}}`}, nil
			}
			return process.Result{Stdout: `{"owner":{"login":"parent"},"name":"repo","url":"https://github.com/parent/repo","defaultBranchRef":{"name":"main"}}`}, nil
		}
		if args[0] == "api" {
			if stdin == nil {
				return process.Result{Stdout: f.PRs}, nil
			}
			f.Published = append([]string(nil), args...)
			var payload map[string]any
			if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
				return process.Result{}, err
			}
			f.PublishedBody, _ = payload["body"].(string)
			if payload["head"] != nil && payload["head"] != "fork:feature" {
				t.Fatalf("wrong head payload: %v", payload)
			}
			if payload["base"] != nil && payload["base"] != "main" {
				t.Fatalf("wrong base payload: %v", payload)
			}
			return process.Result{Stdout: `{"number":42,"html_url":"https://github.com/parent/repo/pull/42"}`}, nil
		}
		return process.Result{}, fmt.Errorf("unexpected gh command: %v", args)
	}))
	f.Deps = dependencies{Git: repo, GitHub: gh, LoadConfig: func() (*config.Config, error) { return f.Config, nil }, NewAI: func(ctx context.Context, cfg *config.Config, model string) (ai.Client, error) {
		f.AI.Model = model
		return f.AI, nil
	}}
	return f
}

func TestPRDryRunUsesParentBaseAndFetchesWithoutPublishing(t *testing.T) {
	f := newPRFixture(t)
	f.Config.PRMaxDiffBytes = 160
	before := testutil.Git(t, f.Dir, "rev-parse", "HEAD")
	out, stderr, err := executeCommand(t, f.Deps, "", "pr", "create", "--dry-run", "--no-render", "--language", "japanese", "--title-language", "english", "--model", "flash")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "generated title") || !strings.Contains(stderr, "limiting AI input") || len(f.AI.Input.Diff) > 160 || !strings.HasSuffix(f.AI.Input.Diff, "[diff truncated]") {
		t.Fatalf("out=%q stderr=%q input=%+v", out, stderr, f.AI.Input)
	}
	if strings.Contains(f.AI.Input.CommitLog, "parent advancement") || strings.Contains(f.AI.Input.DiffStat, "parent-only") || f.AI.Model != "flash-model" || f.AI.Input.TitleLanguage != "english" || f.AI.Input.BodyLanguage != "japanese" {
		t.Fatalf("incorrect base/model/language: %+v", f.AI)
	}
	for _, cmd := range f.Commands {
		if strings.HasPrefix(cmd, "git push") {
			t.Fatal("dry-run pushed a branch")
		}
	}
	if len(f.Published) != 0 || testutil.Git(t, f.Dir, "rev-parse", "HEAD") != before || testutil.Git(t, f.Dir, "branch", "--show-current") != "feature" {
		t.Fatal("dry-run modified branch or published PR")
	}
	foundFetch := false
	for _, cmd := range f.Commands {
		if strings.Contains(cmd, "fetch") && strings.Contains(cmd, "upstream") {
			foundFetch = true
		}
	}
	if !foundFetch {
		t.Fatal("parent base was not fetched")
	}
}

func TestPRCreateAndUpdatePinCorrectRepositories(t *testing.T) {
	for _, update := range []bool{false, true} {
		t.Run(fmt.Sprint(update), func(t *testing.T) {
			f := newPRFixture(t)
			args := []string{"pr", "create", "--yes"}
			if update {
				f.PRs = `[[{"number":42,"title":"old","html_url":"https://github.com/parent/repo/pull/42","state":"open","head":{"ref":"feature","repo":{"full_name":"fork/repo"}},"base":{"ref":"release"}}]]`
				args = append(args, "--update")
			}
			out, _, err := executeCommand(t, f.Deps, "", args...)
			if err != nil {
				t.Fatal(err)
			}
			published := strings.Join(f.Published, " ")
			if (!strings.Contains(published, "--hostname github.com") || !strings.Contains(published, "repos/parent/repo/pulls")) || f.PublishedBody != "generated body" || !strings.Contains(out, "(#42)") {
				t.Fatalf("publication=%s body=%q out=%q", published, f.PublishedBody, out)
			}
			if update {
				if !strings.Contains(published, "--method PATCH") || f.AI.Input.BaseBranch != "release" || !strings.Contains(f.AI.Input.CommitLog, "parent advancement") {
					t.Fatalf("wrong update base: %+v %s", f.AI.Input, published)
				}
			} else if !strings.Contains(published, "--method POST") {
				t.Fatalf("wrong create head/base: %s", published)
			}
			if got := testutil.Git(t, f.Dir, "ls-remote", "--heads", f.Fork, "feature"); !strings.HasPrefix(got, testutil.Git(t, f.Dir, "rev-parse", "HEAD")) {
				t.Fatalf("fork branch was not pushed: %s", got)
			}
		})
	}
}

func TestUpdateCreatesPRWhenNoOpenPRExists(t *testing.T) {
	for _, state := range []string{"missing", "closed", "merged"} {
		t.Run(state, func(t *testing.T) {
			f := newPRFixture(t)
			if state != "missing" {
				merged := state == "merged"
				f.PRs = fmt.Sprintf(`[[{"number":42,"state":"closed","merged":%t,"head":{"ref":"feature","repo":{"full_name":"fork/repo"}},"base":{"ref":"release"}}]]`, merged)
			}
			out, _, err := executeCommand(t, f.Deps, "", "pr", "create", "--update", "--yes")
			if err != nil {
				t.Fatal(err)
			}
			published := strings.Join(f.Published, " ")
			if !strings.Contains(published, "--method POST") || !strings.HasSuffix(published, "repos/parent/repo/pulls") || f.PublishedBody != "generated body" || f.AI.Input.BaseBranch != "main" || !strings.Contains(out, "Pull request created") {
				t.Fatalf("publication=%s body=%q base=%q out=%q", published, f.PublishedBody, f.AI.Input.BaseBranch, out)
			}
		})
	}
}

func TestUpdateWithoutOpenPRUsesCreatePrompt(t *testing.T) {
	f := newPRFixture(t)
	out, _, err := executeCommand(t, f.Deps, "y\nn\n", "pr", "create", "--update", "--no-render")
	if err != nil || !strings.Contains(out, "Create this pull request?") || strings.Contains(out, "Update this pull request?") || len(f.Published) != 0 {
		t.Fatalf("out=%q publication=%v err=%v", out, f.Published, err)
	}
}

func TestExistingPRSkipsGenerationWithoutUpdate(t *testing.T) {
	f := newPRFixture(t)
	f.PRs = `[[{"number":42,"title":"old","html_url":"https://github.com/parent/repo/pull/42","state":"open","draft":true,"head":{"ref":"feature","repo":{"full_name":"fork/repo"}},"base":{"ref":"main"}}]]`
	_, stderr, err := executeCommand(t, f.Deps, "", "pr", "create")
	if err != nil || !strings.Contains(stderr, "already exists") || f.AI.Model != "" || len(f.Published) != 0 {
		t.Fatalf("existing PR incorrectly regenerated: %s %v", stderr, err)
	}
}

func TestBehindStopsAndYesNeverSkipsForceConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		status      git.PushStatus
		wantPush    bool
		wantError   bool
	}{
		{"behind", "y\n", git.PushStatus{Behind: 1}, false, true},
		{"force denied", "n\n", git.PushStatus{Ahead: 1, Behind: 1}, false, false},
		{"force EOF", "", git.PushStatus{Ahead: 1, Behind: 1}, false, false},
		{"force approved", "y\n", git.PushStatus{Ahead: 1, Behind: 1}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pushed := false
			tc.status.HeadSHA, tc.status.RemoteSHA = "head-sha", "remote-sha"
			tc.status.Target = git.PushTarget{RemoteName: "fork", Branch: "feature", LocalBranch: "feature", HasUpstream: true}
			repo := &git.Repository{Runner: testutil.RunnerFunc(func(ctx context.Context, name string, args []string, stdin io.Reader) (process.Result, error) {
				switch args[0] {
				case "rev-parse":
					return process.Result{Stdout: "head-sha"}, nil
				case "symbolic-ref":
					return process.Result{Stdout: "feature"}, nil
				case "push":
					pushed = true
					if !strings.Contains(strings.Join(args, " "), "--force-with-lease=refs/heads/feature:remote-sha") {
						t.Fatal("captured lease missing")
					}
					return process.Result{}, nil
				}
				t.Fatalf("unexpected git call: %v", args)
				return process.Result{}, nil
			})}
			session := ui.NewSession(context.Background(), strings.NewReader(tc.input), io.Discard, io.Discard, false, false)
			confirmed, err := ensureBranchPushed(context.Background(), repo, tc.status, true, session)
			if pushed != tc.wantPush || confirmed != tc.wantPush || (err != nil) != tc.wantError {
				t.Fatalf("pushed=%v confirmed=%v err=%v", pushed, confirmed, err)
			}
		})
	}
}

func TestPRFetchesParentWithoutAddingRemote(t *testing.T) {
	f := newPRFixture(t)
	testutil.Git(t, f.Dir, "remote", "remove", "upstream")
	_, _, err := executeCommand(t, f.Deps, "", "pr", "create", "--dry-run", "--no-render")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.AI.Input.CommitLog, "parent advancement") {
		t.Fatal("fallback used the fork's base")
	}
	if remotes := testutil.Git(t, f.Dir, "remote"); remotes != "origin" {
		t.Fatalf("configured remotes changed: %s", remotes)
	}
	found := false
	for _, command := range f.Commands {
		if strings.HasPrefix(command, "git fetch") && strings.Contains(command, "https://github.com/parent/repo.git") {
			found = true
		}
	}
	if !found {
		t.Fatal("parent clone URL was not fetched")
	}
}
