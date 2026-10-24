package ai

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"google.golang.org/genai"
)

type streamingContentGenerator interface {
	GenerateContentStream(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error]
}

// collectStream retains only visible text and requires a terminal finish reason.
// Partial output is useful for progress but must never become an approvable commit.
func collectStream(ctx context.Context, stream iter.Seq2[*genai.GenerateContentResponse, error], onProgress func(string)) (*genai.GenerateContentResponse, error) {
	result := &genai.GenerateContentResponse{}
	var candidate *genai.Candidate
	var text strings.Builder
	contentSeen := false
	for chunk, err := range stream {
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if chunk == nil {
			continue
		}
		if chunk.UsageMetadata != nil {
			result.UsageMetadata = chunk.UsageMetadata
		}
		if len(chunk.Candidates) == 0 || chunk.Candidates[0] == nil {
			continue
		}
		part := chunk.Candidates[0]
		if candidate == nil {
			candidate = &genai.Candidate{}
			result.Candidates = []*genai.Candidate{candidate}
		}
		if part.FinishReason != "" {
			candidate.FinishReason, candidate.FinishMessage = part.FinishReason, part.FinishMessage
		}
		if part.Content == nil {
			continue
		}
		contentSeen = true
		active := false
		for _, p := range part.Content.Parts {
			if p == nil || p.Text == "" {
				continue
			}
			active = true
			if !p.Thought {
				text.WriteString(p.Text)
			}
		}
		if active && onProgress != nil {
			onProgress(text.String())
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if candidate != nil && candidate.FinishReason == "" {
		return nil, fmt.Errorf("incomplete AI response: stream ended without a finish reason")
	}
	if candidate != nil && contentSeen {
		candidate.Content = &genai.Content{Parts: []*genai.Part{{Text: text.String()}}}
	}
	return result, nil
}
