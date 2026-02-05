package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/EkeMinusYou/gelf/internal/config"
	"github.com/EkeMinusYou/gelf/internal/doc"

	"google.golang.org/genai"
)

// ReviewComment represents a single review comment
type ReviewComment struct {
	FileName string `json:"fileName"`
	LineNo   int    `json:"lineNo,omitempty"`
	Type     string `json:"type"` // must, want, nits, fyi, imo
	Message  string `json:"message"`
}

// FileReview represents review data for a single file
type FileReview struct {
	FileName string          `json:"fileName"`
	DiffText string          `json:"diffText"`
	Comments []ReviewComment `json:"comments"`
}

// StructuredReview represents the complete review data
type StructuredReview struct {
	Summary     string       `json:"summary"`
	FileReviews []FileReview `json:"fileReviews"`
}

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

// ReviewCodeStructured generates a structured review with file-specific comments
func (v *VertexAIClient) ReviewCodeStructured(ctx context.Context, diff string, language string) (*StructuredReview, error) {
	// First, parse the diff to extract file information
	files := v.parseDiffFiles(diff)

	var fileReviews []FileReview
	var allComments []ReviewComment

	for _, file := range files {
		// Generate review for each file individually
		fileReview, err := v.reviewSingleFile(ctx, file.fileName, file.diffText, language)
		if err != nil {
			// Continue with other files if one fails
			continue
		}

		fileReviews = append(fileReviews, *fileReview)
		allComments = append(allComments, fileReview.Comments...)
	}

	// Generate overall summary
	summary, err := v.generateReviewSummary(ctx, diff, allComments, language)
	if err != nil {
		summary = "Failed to generate summary"
	}

	return &StructuredReview{
		Summary:     summary,
		FileReviews: fileReviews,
	}, nil
}

// parseDiffFiles extracts file information from git diff
func (v *VertexAIClient) parseDiffFiles(diff string) []struct {
	fileName string
	diffText string
} {
	var files []struct {
		fileName string
		diffText string
	}

	lines := strings.Split(diff, "\n")
	var currentFile string
	var currentDiff []string

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git") {
			// Save previous file if exists
			if currentFile != "" && len(currentDiff) > 0 {
				files = append(files, struct {
					fileName string
					diffText string
				}{
					fileName: currentFile,
					diffText: strings.Join(currentDiff, "\n"),
				})
			}

			// Extract filename from "diff --git a/file b/file"
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				currentFile = strings.TrimPrefix(parts[3], "b/")
			}
			currentDiff = []string{line}
		} else if currentFile != "" {
			currentDiff = append(currentDiff, line)
		}
	}

	// Add the last file
	if currentFile != "" && len(currentDiff) > 0 {
		files = append(files, struct {
			fileName string
			diffText string
		}{
			fileName: currentFile,
			diffText: strings.Join(currentDiff, "\n"),
		})
	}

	return files
}

// reviewSingleFile generates review for a single file
func (v *VertexAIClient) reviewSingleFile(ctx context.Context, fileName, diffText, language string) (*FileReview, error) {
	prompt := fmt.Sprintf(`Analyze the following git diff for file "%s" and provide specific code review comments in %s language.

RESPONSE REQUIREMENTS:
1. Respond with ONLY a valid JSON object
2. No markdown formatting, no code blocks, no additional text
3. Use this exact structure:

{
  "comments": [
    {
      "fileName": "%s",
      "lineNo": 42,
      "type": "must",
      "message": "Fix potential null pointer dereference"
    }
  ]
}

COMMENT TYPES:
- "must": Critical issues that must be fixed
- "want": Important suggestions for improvement  
- "nits": Minor style/formatting issues
- "fyi": Informational notes
- "imo": Opinion-based suggestions

GUIDELINES:
- Focus on the most important issues only
- Be specific and actionable
- Include line numbers when possible (use approximate line numbers from diff context)
- Maximum 5 comments per file
- If no issues, return: {"comments": []}

File diff:
%s`, fileName, language, fileName, diffText)

	resp, err := v.client.Models.GenerateContent(ctx, v.flashModel,
		[]*genai.Content{
			genai.NewContentFromText(prompt, genai.RoleUser),
		},
		&genai.GenerateContentConfig{
			Temperature: genai.Ptr(float32(0.1)),
		})
	if err != nil {
		return nil, fmt.Errorf("failed to generate file review: %w", err)
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

	// Parse JSON response
	var result struct {
		Comments []ReviewComment `json:"comments"`
	}

	if err := json.Unmarshal([]byte(part.Text), &result); err != nil {
		// Fallback: try to extract JSON from response if it's wrapped in markdown
		text := strings.TrimSpace(part.Text)
		if strings.HasPrefix(text, "```json") {
			text = strings.TrimPrefix(text, "```json")
			text = strings.TrimSuffix(text, "```")
			text = strings.TrimSpace(text)
		}

		if err := json.Unmarshal([]byte(text), &result); err != nil {
			return nil, fmt.Errorf("failed to parse JSON response: %w", err)
		}
	}

	return &FileReview{
		FileName: fileName,
		DiffText: diffText,
		Comments: result.Comments,
	}, nil
}

// generateReviewSummary creates an overall summary of the review
func (v *VertexAIClient) generateReviewSummary(ctx context.Context, diff string, comments []ReviewComment, language string) (string, error) {
	if len(comments) == 0 {
		return "No significant issues found in the code changes.", nil
	}

	mustCount := 0
	wantCount := 0
	nitsCount := 0

	for _, comment := range comments {
		switch comment.Type {
		case "must":
			mustCount++
		case "want":
			wantCount++
		case "nits":
			nitsCount++
		}
	}

	prompt := fmt.Sprintf(`Based on the following code review findings, generate a brief summary (1-2 sentences) in %s language:

FINDINGS:
- Critical issues (must fix): %d
- Important suggestions (want): %d  
- Minor issues (nits): %d
- Total comments: %d

Provide a concise summary of the overall code quality and main areas of concern.`,
		language, mustCount, wantCount, nitsCount, len(comments))

	resp, err := v.client.Models.GenerateContent(ctx, v.flashModel,
		[]*genai.Content{
			genai.NewContentFromText(prompt, genai.RoleUser),
		},
		&genai.GenerateContentConfig{
			Temperature: genai.Ptr(float32(0.3)),
		})
	if err != nil {
		return "", fmt.Errorf("failed to generate summary: %w", err)
	}

	if len(resp.Candidates) == 0 {
		return "", fmt.Errorf("no candidates in response")
	}

	if len(resp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no content parts in response")
	}

	part := resp.Candidates[0].Content.Parts[0]
	return strings.TrimSpace(part.Text), nil
}

func (v *VertexAIClient) GenerateDocumentation(ctx context.Context, sourceInfo *doc.SourceInfo, template, language string) (string, error) {
	prompt, err := v.buildDocumentationPrompt(sourceInfo, template, language)
	if err != nil {
		return "", fmt.Errorf("failed to build prompt: %w", err)
	}

	resp, err := v.client.Models.GenerateContent(ctx, v.proModel,
		[]*genai.Content{
			genai.NewContentFromText(prompt, genai.RoleUser),
		},
		&genai.GenerateContentConfig{
			Temperature: genai.Ptr(float32(0.3)),
		})
	if err != nil {
		return "", fmt.Errorf("failed to generate documentation: %w", err)
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

func (v *VertexAIClient) buildDocumentationPrompt(sourceInfo *doc.SourceInfo, template, language string) (string, error) {
	var basePrompt string

	switch template {
	case "readme":
		basePrompt = v.buildReadmePrompt(sourceInfo, language)
	case "api":
		basePrompt = v.buildAPIPrompt(sourceInfo, language)
	case "changelog":
		basePrompt = v.buildChangelogPrompt(sourceInfo, language)
	case "architecture":
		basePrompt = v.buildArchitecturePrompt(sourceInfo, language)
	case "godoc":
		basePrompt = v.buildGodocPrompt(sourceInfo, language)
	default:
		return "", fmt.Errorf("unsupported template: %s", template)
	}

	return basePrompt, nil
}

func (v *VertexAIClient) buildReadmePrompt(sourceInfo *doc.SourceInfo, language string) string {
	sourceCode := v.formatSourceCode(sourceInfo)

	return fmt.Sprintf(`Generate a comprehensive README.md for this project in %s language.

PROJECT ANALYSIS:
Languages: %s
File count: %d
Project structure:
%s

SOURCE CODE ANALYSIS:
%s

REQUIREMENTS:
1. Write in %s language
2. Include project title and description
3. Add installation instructions
4. Provide usage examples
5. Include API documentation if applicable
6. Add contribution guidelines
7. Include license information
8. Use proper Markdown formatting
9. Be comprehensive but concise
10. Focus on what users need to know

Generate a complete README.md that follows best practices and provides all necessary information for users and contributors.`,
		language,
		strings.Join(sourceInfo.Languages, ", "),
		len(sourceInfo.Files),
		sourceInfo.Structure,
		sourceCode,
		language)
}

func (v *VertexAIClient) buildAPIPrompt(sourceInfo *doc.SourceInfo, language string) string {
	sourceCode := v.formatSourceCode(sourceInfo)

	return fmt.Sprintf(`Generate comprehensive API documentation for this project in %s language.

PROJECT ANALYSIS:
Languages: %s
File count: %d

SOURCE CODE ANALYSIS:
%s

REQUIREMENTS:
1. Write in %s language
2. Document all public functions, methods, and classes
3. Include parameter descriptions and types
4. Provide return value documentation
5. Add usage examples for each API
6. Include error handling information
7. Group related functions together
8. Use proper Markdown formatting
9. Include authentication requirements if applicable
10. Provide complete endpoint documentation if it's a web API

Generate detailed API documentation that developers can use to understand and integrate with this codebase.`,
		language,
		strings.Join(sourceInfo.Languages, ", "),
		len(sourceInfo.Files),
		sourceCode,
		language)
}

func (v *VertexAIClient) buildChangelogPrompt(sourceInfo *doc.SourceInfo, language string) string {
	return fmt.Sprintf(`Generate a CHANGELOG.md template for this project in %s language.

PROJECT ANALYSIS:
Languages: %s
File count: %d
Project structure:
%s

REQUIREMENTS:
1. Write in %s language
2. Follow Keep a Changelog format
3. Include sections for different version types
4. Add categories: Added, Changed, Deprecated, Removed, Fixed, Security
5. Provide examples of how to document changes
6. Include unreleased section
7. Use proper Markdown formatting
8. Include guidelines for maintaining the changelog

Generate a comprehensive CHANGELOG.md template that the team can use to track project changes.`,
		language,
		strings.Join(sourceInfo.Languages, ", "),
		len(sourceInfo.Files),
		sourceInfo.Structure,
		language)
}

func (v *VertexAIClient) buildArchitecturePrompt(sourceInfo *doc.SourceInfo, language string) string {
	sourceCode := v.formatSourceCode(sourceInfo)

	return fmt.Sprintf(`Generate comprehensive architecture documentation for this project in %s language.

PROJECT ANALYSIS:
Languages: %s
File count: %d
Project structure:
%s

SOURCE CODE ANALYSIS:
%s

REQUIREMENTS:
1. Write in %s language
2. Describe overall system architecture
3. Document key components and their responsibilities
4. Explain data flow and interactions
5. Include design patterns used
6. Document external dependencies
7. Describe security considerations
8. Include deployment architecture if applicable
9. Use proper Markdown formatting with diagrams in text format
10. Explain design decisions and trade-offs

Generate detailed architecture documentation that helps developers understand the system design and implementation.`,
		language,
		strings.Join(sourceInfo.Languages, ", "),
		len(sourceInfo.Files),
		sourceInfo.Structure,
		sourceCode,
		language)
}

func (v *VertexAIClient) buildGodocPrompt(sourceInfo *doc.SourceInfo, language string) string {
	sourceCode := v.formatSourceCode(sourceInfo)

	return fmt.Sprintf(`Generate Go package documentation in godoc style using %s language.

PROJECT ANALYSIS:
Languages: %s
File count: %d

SOURCE CODE ANALYSIS:
%s

REQUIREMENTS:
1. Write in %s language
2. Follow Go documentation conventions
3. Document all exported functions, types, and variables
4. Include package-level documentation
5. Provide clear examples for complex functions
6. Use proper godoc formatting
7. Include usage examples that can be run as tests
8. Document any interfaces and their implementations
9. Explain package purpose and design
10. Include performance notes where relevant

Generate comprehensive Go package documentation that follows Go community standards.`,
		language,
		strings.Join(sourceInfo.Languages, ", "),
		len(sourceInfo.Files),
		sourceCode,
		language)
}

func (v *VertexAIClient) formatSourceCode(sourceInfo *doc.SourceInfo) string {
	var result strings.Builder

	result.WriteString(fmt.Sprintf("Summary: %s\n\n", sourceInfo.Summary))

	// 重要なファイルのみを含める（サイズ制限）
	maxFiles := 10
	includedFiles := 0

	for _, file := range sourceInfo.Files {
		if includedFiles >= maxFiles {
			result.WriteString("... (additional files truncated for brevity)\n")
			break
		}

		// 大きなファイルは要約
		if len(file.Content) > 2000 {
			result.WriteString(fmt.Sprintf("File: %s (%s, %d bytes)\n", file.Path, file.Language, file.Size))
			result.WriteString(fmt.Sprintf("Content: %s...\n\n", file.Content[:2000]))
		} else {
			result.WriteString(fmt.Sprintf("File: %s (%s)\n", file.Path, file.Language))
			result.WriteString(fmt.Sprintf("Content:\n%s\n\n", file.Content))
		}

		includedFiles++
	}

	return result.String()
}

func (v *VertexAIClient) Close() error {
	return nil
}
