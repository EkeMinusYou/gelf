package ai

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/genai"
)

type streamingGeneratorFunc func(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error]

func (f streamingGeneratorFunc) GenerateContent(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	return nil, fmt.Errorf("unexpected non-streaming generation")
}

func (f streamingGeneratorFunc) GenerateContentStream(ctx context.Context, model string, contents []*genai.Content, opts *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error] {
	return f(ctx, model, contents, opts)
}

func TestStreamingCommitReportsVisibleTextAndRetainsFinishReason(t *testing.T) {
	var progress []string
	client := &VertexAIClient{generator: streamingGeneratorFunc(func(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error] {
		return func(yield func(*genai.GenerateContentResponse, error) bool) {
			chunks := []*genai.GenerateContentResponse{
				nil,
				{Candidates: []*genai.Candidate{nil}},
				{Candidates: []*genai.Candidate{{Content: &genai.Content{Parts: []*genai.Part{nil, {Thought: true, Text: "private reasoning"}}}}}},
				{Candidates: []*genai.Candidate{{Content: &genai.Content{Parts: []*genai.Part{{Text: "fix: "}}}}}},
				{Candidates: []*genai.Candidate{{Content: &genai.Content{Parts: []*genai.Part{{Text: "correct behavior"}}}}}},
				{Candidates: []*genai.Candidate{{FinishReason: genai.FinishReasonStop}}},
				{UsageMetadata: &genai.GenerateContentResponseUsageMetadata{CandidatesTokenCount: 4}},
			}
			for _, chunk := range chunks {
				if !yield(chunk, nil) {
					return
				}
			}
		}
	})}
	got, err := client.GenerateCommitMessage(context.Background(), CommitInput{OnProgress: func(text string) { progress = append(progress, text) }})
	if err != nil || got != "fix: correct behavior" || !reflect.DeepEqual(progress, []string{"", "fix: ", "fix: correct behavior"}) {
		t.Fatalf("result=%q progress=%q err=%v", got, progress, err)
	}
}

func TestStreamingCommitRejectsPartialOrFailedResponses(t *testing.T) {
	failure := errors.New("stream disconnected")
	for _, tc := range []struct {
		name    string
		finish  genai.FinishReason
		failure error
		cancel  bool
	}{
		{name: "premature EOF"},
		{name: "transport error", failure: failure},
		{name: "token limit", finish: genai.FinishReasonMaxTokens},
		{name: "safety", finish: genai.FinishReasonSafety},
		{name: "cancellation", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := &VertexAIClient{generator: streamingGeneratorFunc(func(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error] {
				return func(yield func(*genai.GenerateContentResponse, error) bool) {
					if !yield(&genai.GenerateContentResponse{Candidates: []*genai.Candidate{{Content: &genai.Content{Parts: []*genai.Part{{Text: "partial message"}}}}}}, nil) {
						return
					}
					if tc.cancel {
						cancel()
						return
					}
					if tc.failure != nil {
						yield(nil, tc.failure)
						return
					}
					if tc.finish != "" {
						yield(&genai.GenerateContentResponse{Candidates: []*genai.Candidate{{FinishReason: tc.finish}}}, nil)
					}
				}
			})}
			var progress string
			got, err := client.GenerateCommitMessage(ctx, CommitInput{OnProgress: func(text string) { progress = text }})
			if err == nil || got != "" || progress != "partial message" {
				t.Fatalf("partial response accepted: result=%q progress=%q err=%v", got, progress, err)
			}
			if tc.failure != nil && !errors.Is(err, tc.failure) {
				t.Fatalf("lost transport error: %v", err)
			}
			if tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
		})
	}
}

func TestFirstTokenTimeoutDoesNotInterruptActiveGeneration(t *testing.T) {
	for _, active := range []bool{false, true} {
		client := &VertexAIClient{generator: streamingGeneratorFunc(func(ctx context.Context, _ string, _ []*genai.Content, _ *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error] {
			return func(yield func(*genai.GenerateContentResponse, error) bool) {
				if !active {
					<-ctx.Done()
					yield(nil, ctx.Err())
					return
				}
				if !yield(&genai.GenerateContentResponse{Candidates: []*genai.Candidate{{Content: &genai.Content{Parts: []*genai.Part{{Thought: true, Text: "reasoning"}}}}}}, nil) {
					return
				}
				select {
				case <-ctx.Done():
					yield(nil, ctx.Err())
				case <-time.After(60 * time.Millisecond):
					yield(response(&genai.Part{Text: "fix: complete"}), nil)
				}
			}
		})}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		text, err, started := client.generateCommitAttempt(ctx, "prompt", nil, 20*time.Millisecond)
		cancel()
		if active {
			if err != nil || strings.TrimSpace(text) != "fix: complete" || !started {
				t.Fatalf("active generation interrupted: text=%q started=%v err=%v", text, started, err)
			}
		} else if !errors.Is(err, context.DeadlineExceeded) || started {
			t.Fatalf("stalled generation was not cancelled: started=%v err=%v", started, err)
		}
	}
}
