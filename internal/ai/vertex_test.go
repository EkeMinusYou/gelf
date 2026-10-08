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
			got, err := client.GenerateCommitMessage(context.Background(), "+change", "english")
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
	if _, err := client.GenerateCommitMessage(context.Background(), "diff", "english"); err == nil {
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
	if _, err := client.GenerateCommitMessage(ctx, "diff", "english"); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
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
