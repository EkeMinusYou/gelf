package ai

import (
	"context"
	"encoding/json"
	"errors"
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

type CommitInput struct {
	Diff          string
	DiffStat      string
	Branch        string
	RecentCommits string
	Language      string
}

type PullRequestContent struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type Client interface {
	GenerateCommitMessage(context.Context, CommitInput) (string, error)
	GeneratePullRequestContent(context.Context, PullRequestInput) (*PullRequestContent, error)
	RevisePullRequestContent(context.Context, PullRequestInput, *PullRequestContent, string) (*PullRequestContent, error)
}

type contentGenerator interface {
	GenerateContent(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)
}

type VertexAIClient struct {
	generator      contentGenerator
	model          string
	commitThinking string
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
	return &VertexAIClient{generator: client.Models, model: model, commitThinking: cfg.CommitThinking}, nil
}

func (v *VertexAIClient) GenerateCommitMessage(ctx context.Context, input CommitInput) (string, error) {
	recentCommits := input.RecentCommits
	if recentCommits == "" {
		recentCommits = "NONE"
	}
	branch := input.Branch
	if branch == "" {
		branch = "UNKNOWN"
	}
	prompt := fmt.Sprintf(`<task>Write a commit message for the staged changes below.</task>

<format>
- Subject line: <type>[optional scope]: <description>
- Conventional Commits types: feat, fix, docs, style, refactor, test, chore, perf, ci, build, revert
- Subject under 72 characters, imperative mood, lowercase description, no trailing period
- For material changes, add a blank line then a body explaining what changed and why
- Wrap body lines at 72 characters; use "- " bullet points for multiple distinct changes
- Omit the body for trivial changes where the subject says everything
- Output only the commit message, no quotes or code blocks
</format>

<style>
- Write the subject description and body in %s
- Match the scope naming and wording of the recent commits when they fit
- The subject names the most significant change; the body covers the rest
- Describe the change itself; do not speculate beyond what the diff shows
</style>

<diffstat>
%s
</diffstat>

<diff>
%s
</diff>

<context>
Branch: %s
<recent_commits>
%s
</recent_commits>
</context>`, input.Language, input.DiffStat, input.Diff, branch, recentCommits)

	text, err := v.generate(ctx, prompt, 0.3, false, v.commitThinking)
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

// generate leaves thinking to the model when thinking is "" or "default".
func (v *VertexAIClient) generate(ctx context.Context, prompt string, temperature float32, structured bool, thinking string) (string, error) {
	opts := &genai.GenerateContentConfig{Temperature: genai.Ptr(temperature)}
	if structured {
		opts.ResponseMIMEType = "application/json"
		opts.ResponseSchema = &genai.Schema{Type: genai.TypeObject, Required: []string{"title", "body"}, Properties: map[string]*genai.Schema{
			"title": {Type: genai.TypeString}, "body": {Type: genai.TypeString},
		}}
	}
	contents := []*genai.Content{genai.NewContentFromText(prompt, genai.RoleUser)}
	var resp *genai.GenerateContentResponse
	var err error
	for _, level := range thinkingFallbacks(thinking) {
		opts.ThinkingConfig = nil
		if level != "" {
			opts.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: level}
		}
		resp, err = v.generator.GenerateContent(ctx, v.model, contents, opts)
		if !unsupportedThinking(err) {
			break
		}
	}
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
	text, err := v.generate(ctx, prompt, 0.2, true, "default")
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

// thinkingFallbacks lists the levels to try in order; "" means the model default.
// Models support different levels (e.g. some reject MINIMAL but accept LOW), so
// an unsupported level steps toward the default instead of failing.
func thinkingFallbacks(thinking string) []genai.ThinkingLevel {
	switch thinking {
	case "", "default":
		return []genai.ThinkingLevel{""}
	case "minimal":
		return []genai.ThinkingLevel{genai.ThinkingLevelMinimal, genai.ThinkingLevelLow, ""}
	default:
		return []genai.ThinkingLevel{genai.ThinkingLevel(strings.ToUpper(thinking)), ""}
	}
}

func unsupportedThinking(err error) bool {
	var apiErr genai.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 400 {
		return false
	}
	// Vertex AI words this differently per model ("thinking_level ..." or "Thinking level ...").
	message := strings.ToLower(apiErr.Message)
	return strings.Contains(message, "thinking_level") || strings.Contains(message, "thinking level")
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
