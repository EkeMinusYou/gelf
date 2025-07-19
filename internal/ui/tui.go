package ui

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/EkeMinusYou/gelf/internal/ai"
	"github.com/EkeMinusYou/gelf/internal/doc"
	"github.com/EkeMinusYou/gelf/internal/git"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type state int

const (
	stateLoading state = iota
	stateConfirm
	stateEditing
	stateCommitting
	stateSuccess
	stateError
)

type model struct {
	aiClient        *ai.VertexAIClient
	diff            string
	diffSummary     git.DiffSummary
	commitMessage   string
	originalMessage string
	err             error
	state           state
	spinner         spinner.Model
	textInput       textinput.Model
	commitLanguage  string
}

type msgCommitGenerated struct {
	message string
	err     error
}

type msgCommitDone struct {
	err error
}

func NewTUI(aiClient *ai.VertexAIClient, diff string, commitLanguage string) *model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = loadingStyle

	ti := textinput.New()
	ti.Placeholder = "Enter your commit message..."
	ti.CharLimit = 0
	ti.Width = 60

	diffSummary := git.ParseDiffSummary(diff)

	return &model{
		aiClient:       aiClient,
		diff:           diff,
		diffSummary:    diffSummary,
		state:          stateLoading,
		spinner:        s,
		textInput:      ti,
		commitLanguage: commitLanguage,
	}
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.generateCommitMessage())
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.state {
		case stateLoading:
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			}
		case stateConfirm:
			switch msg.String() {
			case "y", "Y":
				m.state = stateCommitting
				return m, tea.Batch(m.spinner.Tick, m.commitChanges())
			case "e", "E":
				m.originalMessage = m.commitMessage
				m.textInput.SetValue(m.commitMessage)
				m.textInput.Focus()
				m.state = stateEditing
				return m, textinput.Blink
			case "n", "N", "q", "ctrl+c":
				return m, tea.Quit
			}
		case stateEditing:
			switch msg.String() {
			case "enter":
				m.commitMessage = strings.TrimSpace(m.textInput.Value())
				if m.commitMessage == "" {
					m.commitMessage = m.originalMessage
				}
				m.textInput.Blur()
				m.state = stateConfirm
			case "esc":
				m.commitMessage = m.originalMessage
				m.textInput.Blur()
				m.state = stateConfirm
			default:
				m.textInput, cmd = m.textInput.Update(msg)
				return m, cmd
			}
		case stateSuccess, stateError:
			return m, tea.Quit
		}

	case msgCommitGenerated:
		if msg.err != nil {
			m.err = msg.err
			m.state = stateError
		} else {
			m.commitMessage = msg.message
			m.state = stateConfirm
		}

	case msgCommitDone:
		if msg.err != nil {
			m.err = msg.err
			m.state = stateError
		} else {
			m.state = stateSuccess
		}
		return m, tea.Quit
	}

	// Update spinner
	if m.state == stateLoading || m.state == stateCommitting {
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *model) View() string {
	switch m.state {
	case stateLoading:
		loadingText := fmt.Sprintf("%s %s",
			m.spinner.View(),
			loadingStyle.Render("Generating commit message..."))

		diffSummary := m.formatDiffSummary()
		if diffSummary != "" {
			return fmt.Sprintf("%s\n\n%s", diffSummary, loadingText)
		}
		return loadingText

	case stateConfirm:
		diffSummary := m.formatDiffSummary()
		header := titleStyle.Render("📝 Generated Commit Message:")
		message := messageStyle.Render(m.commitMessage)
		prompt := promptStyle.Render("Commit this message? (y)es / (e)dit / (n)o")

		if diffSummary != "" {
			return fmt.Sprintf("%s\n\n%s\n\n%s\n\n%s", diffSummary, header, message, prompt)
		}
		return fmt.Sprintf("%s\n\n%s\n\n%s", header, message, prompt)

	case stateEditing:
		diffSummary := m.formatDiffSummary()
		header := titleStyle.Render("✏️  Edit Commit Message:")
		inputView := m.textInput.View()
		prompt := editPromptStyle.Render("Press Enter to confirm, Esc to cancel")

		if diffSummary != "" {
			return fmt.Sprintf("%s\n\n%s\n\n%s\n\n%s", diffSummary, header, inputView, prompt)
		}
		return fmt.Sprintf("%s\n\n%s\n\n%s", header, inputView, prompt)

	case stateCommitting:
		return fmt.Sprintf("%s %s",
			m.spinner.View(),
			loadingStyle.Render("Committing changes..."))

	case stateSuccess:
		return ""

	case stateError:
		return errorStyle.Render(fmt.Sprintf("✗ Error: %v", m.err))
	}

	return ""
}

func (m *model) generateCommitMessage() tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		ctx := context.Background()
		message, err := m.aiClient.GenerateCommitMessage(ctx, m.diff, m.commitLanguage)
		return msgCommitGenerated{
			message: strings.TrimSpace(message),
			err:     err,
		}
	})
}

func (m *model) commitChanges() tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		err := git.CommitChanges(m.commitMessage)
		return msgCommitDone{err: err}
	})
}

func (m *model) formatDiffSummary() string {
	if len(m.diffSummary.Files) == 0 {
		return ""
	}

	var parts []string
	parts = append(parts, diffStyle.Render("📄 Changed Files:"))

	for _, file := range m.diffSummary.Files {
		fileName := fileStyle.Render(file.Name)

		var changes []string
		if file.AddedLines > 0 {
			changes = append(changes, addedStyle.Render(fmt.Sprintf("+%d", file.AddedLines)))
		}
		if file.DeletedLines > 0 {
			changes = append(changes, deletedStyle.Render(fmt.Sprintf("-%d", file.DeletedLines)))
		}

		if len(changes) > 0 {
			parts = append(parts, fmt.Sprintf(" • %s (%s)", fileName, strings.Join(changes, ", ")))
		} else {
			parts = append(parts, fmt.Sprintf(" • %s", fileName))
		}
	}

	return strings.Join(parts, "\n")
}

func (m *model) Run() error {
	p := tea.NewProgram(m)
	_, err := p.Run()

	// Print success message after TUI exits so it remains visible
	if m.state == stateSuccess {
		header := successStyle.Render("✓ Commit successful")
		message := messageStyle.Render(m.commitMessage)

		fmt.Printf("%s\n%s\n", header, message)
	}

	return err
}

// Review TUI model
type reviewModel struct {
	aiClient         *ai.VertexAIClient
	diff             string
	diffSummary      git.DiffSummary
	structuredReview *ai.StructuredReview
	err              error
	state            reviewState
	spinner          spinner.Model
	noStyle          bool
	reviewLanguage   string
}

type reviewState int

const (
	reviewStateLoading reviewState = iota
	reviewStateDisplay
	reviewStateError
)

type msgStructuredReviewGenerated struct {
	review *ai.StructuredReview
	err    error
}

// リッチなカラースタイル（枠なし）
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("6")) // シアン

	messageStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")). // ブライトホワイト
			Bold(true).
			Italic(true)

	promptStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("4")). // ブルー
			Bold(true)

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2")). // グリーン
			Bold(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1")). // レッド
			Bold(true)

	loadingStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")). // シアン
			Bold(true)

	editPromptStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("3")). // イエロー
			Bold(true)

	diffStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("7")) // ライトグレー

	fileStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("5")). // マゼンタ
			Bold(true)

	addedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2")) // グリーン

	deletedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1")) // レッド

	codeBlockStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("235")). // Dark gray background
			Padding(1).
			Margin(0, 2)
)

func DisableColor() {
	// No color styles
	titleStyle = lipgloss.NewStyle().Bold(true)
	messageStyle = lipgloss.NewStyle().Bold(true).Italic(true)
	promptStyle = lipgloss.NewStyle().Bold(true)
	successStyle = lipgloss.NewStyle().Bold(true)
	errorStyle = lipgloss.NewStyle().Bold(true)
	loadingStyle = lipgloss.NewStyle().Bold(true)
	editPromptStyle = lipgloss.NewStyle().Bold(true)
	diffStyle = lipgloss.NewStyle()
	fileStyle = lipgloss.NewStyle().Bold(true)
	addedStyle = lipgloss.NewStyle()
	deletedStyle = lipgloss.NewStyle()
	codeBlockStyle = lipgloss.NewStyle().Padding(1).Margin(0, 2)
}

func NewReviewTUI(aiClient *ai.VertexAIClient, diff string, noStyle bool, reviewLanguage string) *reviewModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = loadingStyle

	diffSummary := git.ParseDiffSummary(diff)

	return &reviewModel{
		aiClient:       aiClient,
		diff:           diff,
		diffSummary:    diffSummary,
		state:          reviewStateLoading,
		spinner:        s,
		noStyle:        noStyle,
		reviewLanguage: reviewLanguage,
	}
}

func (m *reviewModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.generateStructuredReview())
}

func (m *reviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.state {
		case reviewStateLoading:
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			}
		case reviewStateDisplay, reviewStateError:
			return m, tea.Quit
		}

	case msgStructuredReviewGenerated:
		if msg.err != nil {
			m.err = msg.err
			m.state = reviewStateError
		} else {
			m.structuredReview = msg.review
			m.state = reviewStateDisplay
		}
		return m, tea.Quit
	}

	// Update spinner
	if m.state == reviewStateLoading {
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *reviewModel) View() string {
	switch m.state {
	case reviewStateLoading:
		loadingText := fmt.Sprintf("%s %s",
			m.spinner.View(),
			loadingStyle.Render("Analyzing code for review..."))

		diffSummary := m.formatReviewDiffSummary()
		if diffSummary != "" {
			return fmt.Sprintf("%s\n\n%s", diffSummary, loadingText)
		}
		return loadingText

	case reviewStateDisplay:
		return "" // Review will be printed after TUI exits

	case reviewStateError:
		return errorStyle.Render(fmt.Sprintf("✗ Error: %v", m.err))
	}

	return ""
}

func (m *reviewModel) Run() (string, error) {
	p := tea.NewProgram(m)
	_, err := p.Run()

	if m.err != nil {
		return "", m.err
	}

	// Print the structured review after TUI exits
	if m.structuredReview != nil {
		m.printStructuredReview()
		return "Review completed", err
	}

	return "", err
}

// Documentation TUI model
type docModel struct {
	analyzer       *doc.Analyzer
	aiClient       *ai.VertexAIClient
	template       string
	format         string
	outputPath     string
	sourceInfo     *doc.SourceInfo
	documentation  string
	err            error
	state          docState
	spinner        spinner.Model
	language       string
}

type docState int

const (
	docStateAnalyzing docState = iota
	docStateGenerating
	docStateWriting
	docStateSuccess
	docStateError
)

type msgSourceAnalyzed struct {
	sourceInfo *doc.SourceInfo
	err        error
}

type msgDocumentationGenerated struct {
	documentation string
	err           error
}

type msgDocumentationWritten struct {
	err error
}

func NewDocTUI(analyzer *doc.Analyzer, aiClient *ai.VertexAIClient, template, format, outputPath, language string) *docModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = loadingStyle

	return &docModel{
		analyzer:   analyzer,
		aiClient:   aiClient,
		template:   template,
		format:     format,
		outputPath: outputPath,
		state:      docStateAnalyzing,
		spinner:    s,
		language:   language,
	}
}

func (m *docModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.analyzeSource())
}

func (m *docModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.state {
		case docStateAnalyzing, docStateGenerating, docStateWriting:
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			}
		case docStateSuccess, docStateError:
			return m, tea.Quit
		}

	case msgSourceAnalyzed:
		if msg.err != nil {
			m.err = msg.err
			m.state = docStateError
			return m, tea.Quit
		}
		m.sourceInfo = msg.sourceInfo
		m.state = docStateGenerating
		return m, m.generateDocumentation()

	case msgDocumentationGenerated:
		if msg.err != nil {
			m.err = msg.err
			m.state = docStateError
			return m, tea.Quit
		}
		m.documentation = msg.documentation
		m.state = docStateWriting
		return m, m.writeDocumentation()

	case msgDocumentationWritten:
		if msg.err != nil {
			m.err = msg.err
			m.state = docStateError
		} else {
			m.state = docStateSuccess
		}
		return m, tea.Quit
	}

	// Update spinner
	if m.state == docStateAnalyzing || m.state == docStateGenerating || m.state == docStateWriting {
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *docModel) View() string {
	switch m.state {
	case docStateAnalyzing:
		return fmt.Sprintf("%s %s",
			m.spinner.View(),
			loadingStyle.Render("Analyzing source code..."))

	case docStateGenerating:
		return fmt.Sprintf("%s %s",
			m.spinner.View(),
			loadingStyle.Render(fmt.Sprintf("Generating %s documentation...", m.template)))

	case docStateWriting:
		return fmt.Sprintf("%s %s",
			m.spinner.View(),
			loadingStyle.Render("Writing documentation file..."))

	case docStateSuccess:
		return successStyle.Render(fmt.Sprintf("✓ Documentation generated successfully: %s", m.outputPath))

	case docStateError:
		return errorStyle.Render(fmt.Sprintf("✗ Error: %v", m.err))
	}

	return ""
}

func (m *docModel) Run() error {
	p := tea.NewProgram(m)
	_, err := p.Run()

	if m.err != nil {
		return m.err
	}

	// Print success message after TUI exits
	if m.state == docStateSuccess {
		fmt.Printf("%s\n", successStyle.Render(fmt.Sprintf("✓ Documentation generated successfully: %s", m.outputPath)))
	}

	return err
}

func (m *docModel) analyzeSource() tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		sourceInfo, err := m.analyzer.Analyze()
		return msgSourceAnalyzed{
			sourceInfo: sourceInfo,
			err:        err,
		}
	})
}

func (m *docModel) generateDocumentation() tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		ctx := context.Background()
		documentation, err := m.aiClient.GenerateDocumentation(ctx, m.sourceInfo, m.template, m.language)
		return msgDocumentationGenerated{
			documentation: documentation,
			err:           err,
		}
	})
}

func (m *docModel) writeDocumentation() tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		output, err := doc.FormatOutput(m.documentation, m.format)
		if err != nil {
			return msgDocumentationWritten{err: err}
		}

		err = os.WriteFile(m.outputPath, []byte(output), 0644)
		return msgDocumentationWritten{err: err}
	})
}

// generateStructuredReview generates a structured review
func (m *reviewModel) generateStructuredReview() tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		ctx := context.Background()
		review, err := m.aiClient.ReviewCodeStructured(ctx, m.diff, m.reviewLanguage)
		return msgStructuredReviewGenerated{
			review: review,
			err:    err,
		}
	})
}

// printStructuredReview prints the structured review with diff and comments
func (m *reviewModel) printStructuredReview() {
	if m.structuredReview == nil {
		return
	}

	// Print overall summary
	fmt.Printf("%s\n%s\n",
		titleStyle.Render("📋 Code Review Summary:"),
		m.structuredReview.Summary)
	fmt.Println()

	// Print each file with its diff and comments
	for _, fileReview := range m.structuredReview.FileReviews {
		m.printFileReview(fileReview)
	}
}

// printFileReview prints a single file's review with diff and comments
func (m *reviewModel) printFileReview(fileReview ai.FileReview) {
	// Simple file header like Claude Code
	fmt.Printf("\n%s %s\n",
		fileStyle.Render("📄"),
		fileStyle.Render(fileReview.FileName))
	fmt.Println(strings.Repeat("─", len(fileReview.FileName)+3))

	if len(fileReview.Comments) > 0 {
		// Print each comment with its relevant code context
		for i, comment := range fileReview.Comments {
			if i > 0 {
				fmt.Println() // Space between comment blocks
			}

			// Print relevant code context for this specific comment first
			if fileReview.DiffText != "" && comment.LineNo > 0 {
				m.printCodeContext(fileReview.DiffText, comment.LineNo)
				fmt.Println()
			}

			// Print comment after code context
			m.printComment(comment)
		}

		// If there are comments without line numbers, show general diff
		hasGeneralComments := false
		for _, comment := range fileReview.Comments {
			if comment.LineNo == 0 {
				hasGeneralComments = true
				break
			}
		}

		if hasGeneralComments && fileReview.DiffText != "" {
			fmt.Println()
			fmt.Printf("  %s\n", diffStyle.Render("📋 Related changes:"))
			diffLines := m.getRelevantDiffSections(fileReview.DiffText, fileReview.Comments)
			for _, line := range diffLines {
				fmt.Println("    " + line)
			}
		}
	} else {
		fmt.Printf("  %s\n", successStyle.Render("✓ No issues found"))
	}
}

// extractRelevantDiffLines extracts only the important lines from a diff
func (m *reviewModel) extractRelevantDiffLines(lines []string) []string {
	var result []string
	var currentHunk []string
	inHunk := false
	hasChanges := false

	for i, line := range lines {
		// Header lines (always include)
		if strings.HasPrefix(line, "diff --git") ||
			strings.HasPrefix(line, "index") ||
			strings.HasPrefix(line, "+++") ||
			strings.HasPrefix(line, "---") {
			result = append(result, line)
			continue
		}

		// Hunk header
		if strings.HasPrefix(line, "@@") {
			// Process previous hunk if it had changes
			if inHunk && hasChanges {
				result = append(result, currentHunk...)
			}

			// Start new hunk
			currentHunk = []string{line}
			inHunk = true
			hasChanges = false
			continue
		}

		if inHunk {
			currentHunk = append(currentHunk, line)

			// Check if this line is a change (added or removed)
			if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
				hasChanges = true
			}

			// If this is the last line or next line starts a new hunk/file
			if i == len(lines)-1 ||
				(i+1 < len(lines) && (strings.HasPrefix(lines[i+1], "@@") ||
					strings.HasPrefix(lines[i+1], "diff --git"))) {

				if hasChanges {
					// Only include context lines around changes
					result = append(result, m.filterHunkLines(currentHunk)...)
				}
				inHunk = false
				hasChanges = false
			}
		}
	}

	return result
}

// filterHunkLines filters a hunk to show only changed lines with minimal context
func (m *reviewModel) filterHunkLines(hunkLines []string) []string {
	if len(hunkLines) == 0 {
		return hunkLines
	}

	// Always include the hunk header
	result := []string{hunkLines[0]}

	// Find changed lines (+ or -)
	changedIndices := make(map[int]bool)
	for i := 1; i < len(hunkLines); i++ {
		line := hunkLines[i]
		if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
			changedIndices[i] = true
		}
	}

	// If no changes, return just the header
	if len(changedIndices) == 0 {
		return result
	}

	// Include changed lines with minimal context (1 line before/after)
	contextLines := 1
	included := make(map[int]bool)

	for changeIdx := range changedIndices {
		// Include the changed line
		included[changeIdx] = true

		// Include context lines
		for j := max(1, changeIdx-contextLines); j <= min(len(hunkLines)-1, changeIdx+contextLines); j++ {
			included[j] = true
		}
	}

	// Add included lines in order
	for i := 1; i < len(hunkLines); i++ {
		if included[i] {
			result = append(result, hunkLines[i])
		}
	}

	return result
}

// getRelevantDiffSections returns diff sections that are relevant to comments as strings
func (m *reviewModel) getRelevantDiffSections(diff string, comments []ai.ReviewComment) []string {
	lines := strings.Split(diff, "\n")

	// If no comments have line numbers, show a minimal diff
	commentLines := make(map[int]bool)
	hasLineNumbers := false
	for _, comment := range comments {
		if comment.LineNo > 0 {
			commentLines[comment.LineNo] = true
			hasLineNumbers = true
		}
	}

	if !hasLineNumbers {
		// Show only changed lines if no line numbers in comments
		return m.extractRelevantDiffLines(lines)
	}

	// Find relevant hunks based on comment line numbers
	relevantLines := m.extractCommentRelevantLines(lines, commentLines)

	// Apply syntax highlighting to each line
	var styledLines []string
	for _, line := range relevantLines {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			styledLines = append(styledLines, addedStyle.Render(line))
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			styledLines = append(styledLines, deletedStyle.Render(line))
		} else if strings.HasPrefix(line, "@@") {
			styledLines = append(styledLines, titleStyle.Render(line))
		} else if strings.HasPrefix(line, "diff --git") || strings.HasPrefix(line, "index") {
			styledLines = append(styledLines, diffStyle.Render(line))
		} else {
			styledLines = append(styledLines, line)
		}
	}

	return styledLines
}

// extractCommentRelevantLines extracts diff lines that are relevant to comment line numbers
func (m *reviewModel) extractCommentRelevantLines(lines []string, commentLines map[int]bool) []string {
	var result []string
	currentLineNum := 0
	inHunk := false
	contextWindow := 3 // Show 3 lines before/after comment lines

	for i, line := range lines {
		// Header lines (always include)
		if strings.HasPrefix(line, "diff --git") ||
			strings.HasPrefix(line, "index") ||
			strings.HasPrefix(line, "+++") ||
			strings.HasPrefix(line, "---") {
			result = append(result, line)
			continue
		}

		// Hunk header - parse line numbers
		if strings.HasPrefix(line, "@@") {
			result = append(result, line)
			inHunk = true
			// Extract starting line number from @@ -old_start,old_count +new_start,new_count @@
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				newPart := parts[2] // +new_start,new_count
				if strings.HasPrefix(newPart, "+") {
					newPart = strings.TrimPrefix(newPart, "+")
					if commaIdx := strings.Index(newPart, ","); commaIdx >= 0 {
						newPart = newPart[:commaIdx]
					}
					if num, err := fmt.Sscanf(newPart, "%d", &currentLineNum); err == nil && num == 1 {
						currentLineNum-- // Will be incremented below
					}
				}
			}
			continue
		}

		if inHunk {
			// Track line numbers
			if !strings.HasPrefix(line, "-") {
				currentLineNum++
			}

			// Check if this line or nearby lines have comments
			isRelevant := false
			for commentLine := range commentLines {
				if abs(currentLineNum-commentLine) <= contextWindow {
					isRelevant = true
					break
				}
			}

			if isRelevant {
				result = append(result, line)
			}

			// End of hunk
			if i == len(lines)-1 ||
				(i+1 < len(lines) && (strings.HasPrefix(lines[i+1], "@@") ||
					strings.HasPrefix(lines[i+1], "diff --git"))) {
				inHunk = false
			}
		}
	}

	return result
}

// abs returns the absolute value of x
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// Helper functions for min/max
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// printComment prints a review comment in Claude Code style
func (m *reviewModel) printComment(comment ai.ReviewComment) {
	var prefix string
	var style lipgloss.Style

	switch comment.Type {
	case "must":
		prefix = "🚨 MUST FIX"
		style = errorStyle
	case "want":
		prefix = "💡 SUGGEST"
		style = promptStyle
	case "nits":
		prefix = "✨ STYLE"
		style = editPromptStyle
	case "fyi":
		prefix = "ℹ️  INFO"
		style = titleStyle
	case "imo":
		prefix = "💭 OPINION"
		style = diffStyle
	default:
		prefix = "💬 NOTE"
		style = diffStyle
	}

	lineInfo := ""
	if comment.LineNo > 0 {
		lineInfo = fmt.Sprintf(" (Line %d)", comment.LineNo)
	}

	// Simple format like Claude Code with consistent alignment
	fmt.Printf("  %s%s\n",
		style.Render(prefix+lineInfo+":"),
		"")
	fmt.Printf("  %s\n", comment.Message)
}

// printCodeContext prints relevant code context for a specific line
func (m *reviewModel) printCodeContext(diff string, targetLine int) {
	lines := strings.Split(diff, "\n")
	contextLines := m.getCodeContextForLine(lines, targetLine)

	if len(contextLines) > 0 {
		fmt.Printf("  %s\n", diffStyle.Render("📋 Code context:"))

		codeContent := strings.Join(contextLines, "\n")
		fmt.Println(codeBlockStyle.Render(codeContent))
	}
}

// getCodeContextForLine extracts code context around a specific line
func (m *reviewModel) getCodeContextForLine(lines []string, targetLine int) []string {
	var result []string
	currentLineNum := 0
	inHunk := false
	contextWindow := 3 // Show 3 lines before/after target line

	for i, line := range lines {
		// Header lines (always include if we find relevant context)
		if strings.HasPrefix(line, "diff --git") ||
			strings.HasPrefix(line, "index") ||
			strings.HasPrefix(line, "+++") ||
			strings.HasPrefix(line, "---") {
			continue // Skip for individual context
		}

		// Hunk header - parse line numbers
		if strings.HasPrefix(line, "@@") {
			inHunk = true
			// Extract starting line number
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				newPart := parts[2] // +new_start,new_count
				if strings.HasPrefix(newPart, "+") {
					newPart = strings.TrimPrefix(newPart, "+")
					if commaIdx := strings.Index(newPart, ","); commaIdx >= 0 {
						newPart = newPart[:commaIdx]
					}
					if num, err := fmt.Sscanf(newPart, "%d", &currentLineNum); err == nil && num == 1 {
						currentLineNum-- // Will be incremented below
					}
				}
			}

			// Check if this hunk contains our target line
			hunkContainsTarget := false
			tempLineNum := currentLineNum
			for j := i + 1; j < len(lines); j++ {
				if strings.HasPrefix(lines[j], "@@") || strings.HasPrefix(lines[j], "diff --git") {
					break
				}
				if !strings.HasPrefix(lines[j], "-") {
					tempLineNum++
				}
				if abs(tempLineNum-targetLine) <= contextWindow {
					hunkContainsTarget = true
					break
				}
			}

			if hunkContainsTarget {
				result = append(result, titleStyle.Render(line))
			}
			continue
		}

		if inHunk {
			// Track line numbers
			if !strings.HasPrefix(line, "-") {
				currentLineNum++
			}

			// Check if this line is within context window of target
			if abs(currentLineNum-targetLine) <= contextWindow {
				if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
					result = append(result, addedStyle.Render(line))
				} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
					result = append(result, deletedStyle.Render(line))
				} else {
					result = append(result, line)
				}
			}

			// End of hunk
			if i == len(lines)-1 ||
				(i+1 < len(lines) && (strings.HasPrefix(lines[i+1], "@@") ||
					strings.HasPrefix(lines[i+1], "diff --git"))) {
				inHunk = false
			}
		}
	}

	return result
}

func (m *reviewModel) formatReviewDiffSummary() string {
	if len(m.diffSummary.Files) == 0 {
		return ""
	}

	var parts []string
	parts = append(parts, diffStyle.Render("📄 Changed Files:"))

	for _, file := range m.diffSummary.Files {
		fileName := fileStyle.Render(file.Name)

		var changes []string
		if file.AddedLines > 0 {
			changes = append(changes, addedStyle.Render(fmt.Sprintf("+%d", file.AddedLines)))
		}
		if file.DeletedLines > 0 {
			changes = append(changes, deletedStyle.Render(fmt.Sprintf("-%d", file.DeletedLines)))
		}

		if len(changes) > 0 {
			parts = append(parts, fmt.Sprintf(" • %s (%s)", fileName, strings.Join(changes, ", ")))
		} else {
			parts = append(parts, fmt.Sprintf(" • %s", fileName))
		}
	}

	return strings.Join(parts, "\n")
}
