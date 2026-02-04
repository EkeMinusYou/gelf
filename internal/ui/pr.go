package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/EkeMinusYou/gelf/internal/ai"
	"github.com/EkeMinusYou/gelf/internal/git"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

type prState int

const (
	prStateLoading prState = iota
	prStateConfirm
	prStateDone
	prStateError
)

type prModel struct {
	aiClient     *ai.VertexAIClient
	input        ai.PullRequestInput
	diffSummary  git.DiffSummary
	commitLines  []string
	render       bool
	useColor     bool
	renderedBody string
	content      *ai.PullRequestContent
	confirmed    bool
	err          error
	state        prState
	spinner      spinner.Model
}

type msgPRGenerated struct {
	content      *ai.PullRequestContent
	renderedBody string
	err          error
}

func NewPRTUI(aiClient *ai.VertexAIClient, input ai.PullRequestInput, render bool, useColor bool) *prModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = loadingStyle

	diffSummary := git.ParseDiffSummary(input.Diff)
	commitLines := parseCommitLines(input.CommitLog)

	return &prModel{
		aiClient:    aiClient,
		input:       input,
		diffSummary: diffSummary,
		commitLines: commitLines,
		render:      render,
		useColor:    useColor,
		state:       prStateLoading,
		spinner:     s,
	}
}

func (m *prModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.generatePRContent())
}

func (m *prModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.state {
		case prStateLoading:
			switch msg.String() {
			case "q", "ctrl+c":
				m.state = prStateDone
				return m, tea.Quit
			}
		case prStateConfirm:
			switch msg.String() {
			case "y", "Y":
				m.confirmed = true
				m.state = prStateDone
				return m, tea.Quit
			case "n", "N", "q", "ctrl+c":
				m.confirmed = false
				m.state = prStateDone
				return m, tea.Quit
			}
		case prStateDone, prStateError:
			return m, tea.Quit
		}

	case msgPRGenerated:
		if msg.err != nil {
			m.err = msg.err
			m.state = prStateError
		} else {
			m.content = msg.content
			m.renderedBody = msg.renderedBody
			m.state = prStateConfirm
		}
		return m, nil
	}

	if m.state == prStateLoading {
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *prModel) View() string {
	switch m.state {
	case prStateLoading:
		loadingText := fmt.Sprintf("%s %s",
			m.spinner.View(),
			loadingStyle.Render("Generating pull request..."))

		return fmt.Sprintf("%s\n\n%s", formatPRContext(m.diffSummary, m.commitLines), loadingText)

	case prStateConfirm:
		header := titleStyle.Render("📝 Generated Pull Request:")
		title := messageStyle.Render(m.content.Title)
		body := m.content.Body
		if m.render && m.renderedBody != "" {
			body = m.renderedBody
		}

		prompt := promptStyle.Render("Create this pull request? (y)es / (n)o")
		context := formatPRContext(m.diffSummary, m.commitLines)
		if context != "" {
			return fmt.Sprintf("%s\n\n%s\n\n%s\n\n%s\n\n%s", context, header, title, body, prompt)
		}
		return fmt.Sprintf("%s\n\n%s\n\n%s\n\n%s", header, title, body, prompt)

	case prStateError:
		return errorStyle.Render(fmt.Sprintf("✗ Error: %v", m.err))

	case prStateDone:
		return ""
	}

	return ""
}

func (m *prModel) Run() (*ai.PullRequestContent, bool, error) {
	p := tea.NewProgram(m)
	_, err := p.Run()
	if err != nil {
		return nil, false, err
	}

	if m.err != nil {
		return nil, false, m.err
	}

	return m.content, m.confirmed, nil
}

func (m *prModel) generatePRContent() tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		ctx := context.Background()
		content, err := m.aiClient.GeneratePullRequestContent(ctx, m.input)
		if err != nil {
			return msgPRGenerated{err: err}
		}

		rendered := ""
		if m.render {
			rendered, err = RenderMarkdown(content.Body, m.useColor)
			if err != nil {
				rendered = ""
			}
		}

		return msgPRGenerated{
			content:      content,
			renderedBody: strings.TrimRight(rendered, "\n"),
		}
	})
}

func formatPRDiffSummary(summary git.DiffSummary) string {
	if len(summary.Files) == 0 {
		return ""
	}

	var parts []string
	parts = append(parts, diffStyle.Render("📄 Changed Files:"))

	for _, file := range summary.Files {
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

func formatPRContext(summary git.DiffSummary, commitLines []string) string {
	sections := []string{}

	diffSummary := formatPRDiffSummary(summary)
	if diffSummary != "" {
		sections = append(sections, diffSummary)
	}

	if len(commitLines) > 0 {
		sections = append(sections, formatPRCommitLog(commitLines))
	}

	return strings.Join(sections, "\n\n")
}

func formatPRCommitLog(commitLines []string) string {
	parts := []string{diffStyle.Render("🧾 Commits:")}
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
