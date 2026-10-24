package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EkeMinusYou/gelf/internal/config"
	"google.golang.org/genai"
)

type generatorFunc func(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)

func (f generatorFunc) GenerateContent(ctx context.Context, model string, contents []*genai.Content, opts *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	return f(ctx, model, contents, opts)
}

func response(parts ...*genai.Part) *genai.GenerateContentResponse {
	return &genai.GenerateContentResponse{Candidates: []*genai.Candidate{{Content: &genai.Content{Parts: parts}, FinishReason: genai.FinishReasonStop}}}
}

func TestResponseExtraction(t *testing.T) {
	cases := []struct {
		name string
		resp *genai.GenerateContentResponse
		want string
	}{
		{"nil response", nil, ""},
		{"no candidates", &genai.GenerateContentResponse{}, ""},
		{"nil candidate", &genai.GenerateContentResponse{Candidates: []*genai.Candidate{nil}}, ""},
		{"missing content", &genai.GenerateContentResponse{Candidates: []*genai.Candidate{{}}}, ""},
		{"blocked", &genai.GenerateContentResponse{Candidates: []*genai.Candidate{{FinishReason: genai.FinishReasonSafety}}}, ""},
		{"empty", response(&genai.Part{Text: "  "}), ""},
		{"thought and multipart", response(nil, &genai.Part{Thought: true, Text: "internal reasoning"}, &genai.Part{Text: "  fix: "}, &genai.Part{Text: "correct behavior\n"}), "fix: correct behavior"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &VertexAIClient{model: "selected", generator: generatorFunc(func(ctx context.Context, model string, contents []*genai.Content, opts *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
				if model != "selected" {
					t.Fatalf("wrong model %s", model)
				}
				return tc.resp, nil
			})}
			got, err := client.GenerateCommitMessage(context.Background(), CommitInput{Diff: "+change", Language: "english"})
			if tc.want == "" {
				if err == nil {
					t.Fatal("invalid response accepted")
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
	truncated := response(&genai.Part{Text: "partial"})
	truncated.Candidates[0].FinishReason = genai.FinishReasonMaxTokens
	client := &VertexAIClient{generator: generatorFunc(func(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		return truncated, nil
	})}
	if _, err := client.GenerateCommitMessage(context.Background(), CommitInput{Diff: "diff", Language: "english"}); err == nil {
		t.Fatal("truncated response accepted")
	}
}

func TestPRGenerationAndRevisionShareValidation(t *testing.T) {
	for _, text := range []string{`{"title":" title ","body":" body "}`, "```json\n{\"title\":\" title \",\"body\":\" body \"}\n```", "```\n{\"title\":\" title \",\"body\":\" body \"}\n```", "invalid", `{"title":"","body":"body"}`, `{"title":"title","body":""}`, `{"title":"line\nbreak","body":"body"}`} {
		t.Run(text, func(t *testing.T) {
			client := &VertexAIClient{model: "gemini-4-argon", generator: generatorFunc(func(ctx context.Context, model string, contents []*genai.Content, opts *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
				if opts.ResponseMIMEType != "application/json" || opts.ResponseSchema == nil || len(opts.ResponseSchema.Required) != 2 {
					t.Fatal("structured output missing")
				}
				if opts.Temperature != nil {
					t.Fatal("Gemini should use its default sampling")
				}
				return response(&genai.Part{Thought: true, Text: "reasoning"}, &genai.Part{Text: text[:len(text)/2]}, &genai.Part{Text: text[len(text)/2:]}), nil
			})}
			input := PullRequestInput{Language: "english", Template: "## Purpose"}
			generated, err := client.GeneratePullRequestContent(context.Background(), input)
			revised, reviseErr := client.RevisePullRequestContent(context.Background(), input, &PullRequestContent{Title: "old", Body: "old"}, "shorten")
			valid := strings.Contains(text, `" title "`)
			if !valid {
				valid = strings.HasPrefix(text, "```")
			}
			if valid {
				if err != nil || reviseErr != nil || *generated != *revised || generated.Title != "title" || generated.Body != "body" {
					t.Fatalf("generation=%+v %v revision=%+v %v", generated, err, revised, reviseErr)
				}
			} else if err == nil || reviseErr == nil {
				t.Fatal("invalid PR content accepted")
			}
		})
	}
}

func TestCredentialsDoNotMutateEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(path, []byte(`{"type":"authorized_user","client_id":"test-id","client_secret":"test-secret","refresh_token":"test-token"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GELF_CREDENTIALS", path)
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(dir, "missing.json"))
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	before := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	client, err := NewVertexAIClient(context.Background(), &config.Config{ProjectID: "test-project", Location: "global"}, "test-model")
	if err != nil || client.model != "test-model" {
		t.Fatalf("explicit credentials failed: %v", err)
	}
	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") != before {
		t.Fatal("credential environment changed")
	}
	t.Setenv("GELF_CREDENTIALS", filepath.Join(dir, "absent.json"))
	if _, err := NewVertexAIClient(context.Background(), &config.Config{}, "test-model"); err == nil {
		t.Fatal("invalid explicit credentials accepted")
	}
}

func TestVertexRequestUsesStableAPIAndSupportedSettings(t *testing.T) {
	for _, name := range []string{"GELF_CREDENTIALS", "GOOGLE_API_KEY", "GEMINI_API_KEY", "GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_LOCATION"} {
		t.Setenv(name, "")
	}
	for _, version := range []string{"", "v1beta1"} {
		t.Run("version="+version, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				wantVersion := version
				if wantVersion == "" {
					wantVersion = "v1"
				}
				if r.URL.Path != "/"+wantVersion+"/publishers/google/models/gemini-3.8-flash:streamGenerateContent" || r.URL.Query().Get("alt") != "sse" {
					t.Errorf("unexpected request path: %s", r.URL.Path)
				}
				var body struct {
					GenerationConfig struct {
						Temperature    *float64                       `json:"temperature"`
						ThinkingConfig struct{ ThinkingLevel string } `json:"thinkingConfig"`
					} `json:"generationConfig"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.GenerationConfig.Temperature != nil || body.GenerationConfig.ThinkingConfig.ThinkingLevel != "LOW" {
					t.Errorf("unsupported generation settings: %+v", body.GenerationConfig)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"fix: done\"}]},\"finishReason\":\"STOP\"}]}\n\n")
			}))
			defer server.Close()
			t.Setenv("GOOGLE_VERTEX_BASE_URL", server.URL)
			client, err := NewVertexAIClient(context.Background(), &config.Config{APIVersion: version, CommitThinking: "minimal"}, "gemini-3.8-flash")
			if err != nil {
				t.Fatal(err)
			}
			got, err := client.GenerateCommitMessage(context.Background(), CommitInput{Diff: "+change"})
			if err != nil || got != "fix: done" || calls.Load() != 1 {
				t.Fatalf("result=%q calls=%d err=%v", got, calls.Load(), err)
			}
		})
	}
}

func TestGenerationPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &VertexAIClient{generator: generatorFunc(func(ctx context.Context, model string, input []*genai.Content, opts *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		return nil, ctx.Err()
	})}
	if _, err := client.GenerateCommitMessage(ctx, CommitInput{Diff: "diff", Language: "english"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}

func TestReviseCommitMessageIncludesCurrentMessageAndInstructions(t *testing.T) {
	var prompt string
	client := &VertexAIClient{generator: generatorFunc(func(ctx context.Context, model string, contents []*genai.Content, opts *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		prompt = contents[0].Parts[0].Text
		return response(&genai.Part{Text: "fix: revised"}), nil
	})}
	got, err := client.ReviseCommitMessage(context.Background(), CommitInput{Diff: "+change"}, "fix: original", "add a body")
	if err != nil || got != "fix: revised" || !strings.Contains(prompt, "+change") || !strings.Contains(prompt, "<current_message>\nfix: original") || !strings.Contains(prompt, "add a body") {
		t.Fatalf("got=%q err=%v prompt=%s", got, err, prompt)
	}
	for _, args := range [][2]string{{"", "add a body"}, {"fix: original", " "}} {
		if _, err := client.ReviseCommitMessage(context.Background(), CommitInput{}, args[0], args[1]); err == nil {
			t.Fatalf("invalid revision accepted: %q", args)
		}
	}
}

func TestCommitThinkingLevelFallsBackWhenUnsupported(t *testing.T) {
	var levels []genai.ThinkingLevel
	var prompt string
	client := &VertexAIClient{commitThinking: "minimal", generator: generatorFunc(func(ctx context.Context, model string, contents []*genai.Content, opts *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		prompt = contents[0].Parts[0].Text
		if opts.ThinkingConfig == nil {
			levels = append(levels, "")
			return response(&genai.Part{Text: "fix: done"}), nil
		}
		levels = append(levels, opts.ThinkingConfig.ThinkingLevel)
		return nil, genai.APIError{Code: 400, Message: "Thinking level is unsupported: THINKING_LEVEL_MINIMAL"}
	})}
	got, err := client.GenerateCommitMessage(context.Background(), CommitInput{Diff: "+change", DiffStat: "a.go | 1 +", Branch: "topic", RecentCommits: "feat(ui): add view", Language: "japanese"})
	if err != nil || got != "fix: done" || len(levels) != 3 || levels[0] != genai.ThinkingLevelMinimal || levels[1] != genai.ThinkingLevelLow || levels[2] != "" {
		t.Fatalf("got=%q err=%v levels=%v", got, err, levels)
	}
	for _, want := range []string{"+change", "a.go | 1 +", "Branch: topic", "feat(ui): add view", "in japanese", "blank line then a body"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, prompt)
		}
	}

	calls := 0
	client.generator = generatorFunc(func(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		calls++
		return nil, genai.APIError{Code: 400, Message: "invalid argument"}
	})
	if _, err := client.GenerateCommitMessage(context.Background(), CommitInput{Diff: "+change"}); err == nil || calls != 1 {
		t.Fatalf("unrelated errors must not be retried: calls=%d err=%v", calls, err)
	}
	client.commitThinking = "default"
	client.generator = generatorFunc(func(ctx context.Context, model string, contents []*genai.Content, opts *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		if opts.ThinkingConfig != nil {
			t.Fatal("default thinking must not override the model")
		}
		return response(&genai.Part{Text: "fix: done"}), nil
	})
	if _, err := client.GenerateCommitMessage(context.Background(), CommitInput{Diff: "+change"}); err != nil {
		t.Fatal(err)
	}
}

func TestGeminiGenerationSettings(t *testing.T) {
	for _, tc := range []struct {
		model       string
		thinking    string
		wantLevel   genai.ThinkingLevel
		wantDefault bool
	}{
		{"gemini-3.8-flash", "minimal", genai.ThinkingLevelLow, true},
		{"publishers/google/models/gemini-3.8-flash", "minimal", genai.ThinkingLevelLow, true},
		{"gemini-3.7-flash", "minimal", genai.ThinkingLevelLow, true},
		{"gemini-3.1-pro-preview", "minimal", genai.ThinkingLevelLow, true},
		{"gemini-3-pro-preview", "minimal", genai.ThinkingLevelLow, true},
		{"gemini-3.6-flash", "minimal", genai.ThinkingLevelMinimal, true},
		{"gemini-3-flash-preview", "minimal", genai.ThinkingLevelMinimal, true},
		{"gemini-3.8-flash", "high", genai.ThinkingLevelHigh, true},
		{"gemini-3.8-flash", "default", "", true},
		{"gemini-4-argon", "minimal", genai.ThinkingLevelLow, true},
		{"gemini-4-argon", "high", genai.ThinkingLevelHigh, true},
		{"gemini-4-argon", "default", "", true},
		{"gemini-2.5-flash", "default", "", false},
		{"custom-model", "minimal", genai.ThinkingLevelMinimal, false},
	} {
		t.Run(tc.model+"/"+tc.thinking, func(t *testing.T) {
			calls := 0
			client := &VertexAIClient{model: tc.model, commitThinking: tc.thinking, generator: generatorFunc(func(ctx context.Context, model string, contents []*genai.Content, opts *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
				calls++
				if model != tc.model {
					t.Fatalf("model changed: %s", model)
				}
				if (opts.Temperature == nil) != tc.wantDefault {
					t.Fatalf("default temperature=%v, want %v", opts.Temperature == nil, tc.wantDefault)
				}
				if !tc.wantDefault && *opts.Temperature != 0.3 {
					t.Fatalf("legacy temperature changed: %v", *opts.Temperature)
				}
				level := genai.ThinkingLevel("")
				if opts.ThinkingConfig != nil {
					level = opts.ThinkingConfig.ThinkingLevel
				}
				if level != tc.wantLevel {
					t.Fatalf("thinking=%s, want %s", level, tc.wantLevel)
				}
				return response(&genai.Part{Text: "fix: correct typo"}), nil
			})}
			input := CommitInput{Diff: "+change"}
			if _, err := client.GenerateCommitMessage(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			if _, err := client.ReviseCommitMessage(context.Background(), input, "fix: typo", "clarify"); err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatalf("unexpected retries: %d calls", calls)
			}
		})
	}
}

func TestCommitTimeoutIncludesFallbacksAndRevisions(t *testing.T) {
	for _, revision := range []bool{false, true} {
		calls := 0
		var deadline time.Time
		client := &VertexAIClient{commitThinking: "minimal", commitTimeout: 20 * time.Millisecond, generator: generatorFunc(func(ctx context.Context, model string, contents []*genai.Content, opts *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
			calls++
			current, ok := ctx.Deadline()
			if !ok {
				t.Fatal("generation has no deadline")
			}
			if calls == 1 {
				deadline = current
				return nil, genai.APIError{Code: 400, Message: "Thinking level is unsupported"}
			}
			if current != deadline {
				t.Fatal("fallback reset the timeout")
			}
			<-ctx.Done()
			return nil, ctx.Err()
		})}
		var err error
		if revision {
			_, err = client.ReviseCommitMessage(context.Background(), CommitInput{}, "fix: typo", "clarify")
		} else {
			_, err = client.GenerateCommitMessage(context.Background(), CommitInput{})
		}
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "commit.timeout") || calls != 2 {
			t.Fatalf("revision=%v calls=%d err=%v", revision, calls, err)
		}
	}
}

func TestCommitTimeoutPreservesEarlierParentDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	deadline, _ := ctx.Deadline()
	client := &VertexAIClient{commitTimeout: time.Minute, generator: generatorFunc(func(ctx context.Context, model string, contents []*genai.Content, opts *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		if got, _ := ctx.Deadline(); got != deadline {
			t.Fatal("parent deadline was extended")
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})}
	_, err := client.GenerateCommitMessage(ctx, CommitInput{})
	if !errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "commit.timeout") {
		t.Fatalf("lost parent deadline: %v", err)
	}
}

func TestStalledCommitRequestRetriesWithSameModelAndPrompt(t *testing.T) {
	for _, revision := range []bool{false, true} {
		calls := 0
		var firstContext context.Context
		var firstPrompt string
		client := &VertexAIClient{model: "gemini-3.8-flash", commitThinking: "minimal", generator: generatorFunc(func(ctx context.Context, model string, contents []*genai.Content, opts *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
			calls++
			if model != "gemini-3.8-flash" || opts.ThinkingConfig.ThinkingLevel != genai.ThinkingLevelLow {
				t.Fatal("retry changed the model or thinking level")
			}
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("request has no deadline")
			}
			if calls == 1 {
				firstContext, firstPrompt = ctx, contents[0].Parts[0].Text
				if time.Until(deadline) > config.DefaultCommitTimeout {
					t.Fatal("first attempt extended the total timeout")
				}
				return nil, fmt.Errorf("request timed out: %w", context.DeadlineExceeded)
			}
			if calls != 2 || firstContext.Err() == nil || contents[0].Parts[0].Text != firstPrompt {
				t.Fatal("retry did not cancel the stalled request or preserve the prompt")
			}
			if time.Until(deadline) > config.DefaultCommitTimeout {
				t.Fatal("retry extended the total timeout")
			}
			return response(&genai.Part{Text: "fix: recovered"}), nil
		})}
		var got string
		var err error
		if revision {
			got, err = client.ReviseCommitMessage(context.Background(), CommitInput{}, "fix: original", "clarify")
		} else {
			got, err = client.GenerateCommitMessage(context.Background(), CommitInput{})
		}
		if err != nil || got != "fix: recovered" || calls != 2 {
			t.Fatalf("revision=%v result=%q calls=%d err=%v", revision, got, calls, err)
		}
	}
}

func TestStalledCommitRequestRetriesOnlyOnce(t *testing.T) {
	for _, tc := range []struct {
		failure error
		calls   int
	}{
		{context.DeadlineExceeded, 2},
		{genai.APIError{Code: 504, Message: "deadline exceeded"}, 2},
		{context.Canceled, 1},
		{genai.APIError{Code: 503, Message: "unavailable"}, 1},
	} {
		calls := 0
		client := &VertexAIClient{generator: generatorFunc(func(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
			calls++
			return nil, tc.failure
		})}
		_, err := client.GenerateCommitMessage(context.Background(), CommitInput{})
		matches := errors.Is(err, tc.failure)
		if expected, ok := tc.failure.(genai.APIError); ok {
			var actual genai.APIError
			matches = errors.As(err, &actual) && actual.Code == expected.Code && actual.Message == expected.Message
		}
		if !matches || calls != tc.calls {
			t.Fatalf("failure=%v calls=%d err=%v", tc.failure, calls, err)
		}
	}
}

func TestPRGenerationAndRevisionIncludeSessionContextAsReference(t *testing.T) {
	var prompts []string
	client := &VertexAIClient{generator: generatorFunc(func(ctx context.Context, model string, contents []*genai.Content, opts *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		prompts = append(prompts, contents[0].Parts[0].Text)
		return response(&genai.Part{Text: `{"title":"title","body":"body"}`}), nil
	})}
	input := PullRequestInput{Diff: "final diff", SessionContext: "user: Preserve compatibility"}
	if _, err := client.GeneratePullRequestContent(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RevisePullRequestContent(context.Background(), input, &PullRequestContent{Title: "old", Body: "old"}, "shorten"); err != nil {
		t.Fatal(err)
	}
	for _, prompt := range prompts {
		for _, text := range []string{"AGENT_SESSION_CONTEXT:\nuser: Preserve compatibility", "final diff", "untrusted reference material", "authoritative for implemented changes", "does not include tool execution evidence"} {
			if !strings.Contains(prompt, text) {
				t.Errorf("prompt missing %q", text)
			}
		}
	}
}
