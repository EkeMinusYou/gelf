package ui

import (
	"fmt"
	"strings"

	"github.com/EkeMinusYou/gelf/internal/ai"
	"github.com/EkeMinusYou/gelf/internal/git"
)

type prModel struct {
	session        *Session
	styles         Styles
	aiClient       ai.Client
	input          ai.PullRequestInput
	diffSummary    git.DiffSummary
	commitLines    []string
	render         bool
	useColor       bool
	renderedBody   string
	content        *ai.PullRequestContent
	printedContext bool
	confirmPrompt  string
}

func NewPRTUI(session *Session, aiClient ai.Client, input ai.PullRequestInput, summary git.DiffSummary, render bool, confirmPrompt string) *prModel {
	commitLines := parseCommitLines(input.CommitLog)

	return &prModel{
		session: session, styles: session.Styles,
		aiClient:    aiClient,
		input:       input,
		diffSummary: summary,
		commitLines: commitLines,
		render:      render,
		useColor:    session.UseColor,
		confirmPrompt: func() string {
			if strings.TrimSpace(confirmPrompt) == "" {
				return "Create this pull request? (y)es / (r)evise / (n)o"
			}
			return confirmPrompt
		}(),
	}
}

func (m *prModel) Run() (*ai.PullRequestContent, bool, error) {
	ctx := m.session.Context
	loadingContext := formatPRContext(m.diffSummary, m.commitLines, m.styles)
	stopSpinner := m.startLoadingIndicator(loadingContext)
	content, err := m.aiClient.GeneratePullRequestContent(ctx, m.input)
	stopSpinner()
	if err != nil {
		return nil, false, err
	}

	m.content = content
	m.refreshRenderedBody()

	fmt.Fprintf(m.session.Out, "%s\n", m.buildPRContent())
	fmt.Fprintln(m.session.Out)

	for {
		choice, err := m.session.PRChoice(m.confirmPrompt)
		if err != nil {
			return nil, false, err
		}

		switch choice {
		case PRChoiceYes:
			return m.content, true, nil
		case PRChoiceNo:
			return m.content, false, nil
		case PRChoiceRevise:
			instructions, ok, err := m.session.Line(
				"💬 Tell me how to revise the pull request:",
				"e.g. shorten the title and clarify the summary",
			)
			if err != nil {
				return nil, false, err
			}
			if !ok || instructions == "" {
				continue
			}

			revisionStop := m.startRevisionIndicator()
			revised, err := m.aiClient.RevisePullRequestContent(ctx, m.input, m.content, instructions)
			revisionStop()
			if err != nil {
				fmt.Fprintf(m.session.Err, "%s\n\n", m.styles.Error.Render(fmt.Sprintf("✗ Failed to revise pull request: %v", err)))
				continue
			}

			m.content = revised
			m.refreshRenderedBody()
			m.printedContext = true

			fmt.Fprintln(m.session.Out)
			fmt.Fprintf(m.session.Out, "%s\n", m.buildPRContent())
			fmt.Fprintln(m.session.Out)
		}
	}
}

func (m *prModel) refreshRenderedBody() {
	m.renderedBody = ""
	if !m.render || m.content == nil {
		return
	}
	rendered, err := RenderMarkdown(m.content.Body, m.useColor)
	if err == nil {
		m.renderedBody = strings.TrimRight(rendered, "\n")
	}
}

func (m *prModel) startLoadingIndicator(context string) func() {
	if !isTerminalWriter(m.session.Err) {
		return func() {}
	}

	if strings.TrimSpace(context) != "" {
		fmt.Fprintln(m.session.Err, context)
		fmt.Fprintln(m.session.Err)
		m.printedContext = true
	}

	return m.session.Spinner("Generating pull request message...", false)
}

func (m *prModel) startRevisionIndicator() func() {
	if !isTerminalWriter(m.session.Err) {
		return func() {}
	}
	return m.session.Spinner("Revising pull request based on your feedback...", false)
}

func (m *prModel) buildPRContent() string {
	header := m.styles.Title.Render("📝 Generated Pull Request:")
	title := m.styles.Message.Render(m.content.Title)
	body := m.content.Body
	if m.render && m.renderedBody != "" {
		body = m.renderedBody
	}

	sections := []string{}
	if !m.printedContext {
		context := formatPRContext(m.diffSummary, m.commitLines, m.styles)
		if context != "" {
			sections = append(sections, context)
		}
	}
	sections = append(sections, header, title, body)

	return strings.Join(sections, "\n\n")
}

func formatPRContext(summary git.DiffSummary, commitLines []string, styles Styles) string {
	sections := []string{}

	diffSummary := formatDiffSummary(summary, styles)
	if diffSummary != "" {
		sections = append(sections, diffSummary)
	}

	if len(commitLines) > 0 {
		sections = append(sections, formatPRCommitLog(commitLines, styles))
	}

	return strings.Join(sections, "\n\n")
}

func formatPRCommitLog(commitLines []string, styles Styles) string {
	parts := []string{styles.Diff.Render("🧾 Commits:")}
	for _, line := range commitLines {
		parts = append(parts, fmt.Sprintf(" • %s", line))
	}
	return strings.Join(parts, "\n")
}

func parseCommitLines(log string) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}
