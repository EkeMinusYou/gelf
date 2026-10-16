package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EkeMinusYou/gelf/internal/config"
	"github.com/EkeMinusYou/gelf/internal/gitea"
	"github.com/EkeMinusYou/gelf/internal/process"
	"github.com/EkeMinusYou/gelf/internal/testutil"
)

func TestGiteaPRWorkflow(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		flags                   []string
		existing, draft         bool
		method, base, wantTitle string
	}{
		{name: "auto dry run", flags: []string{"--dry-run", "--no-render"}, base: "main"},
		{name: "auto separate push URL", flags: []string{"--dry-run", "--no-render", "--provider", "auto"}, base: "main"},
		{name: "auto create", flags: []string{"--yes"}, method: "POST", base: "main", wantTitle: "generated title"},
		{name: "dry run flag overrides configuration", flags: []string{"--dry-run", "--no-render", "--provider", "gitea"}, base: "main"},
		{name: "create from configuration", flags: []string{"--yes"}, method: "POST", base: "main", wantTitle: "generated title"},
		{name: "draft", flags: []string{"--yes", "--draft"}, method: "POST", base: "main", wantTitle: "WIP: generated title"},
		{name: "update", flags: []string{"--yes", "--update"}, existing: true, method: "PATCH", base: "release", wantTitle: "generated title"},
		{name: "update draft preserves state", flags: []string{"--yes", "--update"}, existing: true, draft: true, method: "PATCH", base: "release", wantTitle: "WIP: generated title"},
		{name: "existing PR stops before generation", flags: []string{"--yes"}, existing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPRFixture(t)
			testutil.Write(t, f.Dir, ".gitea/PULL_REQUEST_TEMPLATE.md", "## Gitea purpose")
			f.Config.PRProvider = "gitea"
			if strings.HasPrefix(tc.name, "auto") {
				f.Config.PRProvider = "auto"
			}
			if tc.name == "auto separate push URL" {
				f.Config.PRProvider = "github"
			}
			if strings.Contains(tc.name, "overrides") {
				f.Config.PRProvider = "github"
			}
			// The Gitea workflow must not depend on gh being installed or authenticated.
			f.Deps.GitHub = nil
			published := false
			var serverURL string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "token test-token" {
					t.Error("missing Gitea authentication")
				}
				switch r.URL.Path {
				case "/git/api/v1/repos/fork/renamed":
					fmt.Fprintf(w, `{"owner":{"login":"fork"},"name":"renamed","default_branch":"main","parent":{"owner":{"login":"parent"},"name":"repo"}}`)
				case "/git/api/v1/repos/parent/repo":
					fmt.Fprintf(w, `{"owner":{"login":"parent"},"name":"repo","default_branch":"main","clone_url":%q}`, serverURL+"/parent/repo.git")
				case "/git/api/v1/repos/parent/repo/pulls", "/git/api/v1/repos/parent/repo/pulls/42":
					if r.Method == http.MethodGet {
						pr := fmt.Sprintf(`{"number":42,"title":"previous title","html_url":%q,"state":"open","draft":%t,"head":{"ref":"feature","repo":{"full_name":"fork/renamed"}},"base":{"ref":"release"}}`, serverURL+"/parent/repo/pulls/42", tc.draft)
						if strings.HasSuffix(r.URL.Path, "/42") {
							fmt.Fprint(w, pr)
							return
						}
						if tc.existing && r.URL.Query().Get("page") == "1" {
							fmt.Fprintf(w, "[%s]", pr)
						} else {
							fmt.Fprint(w, `[]`)
						}
						return
					}
					published = true
					var payload map[string]any
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					if r.Method != tc.method || payload["title"] != tc.wantTitle || payload["body"] != "generated body" {
						t.Errorf("unexpected publication: %s %v", r.Method, payload)
					}
					if r.Method == http.MethodPost && (payload["head"] != "fork/renamed:feature" || payload["base"] != tc.base) {
						t.Errorf("wrong fork or base: %v", payload)
					}
					fmt.Fprintf(w, `{"number":42,"html_url":%q,"draft":%t}`, serverURL+"/parent/repo/pulls/42", tc.draft || strings.Contains(tc.name, "draft"))
				default:
					t.Errorf("unexpected Gitea request: %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			serverURL = server.URL + "/git"
			if tc.name == "create from configuration" {
				f.Config.GiteaLogin = "work"
			}
			f.Deps.Gitea = gitea.NewClient(testutil.RunnerFunc(func(ctx context.Context, name string, args []string, body io.Reader) (process.Result, error) {
				if name != "tea" {
					t.Fatalf("unexpected binary: %s", name)
				}
				if args[0] == "logins" {
					return process.Result{Stdout: fmt.Sprintf(`[{"name":"work","url":%q}]`, serverURL)}, nil
				}
				if !strings.HasPrefix(strings.Join(args, " "), "api --login work --method ") {
					t.Fatalf("tea API did not pin the matching login: %v", args)
				}
				req, err := http.NewRequestWithContext(ctx, args[4], serverURL+"/api/v1/"+args[len(args)-1], body)
				if err != nil {
					return process.Result{}, err
				}
				req.Header.Set("Authorization", "token test-token")
				resp, err := server.Client().Do(req)
				if err != nil {
					return process.Result{}, err
				}
				defer resp.Body.Close()
				data, err := io.ReadAll(resp.Body)
				return process.Result{Stdout: string(data), Stderr: "HTTP/1.1 " + resp.Status}, err
			}))
			originalRunner := f.Deps.Git.Runner
			f.Deps.Git.Runner = testutil.RunnerFunc(func(ctx context.Context, name string, args []string, input io.Reader) (process.Result, error) {
				mapped := append([]string(nil), args...)
				for i, arg := range mapped {
					if strings.HasPrefix(arg, serverURL+"/") {
						mapped[i] = strings.Replace(arg, serverURL, "https://github.com", 1)
						mapped[i] = strings.ReplaceAll(mapped[i], "/fork/renamed.git", "/fork/repo.git")
					}
				}
				result, err := originalRunner.Run(ctx, name, mapped, input)
				if len(args) >= 3 && args[0] == "remote" && args[1] == "get-url" {
					if tc.name == "auto separate push URL" && !strings.Contains(strings.Join(args, " "), "--push") {
						return result, err
					}
					result.Stdout = strings.ReplaceAll(result.Stdout, "https://github.com", serverURL)
					result.Stdout = strings.ReplaceAll(result.Stdout, "/fork/repo.git", "/fork/renamed.git")
				}
				return result, err
			})
			// Exercise clone URL fallback without a configured parent remote.
			testutil.Git(t, f.Dir, "remote", "remove", "upstream")
			args := append([]string{"pr", "create"}, tc.flags...)
			out, stderr, err := executeCommand(t, f.Deps, "", args...)
			if err != nil {
				t.Fatalf("out=%q stderr=%q err=%v", out, stderr, err)
			}
			if published != (tc.method != "") {
				t.Fatalf("published=%t expected=%s", published, tc.method)
			}
			if tc.base != "" && (f.AI.Input.BaseBranch != tc.base || f.AI.Input.Template != "## Gitea purpose" || strings.Contains(f.AI.Input.Diff, "parent-only") != (tc.base == "release")) {
				t.Fatalf("wrong comparison or template: %+v", f.AI.Input)
			}
			if tc.existing && tc.method == "" && (f.AI.Model != "" || !strings.Contains(stderr, "Pull request already exists")) {
				t.Fatalf("existing PR did not stop before generation: %+v stderr=%q", f.AI, stderr)
			}
			for _, command := range f.Commands {
				if tc.method == "" && strings.HasPrefix(command, "git push") {
					t.Fatalf("preview or existing PR pushed: %s", command)
				}
			}
			if tc.method != "" && !strings.Contains(out, serverURL+"/parent/repo/pulls/42") {
				t.Fatalf("published PR URL missing: %q", out)
			}
		})
	}
}

func TestPRRejectsInvalidProviderBeforeGitOperations(t *testing.T) {
	deps := dependencies{LoadConfig: func() (*config.Config, error) { return testConfig(), nil }}
	for _, provider := range []string{"invalid", ""} {
		if _, _, err := executeCommand(t, deps, "", "pr", "create", "--provider", provider); err == nil || !strings.Contains(err.Error(), "--provider") {
			t.Fatalf("invalid provider=%q err=%v", provider, err)
		}
	}
}

func TestGiteaMissingLoginStopsBeforePushAndGeneration(t *testing.T) {
	f := newPRFixture(t)
	f.Config.PRProvider = "gitea"
	f.Deps.GitHub = nil
	f.Deps.Gitea = gitea.NewClient(testutil.RunnerFunc(func(context.Context, string, []string, io.Reader) (process.Result, error) {
		return process.Result{Stdout: "[]"}, nil
	}))
	_, _, err := executeCommand(t, f.Deps, "", "pr", "create", "--yes")
	if err == nil || !strings.Contains(err.Error(), "tea login add") || f.AI.Model != "" {
		t.Fatalf("missing login did not stop generation: %v AI=%+v", err, f.AI)
	}
	for _, command := range f.Commands {
		if strings.HasPrefix(command, "git push") || strings.HasPrefix(command, "git fetch") {
			t.Fatalf("missing login changed remote refs: %s", command)
		}
	}
}
