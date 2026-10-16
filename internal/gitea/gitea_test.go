package gitea

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/EkeMinusYou/gelf/internal/ai"
	"github.com/EkeMinusYou/gelf/internal/forge"
	"github.com/EkeMinusYou/gelf/internal/process"
	"github.com/EkeMinusYou/gelf/internal/testutil"
)

func teaClient(t *testing.T, handle func(method, endpoint string, body io.Reader) (string, error)) *Client {
	t.Helper()
	c := NewClient(testutil.RunnerFunc(func(ctx context.Context, name string, args []string, body io.Reader) (process.Result, error) {
		if name != "tea" {
			t.Fatalf("unexpected binary: %s", name)
		}
		if args[0] == "logins" {
			return process.Result{Stdout: `[{"name":"work","url":"https://git.example:3000/gitea","ssh_host":"git-ssh.example"}]`}, nil
		}
		joined := strings.Join(args, " ")
		if !strings.HasPrefix(joined, "api --login work --method ") || !strings.Contains(joined, "--include --output -") {
			t.Fatalf("invalid tea command: %v", args)
		}
		if body != nil && !strings.Contains(joined, "--data @-") {
			t.Fatal("payload was not passed through stdin")
		}
		if ctx.Err() != nil {
			return process.Result{}, ctx.Err()
		}
		data, err := handle(args[4], args[len(args)-1], body)
		return process.Result{Stdout: data, Stderr: "HTTP/1.1 200 OK\nContent-Type: application/json\n"}, err
	}))
	if _, err := c.ResolveRemote(context.Background(), "https://git.example:3000/gitea/fork/renamed.git", ""); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLoginAndRemoteIdentity(t *testing.T) {
	logins := `[{"name":"other","url":"https://other.example"},{"name":"work","url":"https://git.example:3000/gitea","ssh_host":"git-ssh.example"}]`
	client := NewClient(testutil.RunnerFunc(func(ctx context.Context, name string, args []string, body io.Reader) (process.Result, error) {
		if name != "tea" || strings.Join(args, " ") != "logins list --output json" || body != nil {
			t.Fatalf("invalid login lookup: %s %v", name, args)
		}
		return process.Result{Stdout: logins}, nil
	}))
	for _, remote := range []string{
		"https://git.example:3000/gitea/team/repo.git",
		"git@git.example:team/repo.git",
		"ssh://git@git.example:2222/team/repo.git",
		"git@git-ssh.example:team/repo.git",
	} {
		info, err := client.ResolveRemote(context.Background(), remote, "")
		if err != nil || info.Host != "git.example:3000" || info.FullName() != "team/repo" || client.Login != "work" {
			t.Fatalf("remote=%s repo=%+v login=%s err=%v", remote, info, client.Login, err)
		}
	}
	for _, remote := range []string{"https://wrong.example/team/repo.git", "http://git.example:3000/gitea/team/repo.git", "https://git.example:3000/other/team/repo.git", "git@wrong.example:team/repo.git", "/tmp/repo.git"} {
		if _, err := client.ResolveRemote(context.Background(), remote, "work"); err == nil {
			t.Fatalf("invalid remote accepted: %s", remote)
		}
	}
	logins = `[{"name":"work","url":"https://git.example"},{"name":"personal","url":"https://git.example"}]`
	if _, err := client.ResolveRemote(context.Background(), "git@git.example:team/repo.git", ""); err == nil || !strings.Contains(err.Error(), "multiple tea logins") {
		t.Fatalf("ambiguous logins accepted: %v", err)
	}
	if _, err := client.ResolveRemote(context.Background(), "git@git.example:team/repo.git", "personal"); err != nil || client.Login != "personal" {
		t.Fatalf("explicit login was not selected: %s %v", client.Login, err)
	}
	if _, err := client.ResolveRemote(context.Background(), "git@git.example:team/repo.git", "missing"); err == nil {
		t.Fatal("unknown login accepted")
	}
	logins = "[]"
	if _, err := client.ResolveRemote(context.Background(), "git@git.example:team/repo.git", ""); err == nil || !strings.Contains(err.Error(), "tea login add") {
		t.Fatalf("missing login: %v", err)
	}
}

func TestRepositoryAndPaginatedPRSelection(t *testing.T) {
	data := `[{"number":1,"state":"open","head":{"ref":"feature","repo":{"full_name":"base/repo"}}},{"number":2,"state":"closed","head":{"ref":"feature","repo":{"full_name":"fork/renamed"}}},{"number":3,"state":"open","head":{"ref":"feature","repo":null}}]`
	client := teaClient(t, func(method, endpoint string, body io.Reader) (string, error) {
		parsed, _ := url.Parse(endpoint)
		switch parsed.Path {
		case "repos/fork/renamed":
			return `{"owner":{"login":"fork"},"name":"renamed","default_branch":"main","clone_url":"ssh://git@git.example:2222/fork/renamed.git","parent":{"owner":{"login":"base"},"name":"repo","default_branch":"main"}}`, nil
		case "repos/base/repo/pulls":
			if parsed.Query().Get("state") != "open" || parsed.Query().Get("limit") != "100" {
				t.Errorf("invalid query: %s", endpoint)
			}
			switch parsed.Query().Get("page") {
			case "1":
				return data, nil
			case "2":
				return `[{"number":4,"state":"open","draft":true,"head":{"ref":"feature","repo":{"full_name":"fork/renamed"}},"base":{"ref":"release"}}]`, nil
			case "3":
				return `[]`, nil
			}
		}
		return "", fmt.Errorf("unexpected endpoint %s", endpoint)
	})
	ctx := context.Background()
	head, base, err := client.Repo(ctx, "fork/renamed")
	if err != nil || head.FullName() != "fork/renamed" || base.FullName() != "base/repo" || base.DefaultBranch != "main" || head.CloneURL != "ssh://git@git.example:2222/fork/renamed.git" {
		t.Fatalf("head=%+v base=%+v err=%v", head, base, err)
	}
	pr, err := client.FindPullRequest(ctx, *base, *head, "feature")
	if err != nil || pr == nil || pr.Number != 4 || !pr.IsDraft || pr.Base.Ref != "release" {
		t.Fatalf("pr=%+v err=%v", pr, err)
	}
	data = `[{"number":5,"state":"open","head":{"ref":"feature","repo":{"full_name":"fork/renamed"}}}]`
	if _, err := client.FindPullRequest(ctx, *base, *head, "feature"); err == nil || !strings.Contains(err.Error(), "multiple open PRs") {
		t.Fatalf("ambiguous PRs accepted: %v", err)
	}
	pr, err = client.FindPullRequest(ctx, *base, *head, "other")
	if err != nil || pr != nil {
		t.Fatalf("unrelated PR matched: %+v %v", pr, err)
	}
}

func TestPublicationAndDraftPreservation(t *testing.T) {
	var payload map[string]any
	client := teaClient(t, func(method, endpoint string, body io.Reader) (string, error) {
		if method == "GET" {
			return `{"number":42,"title":"WIP: previous","draft":true}`, nil
		}
		if method != "POST" && method != "PATCH" {
			t.Errorf("unexpected method: %s", method)
		}
		if !strings.HasPrefix(endpoint, "repos/team/repo/pulls") {
			t.Errorf("wrong publication endpoint: %s", endpoint)
		}
		if err := json.NewDecoder(body).Decode(&payload); err != nil {
			t.Error(err)
		}
		return `{"number":42,"html_url":"https://git.example/team/repo/pulls/42","draft":true}`, nil
	})
	base := forge.RepoInfo{Host: client.base.Host, Owner: "team", Name: "repo"}
	head := forge.RepoInfo{Host: client.base.Host, Owner: "fork", Name: "renamed"}
	content := &ai.PullRequestContent{Title: "title", Body: "body\nwith newline"}
	ctx := context.Background()
	if _, err := client.Create(ctx, base, head, "feature/topic", "main", content, true); err != nil {
		t.Fatal(err)
	}
	if payload["head"] != "fork/renamed:feature/topic" || payload["base"] != "main" || payload["title"] != "WIP: title" || payload["body"] != content.Body || payload["allow_maintainer_edit"] != true {
		t.Fatalf("incorrect fork payload: %v", payload)
	}
	payload = nil
	if _, err := client.Create(ctx, base, base, "feature", "main", content, false); err != nil {
		t.Fatal(err)
	}
	if payload["head"] != "feature" || payload["title"] != "title" {
		t.Fatalf("incorrect same-repository payload: %v", payload)
	}
	payload = nil
	if _, err := client.Update(ctx, base, 42, content); err != nil {
		t.Fatal(err)
	}
	if payload["title"] != "WIP: title" || payload["body"] != content.Body || len(payload) != 2 || content.Title != "title" {
		t.Fatalf("draft state or unrelated fields changed: %v content=%+v", payload, content)
	}
}

func TestAPIFailuresAndCancellation(t *testing.T) {
	denied := errors.New("tea failed")
	client := teaClient(t, func(string, string, io.Reader) (string, error) { return "", denied })
	if _, _, err := client.Repo(context.Background(), "team/repo"); !errors.Is(err, denied) {
		t.Fatalf("lost CLI error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := client.Repo(ctx, "team/repo"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
	for _, tc := range []struct{ status, body, want string }{
		{"HTTP/1.1 401 Unauthorized", `{"message":"bad credentials"}`, "HTTP 401"},
		{"HTTP/2.0 403 Forbidden", `{"message":"access denied"}`, "HTTP 403"},
		{"HTTP/1.1 404 Not Found", `{"message":"not found"}`, "HTTP 404"},
		{"HTTP/1.1 200 OK", "not JSON", "failed to parse"},
		{"HTTP/1.1 200 OK", "{}", "incomplete"},
		{"", "{}", "did not report an HTTP status"},
	} {
		client.Runner = testutil.RunnerFunc(func(context.Context, string, []string, io.Reader) (process.Result, error) {
			// HTTP failures can still have a zero tea exit status.
			return process.Result{Stdout: tc.body, Stderr: tc.status}, nil
		})
		if _, _, err := client.Repo(context.Background(), "team/repo"); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("status=%s err=%v", tc.status, err)
		}
	}
}

func TestLocalTemplates(t *testing.T) {
	client := NewClient(nil)
	for _, path := range []string{".gitea/PULL_REQUEST_TEMPLATE.md", ".gitea/pull_request_template.md", ".github/PULL_REQUEST_TEMPLATE.md", ".gitea/PULL_REQUEST_TEMPLATE/01.md"} {
		root := t.TempDir()
		testutil.Write(t, root, path, "template")
		template, err := client.FindPullRequestTemplate(context.Background(), root, "team")
		if err != nil || template == nil || !strings.EqualFold(template.Path, path) || template.Content != "template" {
			t.Fatalf("template=%+v err=%v", template, err)
		}
	}
	root := t.TempDir()
	testutil.Write(t, root, ".github/PULL_REQUEST_TEMPLATE.md", "GitHub")
	testutil.Write(t, root, ".gitea/PULL_REQUEST_TEMPLATE.md", "Gitea")
	template, err := client.FindPullRequestTemplate(context.Background(), root, "team")
	if err != nil || template == nil || template.Content != "Gitea" {
		t.Fatalf("Gitea template did not take priority: %+v %v", template, err)
	}
	root = t.TempDir()
	testutil.Write(t, root, ".github/PULL_REQUEST_TEMPLATE.md", "GitHub")
	testutil.Write(t, root, ".gitea/PULL_REQUEST_TEMPLATE/01.md", "Gitea directory")
	template, err = client.FindPullRequestTemplate(context.Background(), root, "team")
	if err != nil || template == nil || template.Content != "Gitea directory" {
		t.Fatalf("Gitea directory lost priority over GitHub: %+v %v", template, err)
	}
	template, err = client.FindPullRequestTemplate(context.Background(), t.TempDir(), "team")
	if err != nil || template != nil {
		t.Fatalf("missing template should not invoke tea: %+v %v", template, err)
	}
}

func TestInstalledTeaAPI(t *testing.T) {
	if _, err := exec.LookPath("tea"); err != nil {
		t.Skip("tea is not installed")
	}
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	for _, name := range []string{"GITEA_SERVER_URL", "GITEA_SERVER_TOKEN", "GITEA_SERVER_USER", "GITEA_SERVER_PASSWORD"} {
		t.Setenv(name, "")
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "token isolated-test-token" {
			t.Error("tea did not supply its stored credentials")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/gitea/api/v1/repos/team/repo/pulls" {
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if payload["head"] != "feature" || payload["body"] != "body\nwith newline" {
				t.Errorf("tea stdin payload was corrupted: %v", payload)
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"number":42,"html_url":"https://git.example/team/repo/pulls/42"}`)
			return
		}
		if r.URL.Path != "/gitea/api/v1/repos/team/repo" {
			t.Errorf("wrong tea endpoint: %s", r.URL)
		}
		if requests == 1 {
			fmt.Fprint(w, `{"owner":{"login":"team"},"name":"repo","default_branch":"main"}`)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"denied"}`)
	}))
	defer server.Close()
	testutil.Write(t, root, "tea/config.yml", fmt.Sprintf("logins:\n  - name: isolated\n    url: %s/gitea\n    user: tester\n    token: isolated-test-token\n    default: true\n", server.URL))
	client := NewClient(process.CommandRunner{Dir: root})
	if _, err := client.ResolveRemote(context.Background(), server.URL+"/gitea/team/repo.git", "isolated"); err != nil {
		t.Fatal(err)
	}
	repo, _, err := client.Repo(context.Background(), "team/repo")
	if err != nil || repo.DefaultBranch != "main" {
		t.Fatalf("real tea API request failed: %+v %v", repo, err)
	}
	if _, err := client.Create(context.Background(), *repo, *repo, "feature", "main", &ai.PullRequestContent{Title: "title", Body: "body\nwith newline"}, false); err != nil {
		t.Fatalf("real tea API publication failed: %v", err)
	}
	if _, _, err := client.Repo(context.Background(), "team/repo"); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("real tea API error was ignored: %v", err)
	}
}
