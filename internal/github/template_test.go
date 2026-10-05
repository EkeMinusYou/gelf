package github

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EkeMinusYou/gelf/internal/process"
	"github.com/EkeMinusYou/gelf/internal/testutil"
)

func TestLocalTemplateNeedsNoToken(t *testing.T) {
	dir := t.TempDir()
	testutil.Write(t, dir, ".github/PULL_REQUEST_TEMPLATE/02.md", "second")
	testutil.Write(t, dir, ".github/PULL_REQUEST_TEMPLATE/01.md", "first")
	client := NewClient(testutil.RunnerFunc(func(context.Context, string, []string, io.Reader) (process.Result, error) {
		t.Fatal("local template requested authentication")
		return process.Result{}, nil
	}))
	template, err := client.FindPullRequestTemplate(context.Background(), dir, "owner")
	if err != nil || template.Content != "first" || template.Source != "repo" {
		t.Fatalf("template=%+v %v", template, err)
	}
}

func TestOrganizationTemplateHTTPAndErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token test-token" {
			t.Error("missing authentication")
		}
		if r.URL.Path == "/repos/owner/.github/contents/.github/PULL_REQUEST_TEMPLATE.md" {
			fmt.Fprintf(w, `{"type":"file","encoding":"base64","content":%q}`, base64.StdEncoding.EncodeToString([]byte("## Purpose")))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	client := NewClient(testutil.RunnerFunc(func(context.Context, string, []string, io.Reader) (process.Result, error) {
		return process.Result{Stdout: "test-token"}, nil
	}))
	client.HTTP, client.APIBaseURL = server.Client(), server.URL
	template, err := client.FindPullRequestTemplate(context.Background(), t.TempDir(), "owner")
	if err != nil || template.Content != "## Purpose" || template.Source != "org" {
		t.Fatalf("template=%+v %v", template, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := client.fetchGitHubContent(ctx, "test-token", "owner", ".github", "path"); err == nil {
		t.Fatal("HTTP cancellation ignored")
	}
	if _, err := decodeBase64("invalid"); err == nil {
		t.Fatal("invalid base64 accepted")
	}
	if content, err := decodeBase64("cHVy\ncG9zZQ=="); err != nil || !strings.Contains(content, "purpose") {
		t.Fatalf("base64=%q %v", content, err)
	}
}
