package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/EkeMinusYou/gelf/internal/config"

	"google.golang.org/genai"
)

type PullRequestInput struct {
	BaseBranch string
	HeadBranch string
	CommitLog  string
	DiffStat   string
	Diff       string
	Template   string
	Language   string
}

type PullRequestContent struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type VertexAIClient struct {
	client     *genai.Client
	flashModel string
	proModel   string
}

func NewVertexAIClient(ctx context.Context, cfg *config.Config) (*VertexAIClient, error) {
	// Check for GELF_CREDENTIALS first, then fall back to GOOGLE_APPLICATION_CREDENTIALS
	credentialsPath := os.Getenv("GELF_CREDENTIALS")
	if credentialsPath == "" {
		credentialsPath = os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	}

	// If we have a credentials path, set it as GOOGLE_APPLICATION_CREDENTIALS
	// since genai.NewClient uses this environment variable internally
	if credentialsPath != "" {
		originalCreds := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
		os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credentialsPath)

		// Restore original credentials after client creation
		defer func() {
			if originalCreds != "" {
				os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", originalCreds)
			} else {
				os.Unsetenv("GOOGLE_APPLICATION_CREDENTIALS")
			}
		}()
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:  cfg.ProjectID,
		Location: cfg.Location,
		Backend:  genai.BackendVertexAI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Vertex AI client: %w", err)
	}

	return &VertexAIClient{
		client:     client,
		flashModel: cfg.FlashModel,
		proModel:   cfg.ProModel,
	}, nil
}

func (v *VertexAIClient) GenerateCommitMessage(ctx context.Context, diff string, language string) (string, error) {
	prompt := fmt.Sprintf(`Analyze the following git diff and generate a precise commit message following the Conventional Commits specification.

DIFF ANALYSIS GUIDE:
1. Look at file paths to understand what parts of the codebase are affected
2. Examine +/- lines to understand what was added, removed, or modified
3. Pay attention to function names, variable names, and code structure changes
4. Consider the context lines (prefixed with space) to understand the surrounding code
5. Identify the primary purpose: new feature, bug fix, refactoring, etc.

COMMIT MESSAGE REQUIREMENTS:
1. Use %s language
2. Follow format: <type>[optional scope]: <description>
3. Valid types: feat, fix, docs, style, refactor, test, chore, perf, ci, build, revert
4. Keep under 72 characters total
5. Use imperative mood ("add" not "added")
6. Start description with lowercase letter
7. No period at the end
8. If multiple changes, focus on the most significant one
9. Use scope when it helps clarify the area of change (e.g., auth, api, ui)

EXAMPLES:
- feat(auth): add JWT token validation
- fix(api): resolve null pointer in user service
- refactor(db): simplify connection pooling logic
- test(payment): add unit tests for stripe integration
- chore(deps): update react to version 18.2.0

Git diff:
%s

Respond with only the commit message, no additional text or formatting.`, language, diff)

	resp, err := v.client.Models.GenerateContent(ctx, v.flashModel,
		[]*genai.Content{
			genai.NewContentFromText(prompt, genai.RoleUser),
		},
		&genai.GenerateContentConfig{
			Temperature: genai.Ptr(float32(0.3)),
		})
	if err != nil {
		return "", fmt.Errorf("failed to generate commit message: %w", err)
	}

	if len(resp.Candidates) == 0 {
		return "", fmt.Errorf("no candidates in response")
	}

	if len(resp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no content parts in response")
	}

	part := resp.Candidates[0].Content.Parts[0]
	if part.Text == "" {
		return "", fmt.Errorf("empty text in response part")
	}

	return part.Text, nil
}

func (v *VertexAIClient) GeneratePullRequestContent(ctx context.Context, input PullRequestInput) (*PullRequestContent, error) {
	template := input.Template
	if strings.TrimSpace(template) == "" {
		template = "NONE"
	}

	prompt := fmt.Sprintf(`You are an expert software engineer writing a GitHub pull request title and description.

OUTPUT FORMAT:
- Respond with ONLY a valid JSON object.
- No markdown fences or extra text.
- JSON schema: {"title":"...", "body":"..."}

LANGUAGE:
- Write in %s.

TITLE REQUIREMENTS:
- Concise and specific.
- Use imperative mood.
- Keep it under 72 characters if possible.

BODY REQUIREMENTS:
- If PR_TEMPLATE is not "NONE", use it as the base text.
- Preserve headings, lists, checkboxes, and HTML comments from the template.
- Fill each section with relevant information derived from the commits and diff.
- Replace placeholder text with concrete details.
- If testing information is unknown, explicitly say tests were not run.
- If PR_TEMPLATE is "NONE", use sections: Summary, Changes, Testing.

BASE BRANCH: %s
HEAD BRANCH: %s

COMMITS (oldest to newest):
%s

DIFF STAT:
%s

DIFF:
%s

PR_TEMPLATE:
%s
`, input.Language, input.BaseBranch, input.HeadBranch, input.CommitLog, input.DiffStat, input.Diff, template)

	resp, err := v.client.Models.GenerateContent(ctx, v.flashModel,
		[]*genai.Content{
			genai.NewContentFromText(prompt, genai.RoleUser),
		},
		&genai.GenerateContentConfig{
			Temperature: genai.Ptr(float32(0.2)),
		})
	if err != nil {
		return nil, fmt.Errorf("failed to generate pull request content: %w", err)
	}

	if len(resp.Candidates) == 0 {
		return nil, fmt.Errorf("no candidates in response")
	}

	if len(resp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("no content parts in response")
	}

	part := resp.Candidates[0].Content.Parts[0]
	if part.Text == "" {
		return nil, fmt.Errorf("empty text in response part")
	}

	text := strings.TrimSpace(part.Text)
	if strings.HasPrefix(text, "```json") {
		text = strings.TrimPrefix(text, "```json")
		text = strings.TrimSuffix(text, "```")
		text = strings.TrimSpace(text)
	}

	var result PullRequestContent
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	result.Title = strings.TrimSpace(result.Title)
	result.Body = strings.TrimSpace(result.Body)
	if result.Title == "" {
		return nil, fmt.Errorf("generated PR title is empty")
	}
	if result.Body == "" {
		return nil, fmt.Errorf("generated PR body is empty")
	}

	return &result, nil
}

func (v *VertexAIClient) Close() error {
	return nil
}
