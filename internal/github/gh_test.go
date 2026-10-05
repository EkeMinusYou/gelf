package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/EkeMinusYou/gelf/internal/ai"
	"github.com/EkeMinusYou/gelf/internal/process"
	"github.com/EkeMinusYou/gelf/internal/testutil"
)

func TestRepoAndRemoteIdentity(t *testing.T) {
	for _, remote := range []string{"git@github.com:owner/repo.git", "ssh://git@github.com/owner/repo.git", "https://github.com/owner/repo.git", "https://github.com/owner/repo"} {
		info, err := RepoInfoFromRemoteURL(remote)
		if err != nil || info.Host != "github.com" || info.FullName() != "owner/repo" {
			t.Fatalf("%s: %+v %v", remote, info, err)
		}
	}
	for _, remote := range []string{"", "/tmp/repo.git", "https://github.com/owner/repo/extra"} {
		if _, err := RepoInfoFromRemoteURL(remote); err == nil {
			t.Fatalf("invalid URL accepted: %q", remote)
		}
	}
	client := NewClient(testutil.RunnerFunc(func(context.Context, string, []string, io.Reader) (process.Result, error) {
		return process.Result{Stdout: `{"owner":{"login":"fork"},"name":"repo","url":"https://github.com/fork/repo","defaultBranchRef":{"name":"main"},"parent":{"id":"parent-id","owner":{"login":"parent"},"name":"repo"}}`}, nil
	}))
	current, parent, err := client.Repo(context.Background(), "")
	if err != nil || current.FullName() != "fork/repo" || parent.FullName() != "parent/repo" || current.DefaultBranch != "main" {
		t.Fatalf("repo=%+v parent=%+v err=%v", current, parent, err)
	}
}

func TestFindPRRequiresExactOpenHeadAcrossPages(t *testing.T) {
	base := RepoInfo{Host: "github.com", Owner: "base", Name: "repo"}
	head := RepoInfo{Host: "github.com", Owner: "fork", Name: "repo"}
	data := `[[{"number":1,"state":"open","head":{"ref":"feature","repo":{"full_name":"base/repo"}}},{"number":2,"state":"merged","head":{"ref":"feature","repo":{"full_name":"fork/repo"}}}],[{"number":3,"state":"open","draft":true,"html_url":"https://github.com/base/repo/pull/3","head":{"ref":"feature","repo":{"full_name":"fork/repo"}},"base":{"ref":"release"}}]]`
	client := NewClient(testutil.RunnerFunc(func(ctx context.Context, name string, args []string, stdin io.Reader) (process.Result, error) {
		joined := strings.Join(args, " ")
		if name != "gh" || !strings.Contains(joined, "api --hostname github.com --paginate --slurp") || !strings.Contains(joined, "head=fork%3Afeature") || !strings.Contains(joined, "state=open") {
			t.Fatalf("invalid PR query: %s %s", name, joined)
		}
		return process.Result{Stdout: data}, nil
	}))
	pr, err := client.FindPullRequest(context.Background(), base, head, "feature")
	if err != nil || pr.Number != 3 || !pr.IsDraft || pr.Base.Ref != "release" {
		t.Fatalf("wrong PR: %+v %v", pr, err)
	}
	data = `[[{"number":2,"state":"closed","head":{"ref":"feature","repo":{"full_name":"fork/repo"}}}]]`
	pr, err = client.FindPullRequest(context.Background(), base, head, "feature")
	if err != nil || pr != nil {
		t.Fatalf("closed PR matched: %+v %v", pr, err)
	}
	data = `[[{"number":3,"state":"open","head":{"ref":"feature","repo":{"full_name":"fork/repo"}}},{"number":4,"state":"open","head":{"ref":"feature","repo":{"full_name":"fork/repo"}}}]]`
	if _, err := client.FindPullRequest(context.Background(), base, head, "feature"); err == nil {
		t.Fatal("ambiguous PR silently selected")
	}
}

func TestPublishPinsRepositoryAndHead(t *testing.T) {
	base := RepoInfo{Host: "github.example.com", Owner: "parent", Name: "repo"}
	head := RepoInfo{Host: "github.example.com", Owner: "fork", Name: "repo"}
	client := NewClient(testutil.RunnerFunc(func(ctx context.Context, name string, args []string, stdin io.Reader) (process.Result, error) {
		joined := strings.Join(args, " ")
		var payload map[string]any
		if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["body"] != "body\nwith newline" || !strings.Contains(joined, "--hostname github.example.com") || !strings.Contains(joined, "repos/parent/repo/pulls") {
			t.Fatalf("incorrect publication: %s body=%v", joined, payload)
		}
		if strings.Contains(joined, "--method POST") && (payload["head"] != "fork:remote-feature" || payload["head_repo"] != "repo" || payload["draft"] != true) {
			t.Fatalf("incorrect head: %v", payload)
		}
		return process.Result{Stdout: `{"number":42,"html_url":"https://github.example.com/parent/repo/pull/42"}`}, nil
	}))
	content := &ai.PullRequestContent{Title: "title", Body: "body\nwith newline"}
	if _, err := client.Create(context.Background(), base, head, "remote-feature", "main", content, true); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Update(context.Background(), base, 42, content); err != nil {
		t.Fatal(err)
	}
	// Deleted head repositories cannot be used as an identity fallback.
	var pr PullRequestInfo
	if err := json.Unmarshal([]byte(`{"head":{"ref":"feature","repo":null}}`), &pr); err != nil {
		t.Fatal(err)
	}
	if pr.Head.Repo != nil {
		t.Fatal(fmt.Sprint(pr.Head.Repo))
	}
}
