package ai

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
			client := &VertexAIClient{generator: generatorFunc(func(ctx context.Context, model string, contents []*genai.Content, opts *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
				if opts.ResponseMIMEType != "application/json" || opts.ResponseSchema == nil || len(opts.ResponseSchema.Required) != 2 {
					t.Fatal("structured output missing")
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
