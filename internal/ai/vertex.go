package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"cloud.google.com/go/auth/credentials"
	"github.com/EkeMinusYou/gelf/internal/config"
	"google.golang.org/genai"
)

type PullRequestInput struct {
	BaseBranch    string
	HeadBranch    string
	CommitLog     string
	DiffStat      string
	Diff          string
	Template      string
	Language      string
	TitleLanguage string
	BodyLanguage  string
}

type PullRequestContent struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type Client interface {
	GenerateCommitMessage(context.Context, string, string) (string, error)
	GeneratePullRequestContent(context.Context, PullRequestInput) (*PullRequestContent, error)
	RevisePullRequestContent(context.Context, PullRequestInput, *PullRequestContent, string) (*PullRequestContent, error)
}

type contentGenerator interface {
	GenerateContent(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)
}

type VertexAIClient struct {
	generator contentGenerator
	model     string
}

func NewVertexAIClient(ctx context.Context, cfg *config.Config, model string) (*VertexAIClient, error) {
	cc := &genai.ClientConfig{Project: cfg.ProjectID, Location: cfg.Location, Backend: genai.BackendVertexAI}
	if path := os.Getenv("GELF_CREDENTIALS"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read GELF_CREDENTIALS: %w", err)
		}
		var metadata struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &metadata); err != nil {
			return nil, fmt.Errorf("invalid GELF_CREDENTIALS JSON: %w", err)
		}
		creds, err := credentials.NewCredentialsFromJSON(credentials.CredType(metadata.Type), data, &credentials.DetectOptions{Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"}})
		if err != nil {
			return nil, fmt.Errorf("failed to load GELF_CREDENTIALS: %w", err)
		}
		cc.Credentials = creds
	}
	client, err := genai.NewClient(ctx, cc)
	if err != nil {
		return nil, fmt.Errorf("failed to create Vertex AI client: %w", err)
	}
	return &VertexAIClient{generator: client.Models, model: model}, nil
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

	text, err := v.generate(ctx, prompt, 0.3, false)
	if err != nil {
		return "", fmt.Errorf("failed to generate commit message: %w", err)
	}
	return text, nil
}

func (v *VertexAIClient) GeneratePullRequestContent(ctx context.Context, input PullRequestInput) (*PullRequestContent, error) {
	titleLanguage, bodyLanguage, template := input.promptSettings()

	prompt := fmt.Sprintf(`You are an expert software engineer writing a GitHub pull request title and description.

OUTPUT FORMAT:
- Respond with ONLY a valid JSON object.
- No markdown fences or extra text.
- JSON schema: {"title":"...", "body":"..."}

LANGUAGE:
- Write the title in %s.
- Write the body in %s.

TITLE REQUIREMENTS:
- Concise and specific.
- Use imperative mood.
- Keep it under 72 characters if possible.

BODY REQUIREMENTS:
- Describe the purpose and key changes concisely, using only information supported by the commits and diff.
- If PR_TEMPLATE is not "NONE", use it as the base text, preserve its headings, lists, checkboxes, and HTML comments, and replace placeholders with relevant details.
- If PR_TEMPLATE is "NONE", use headings or bullet points only when they improve clarity; no fixed sections are required.

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
`, titleLanguage, bodyLanguage, input.BaseBranch, input.HeadBranch, input.CommitLog, input.DiffStat, input.Diff, template)

	return v.generatePR(ctx, prompt)
}

func (v *VertexAIClient) RevisePullRequestContent(ctx context.Context, input PullRequestInput, previous *PullRequestContent, instructions string) (*PullRequestContent, error) {
	if previous == nil {
		return nil, fmt.Errorf("previous content is required for revision")
	}
	if strings.TrimSpace(instructions) == "" {
		return nil, fmt.Errorf("revision instructions are empty")
	}

	titleLanguage, bodyLanguage, template := input.promptSettings()

	prompt := fmt.Sprintf(`You are an expert software engineer revising a GitHub pull request title and description based on user feedback.

OUTPUT FORMAT:
- Respond with ONLY a valid JSON object.
- No markdown fences or extra text.
- JSON schema: {"title":"...", "body":"..."}

LANGUAGE:
- Write the title in %s.
- Write the body in %s.

REVISION REQUIREMENTS:
- Apply the user's revision instructions faithfully.
- Preserve information and structure that is not affected by the instructions.
- Keep the title concise (under 72 characters if possible) and in imperative mood.
- If PR_TEMPLATE is not "NONE", continue to respect its sections, headings, lists, checkboxes, and HTML comments.
- If PR_TEMPLATE is "NONE", describe the purpose and key changes concisely, using headings or bullet points only when they improve clarity; no fixed sections are required.
- Do not invent information not supported by the commits and diff.

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

CURRENT_TITLE:
%s

CURRENT_BODY:
%s

USER_REVISION_INSTRUCTIONS:
%s
`, titleLanguage, bodyLanguage, input.BaseBranch, input.HeadBranch, input.CommitLog, input.DiffStat, input.Diff, template, previous.Title, previous.Body, instructions)

	return v.generatePR(ctx, prompt)
}

func (v *VertexAIClient) generate(ctx context.Context, prompt string, temperature float32, structured bool) (string, error) {
	opts := &genai.GenerateContentConfig{Temperature: genai.Ptr(temperature)}
	if structured {
		opts.ResponseMIMEType = "application/json"
		opts.ResponseSchema = &genai.Schema{Type: genai.TypeObject, Required: []string{"title", "body"}, Properties: map[string]*genai.Schema{
			"title": {Type: genai.TypeString}, "body": {Type: genai.TypeString},
		}}
	}
	resp, err := v.generator.GenerateContent(ctx, v.model, []*genai.Content{genai.NewContentFromText(prompt, genai.RoleUser)}, opts)
	if err != nil {
		return "", err
	}
	if resp == nil || len(resp.Candidates) == 0 || resp.Candidates[0] == nil {
		return "", fmt.Errorf("no candidates in AI response")
	}
	candidate := resp.Candidates[0]
	if candidate.FinishReason != "" && candidate.FinishReason != genai.FinishReasonStop {
		return "", fmt.Errorf("AI response stopped with reason %s: %s", candidate.FinishReason, candidate.FinishMessage)
	}
	if candidate.Content == nil {
		return "", fmt.Errorf("no content in AI response")
	}
	var text strings.Builder
	for _, part := range candidate.Content.Parts {
		if part != nil && !part.Thought {
			text.WriteString(part.Text)
		}
	}
	result := strings.TrimSpace(text.String())
	if result == "" {
		return "", fmt.Errorf("empty text in AI response")
	}
	return result, nil
}

func (v *VertexAIClient) generatePR(ctx context.Context, prompt string) (*PullRequestContent, error) {
	text, err := v.generate(ctx, prompt, 0.2, true)
	if err != nil {
		return nil, fmt.Errorf("failed to generate pull request content: %w", err)
	}
	if strings.HasPrefix(text, "```") {
		if newline := strings.IndexByte(text, '\n'); newline >= 0 && strings.HasSuffix(text, "```") {
			text = strings.TrimSpace(text[newline+1 : len(text)-3])
		}
	}
	var result PullRequestContent
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}
	result.Title, result.Body = strings.TrimSpace(result.Title), strings.TrimSpace(result.Body)
	if result.Title == "" || result.Body == "" {
		return nil, fmt.Errorf("generated PR title and body must not be empty")
	}
	if strings.ContainsAny(result.Title, "\r\n") {
		return nil, fmt.Errorf("generated PR title must be a single line")
	}
	return &result, nil
}

func (input PullRequestInput) promptSettings() (titleLanguage, bodyLanguage, template string) {
	titleLanguage, bodyLanguage, template = input.TitleLanguage, input.BodyLanguage, input.Template
	if titleLanguage == "" {
		titleLanguage = input.Language
	}
	if bodyLanguage == "" {
		bodyLanguage = input.Language
	}
	if strings.TrimSpace(template) == "" {
		template = "NONE"
	}
	return
}
