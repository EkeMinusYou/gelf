package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EkeMinusYou/gelf/internal/testutil"
)

func TestStagedSummary(t *testing.T) {
	dir := testutil.Repo(t)
	testutil.Write(t, dir, "old.txt", "rename content\n")
	testutil.Write(t, dir, "deleted.txt", "\n-old\n")
	testutil.Git(t, dir, "add", ".")
	testutil.Git(t, dir, "commit", "-qm", "baseline")
	testutil.Git(t, dir, "mv", "old.txt", "日本語.txt")
	if err := os.Remove(filepath.Join(dir, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	testutil.Write(t, dir, "added.txt", "\n+new\n")
	testutil.Write(t, dir, "tab\tname.txt", "line\n")
	testutil.Write(t, dir, "binary.dat", "\x00binary")
	testutil.Git(t, dir, "add", "-A")
	summary, err := NewRepository(dir).StagedSummary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]FileDiff{}
	for _, file := range summary.Files {
		files[file.Name] = file
	}
	if len(files) != 5 || files["added.txt"].AddedLines != 2 || files["deleted.txt"].DeletedLines != 2 || files["日本語.txt"].OldName != "old.txt" || !files["binary.dat"].Binary || files["tab\tname.txt"].AddedLines != 1 {
		t.Fatalf("incorrect summary: %+v", summary)
	}
}

func TestRecentCommitSubjects(t *testing.T) {
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	repo := NewRepository(dir)
	if subjects, err := repo.RecentCommitSubjects(context.Background(), 2); err != nil || subjects != "" {
		t.Fatalf("unborn branch: %q %v", subjects, err)
	}
	dir = testutil.Repo(t)
	for _, subject := range []string{"first", "second", "third"} {
		testutil.Git(t, dir, "commit", "-q", "--allow-empty", "-m", subject)
	}
	if subjects, err := NewRepository(dir).RecentCommitSubjects(context.Background(), 2); err != nil || subjects != "third\nsecond" {
		t.Fatalf("subjects: %q %v", subjects, err)
	}
}

func TestPushStatusAndLease(t *testing.T) {
	ctx := context.Background()
	dir := testutil.Repo(t)
	bare := filepath.Join(t.TempDir(), "remote.git")
	testutil.Git(t, dir, "init", "--bare", "-q", bare)
	testutil.Git(t, dir, "remote", "add", "origin", bare)
	testutil.Git(t, dir, "checkout", "-qb", "feature")
	repo := NewRepository(dir)
	target, err := repo.PushTarget(ctx, "feature")
	if err != nil {
		t.Fatal(err)
	}
	status, err := repo.PushStatus(ctx, target)
	if err != nil || status.RemoteSHA != "" {
		t.Fatalf("new branch: %+v %v", status, err)
	}
	if err := repo.Push(ctx, status, false); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Git(t, dir, "rev-parse", "--abbrev-ref", "@{u}"); got != "origin/feature" {
		t.Fatalf("upstream=%s", got)
	}
	target, _ = repo.PushTarget(ctx, "feature")
	status, err = repo.PushStatus(ctx, target)
	if err != nil || !status.UpToDate() {
		t.Fatalf("equal: %+v %v", status, err)
	}
	peer := filepath.Join(t.TempDir(), "peer")
	testutil.Git(t, dir, "clone", "-q", "--branch", "feature", bare, peer)
	testutil.Git(t, peer, "commit", "--allow-empty", "-qm", "remote advance")
	testutil.Git(t, peer, "push", "-q", "origin", "feature")
	status, err = repo.PushStatus(ctx, target)
	if err != nil || !status.BehindOnly() || status.Diverged() {
		t.Fatalf("stale tracking ref must refresh to behind: %+v %v", status, err)
	}
	testutil.Git(t, dir, "commit", "--allow-empty", "-qm", "local advance")
	status, err = repo.PushStatus(ctx, target)
	if err != nil || !status.Diverged() {
		t.Fatalf("diverged: %+v %v", status, err)
	}
	// A remote update after confirmation must invalidate the captured lease.
	testutil.Git(t, peer, "commit", "--allow-empty", "-qm", "remote raced")
	testutil.Git(t, peer, "push", "-q", "origin", "feature")
	if err := repo.Push(ctx, status, true); err == nil {
		t.Fatal("stale lease was accepted")
	}
	status, err = repo.PushStatus(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Push(ctx, status, true); err != nil {
		t.Fatal(err)
	}
	if remote := testutil.Git(t, dir, "ls-remote", "--heads", bare, "feature"); !strings.HasPrefix(remote, status.HeadSHA) {
		t.Fatalf("wrong pushed commit: %s", remote)
	}
	testutil.Git(t, dir, "commit", "--allow-empty", "-qm", "ahead")
	status, err = repo.PushStatus(ctx, target)
	if err != nil || status.Ahead != 1 || status.Behind != 0 {
		t.Fatalf("ahead: %+v %v", status, err)
	}
	if err := repo.Push(ctx, status, false); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "push", "-q", "origin", "--delete", "feature")
	status, err = repo.PushStatus(ctx, target)
	if err != nil || status.RemoteSHA != "" || status.UpToDate() {
		t.Fatalf("deleted branch: %+v %v", status, err)
	}
}

func TestPushRemoteAndBranchMapping(t *testing.T) {
	dir := testutil.Repo(t)
	ctx := context.Background()
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/parent/repo.git")
	testutil.Git(t, dir, "remote", "add", "fork", "https://github.com/fork/repo.git")
	testutil.Git(t, dir, "config", "branch.main.remote", "origin")
	testutil.Git(t, dir, "config", "branch.main.merge", "refs/heads/remote-main")
	repo := NewRepository(dir)
	target, err := repo.PushTarget(ctx, "main")
	if err != nil || target.Branch != "remote-main" || !target.HasUpstream {
		t.Fatalf("mapping: %+v %v", target, err)
	}
	testutil.Git(t, dir, "config", "remote.pushDefault", "fork")
	target, err = repo.PushTarget(ctx, "main")
	if err != nil || target.RemoteName != "fork" || target.Branch != "main" || target.HasUpstream {
		t.Fatalf("pushDefault: %+v %v", target, err)
	}
	testutil.Git(t, dir, "config", "branch.main.pushRemote", "origin")
	testutil.Git(t, dir, "remote", "set-url", "--push", "origin", "https://github.com/other/repo.git")
	target, err = repo.PushTarget(ctx, "main")
	if err != nil || target.RemoteName != "origin" || target.PushURL != "https://github.com/other/repo.git" {
		t.Fatalf("pushRemote/pushURL: %+v %v", target, err)
	}
}

func TestFetchBaseWithoutRemoteAndCommitError(t *testing.T) {
	dir := testutil.Repo(t)
	remote := testutil.Repo(t)
	testutil.Git(t, remote, "commit", "--allow-empty", "-qm", "base advance")
	repo := NewRepository(dir)
	before := testutil.Git(t, dir, "rev-parse", "HEAD")
	sha, err := repo.FetchBase(context.Background(), remote, "main")
	if err != nil || sha != testutil.Git(t, remote, "rev-parse", "HEAD") {
		t.Fatalf("fetch=%s %v", sha, err)
	}
	if after := testutil.Git(t, dir, "rev-parse", "HEAD"); after != before {
		t.Fatal("fetch moved HEAD")
	}
	if remotes := testutil.Git(t, dir, "remote"); remotes != "" {
		t.Fatal("fetch added a configured remote")
	}
	hook := filepath.Join(t.TempDir(), "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'hook failure details' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "config", "core.hooksPath", filepath.Dir(hook))
	testutil.Write(t, dir, "change", "content")
	testutil.Git(t, dir, "add", ".")
	err = repo.Commit(context.Background(), "fix: change")
	if err == nil || !strings.Contains(err.Error(), "hook failure details") {
		t.Fatalf("commit diagnostics lost: %v", err)
	}
}

func TestPushStatusUsesPushURLWithoutOverwritingPullRefs(t *testing.T) {
	dir := testutil.Repo(t)
	fetchRepo, pushRepo := filepath.Join(t.TempDir(), "fetch.git"), filepath.Join(t.TempDir(), "push.git")
	testutil.Git(t, dir, "clone", "--bare", "-q", dir, fetchRepo)
	testutil.Git(t, dir, "clone", "--bare", "-q", dir, pushRepo)
	peer := filepath.Join(t.TempDir(), "peer")
	testutil.Git(t, dir, "clone", "-q", fetchRepo, peer)
	testutil.Git(t, peer, "commit", "--allow-empty", "-qm", "fetch destination advanced")
	testutil.Git(t, peer, "push", "-q", "origin", "main")
	testutil.Git(t, dir, "remote", "add", "origin", fetchRepo)
	testutil.Git(t, dir, "remote", "set-url", "--push", "origin", pushRepo)
	testutil.Git(t, dir, "fetch", "-q", "origin")
	tracked := testutil.Git(t, dir, "rev-parse", "refs/remotes/origin/main")
	repo := NewRepository(dir)
	target, err := repo.PushTarget(context.Background(), "main")
	if err != nil {
		t.Fatal(err)
	}
	status, err := repo.PushStatus(context.Background(), target)
	if err != nil || !status.UpToDate() {
		t.Fatalf("push URL status: %+v %v", status, err)
	}
	if got := testutil.Git(t, dir, "rev-parse", "refs/remotes/origin/main"); got != tracked {
		t.Fatal("push refresh overwrote pull tracking ref")
	}
}
