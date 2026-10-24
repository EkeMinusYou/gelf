package ai

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EkeMinusYou/gelf/internal/config"
	"google.golang.org/genai"
)

func cacheTestClient(calls *int) Client {
	return &VertexAIClient{generator: generatorFunc(func(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		*calls += 1
		return response(&genai.Part{Text: fmt.Sprintf("fix: generated message %d", *calls)}), nil
	})}
}

func TestCommitCacheReusesExactValidatedOutputAcrossClients(t *testing.T) {
	cache := &CommitCache{Dir: filepath.Join(t.TempDir(), "cache")}
	cfg := &config.Config{ProjectID: "project", Location: "us", APIVersion: "v1", CommitThinking: "minimal"}
	root := t.TempDir()
	input := CommitInput{Diff: "+change", DiffStat: "file | 1 +", Branch: "topic", RecentCommits: "fix: style", Language: "english"}
	calls := 0
	first, err := cache.Wrap(cacheTestClient(&calls), cfg, "model", root, "full diff").GenerateCommitMessage(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	var progress string
	input.OnProgress = func(text string) { progress = text }
	client := cache.Wrap(cacheTestClient(&calls), cfg, "model", root, "full diff")
	second, err := client.GenerateCommitMessage(context.Background(), input)
	if err != nil || second != first || progress != first || calls != 1 {
		t.Fatalf("cache changed output or called the API: first=%q second=%q progress=%q calls=%d err=%v", first, second, progress, calls, err)
	}
	if _, err := client.ReviseCommitMessage(context.Background(), input, first, "clarify"); err != nil || calls != 2 {
		t.Fatalf("revision was cached: calls=%d err=%v", calls, err)
	}
	third, err := client.GenerateCommitMessage(context.Background(), input)
	if err != nil || third != first || calls != 2 {
		t.Fatal("revision replaced the initial prompt's cached output")
	}
	entries, err := os.ReadDir(cache.Dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache is not bounded to one entry per worktree: %v %v", entries, err)
	}
	info, err := os.Stat(filepath.Join(cache.Dir, entries[0].Name()))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("cache message is not private: %v %v", info, err)
	}
	data, err := os.ReadFile(filepath.Join(cache.Dir, entries[0].Name()))
	if err != nil || strings.Contains(string(data), "full diff") || strings.Contains(string(data), "+change") {
		t.Fatalf("cache persisted source input: %q %v", data, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.GenerateCommitMessage(ctx, input); !errors.Is(err, context.Canceled) {
		t.Fatalf("cache ignored cancellation: %v", err)
	}
}

func TestCommitCacheInvalidatesEveryGenerationInput(t *testing.T) {
	type request struct {
		cfg                   config.Config
		input                 CommitInput
		model, root, fullDiff string
		endpoint              string
	}
	for _, tc := range []struct {
		name   string
		change func(*request)
	}{
		{"diff", func(r *request) { r.input.Diff += "\n+other" }},
		{"stat", func(r *request) { r.input.DiffStat += "\nother | 1 +" }},
		{"branch", func(r *request) { r.input.Branch = "other" }},
		{"recent commits", func(r *request) { r.input.RecentCommits = "feat: different style" }},
		{"language", func(r *request) { r.input.Language = "japanese" }},
		{"omitted diff content", func(r *request) { r.fullDiff += "\nchange beyond AI input limit" }},
		{"project", func(r *request) { r.cfg.ProjectID = "other" }},
		{"location", func(r *request) { r.cfg.Location = "global" }},
		{"API version", func(r *request) { r.cfg.APIVersion = "v1beta1" }},
		{"model", func(r *request) { r.model = "other-model" }},
		{"thinking", func(r *request) { r.cfg.CommitThinking = "high" }},
		{"repository", func(r *request) { r.root += "-other" }},
		{"endpoint", func(r *request) { r.endpoint = "https://different.invalid" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GOOGLE_VERTEX_BASE_URL", "")
			cache := &CommitCache{Dir: t.TempDir()}
			r := request{cfg: config.Config{ProjectID: "project", Location: "us", APIVersion: "v1", CommitThinking: "minimal"}, input: CommitInput{Diff: "limited diff", DiffStat: "file | 1 +", Branch: "topic", RecentCommits: "fix: style", Language: "english"}, model: "model", root: t.TempDir(), fullDiff: "full diff"}
			calls := 0
			client := cacheTestClient(&calls)
			if _, err := cache.Wrap(client, &r.cfg, r.model, r.root, r.fullDiff).GenerateCommitMessage(context.Background(), r.input); err != nil {
				t.Fatal(err)
			}
			tc.change(&r)
			t.Setenv("GOOGLE_VERTEX_BASE_URL", r.endpoint)
			if _, err := cache.Wrap(client, &r.cfg, r.model, r.root, r.fullDiff).GenerateCommitMessage(context.Background(), r.input); err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatalf("changed %s reused a message: calls=%d", tc.name, calls)
			}
		})
	}
}

func TestCommitCacheExpiresAndRecoversFromInvalidStorage(t *testing.T) {
	for _, reason := range []string{"expired", "future timestamp", "corrupt JSON", "oversized"} {
		t.Run(reason, func(t *testing.T) {
			now := time.Now()
			cache := &CommitCache{Dir: t.TempDir(), Now: func() time.Time { return now }}
			calls := 0
			client := cache.Wrap(cacheTestClient(&calls), &config.Config{}, "model", "repo", "diff")
			if _, err := client.GenerateCommitMessage(context.Background(), CommitInput{}); err != nil {
				t.Fatal(err)
			}
			switch reason {
			case "expired":
				now = now.Add(commitCacheMaxAge + time.Second)
			case "future timestamp":
				now = now.Add(-time.Second)
			default:
				data := "invalid JSON"
				if reason == "oversized" {
					data = strings.Repeat("x", commitCacheMaxBytes+1)
				}
				if err := os.WriteFile(filepath.Join(cache.Dir, cacheHash("repo")+".json"), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := client.GenerateCommitMessage(context.Background(), CommitInput{}); err != nil || calls != 2 {
				t.Fatalf("invalid cache blocked generation: calls=%d err=%v", calls, err)
			}
		})
	}
	cacheDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(cacheDir, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	cache := &CommitCache{Dir: cacheDir}
	calls := 0
	client := cache.Wrap(cacheTestClient(&calls), &config.Config{}, "model", "repo", "diff")
	for i := 0; i < 2; i++ {
		if _, err := client.GenerateCommitMessage(context.Background(), CommitInput{}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatal("unavailable cache prevented fresh generation")
	}
}

func TestCommitCacheDoesNotStoreFailures(t *testing.T) {
	cache := &CommitCache{Dir: t.TempDir()}
	calls := 0
	failure := errors.New("generation failed")
	base := &VertexAIClient{generator: generatorFunc(func(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		calls++
		return nil, failure
	})}
	client := cache.Wrap(base, &config.Config{}, "model", "repo", "diff")
	for i := 0; i < 2; i++ {
		if _, err := client.GenerateCommitMessage(context.Background(), CommitInput{}); !errors.Is(err, failure) {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(cache.Dir)
	if err != nil || len(entries) != 0 || calls != 2 {
		t.Fatalf("failed generation was cached: entries=%d calls=%d err=%v", len(entries), calls, err)
	}
}
