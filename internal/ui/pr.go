package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/EkeMinusYou/gelf/internal/ai"
	"github.com/EkeMinusYou/gelf/internal/git"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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
	viewport     viewport.Model
	viewWidth    int
	viewHeight   int
	viewReady    bool
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
	case tea.WindowSizeMsg:
		m.viewWidth = msg.Width
		m.viewHeight = msg.Height
		m.updateViewportSize()
		return m, nil

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
			default:
				if m.viewReady {
					m.viewport, cmd = m.viewport.Update(msg)
					return m, cmd
				}
			}
		case prStateDone, prStateError:
			return m, tea.Quit
		}

	case tea.MouseMsg:
		if m.state == prStateConfirm && m.viewReady {
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}

	case msgPRGenerated:
		if msg.err != nil {
			m.err = msg.err
			m.state = prStateError
		} else {
			m.content = msg.content
			m.renderedBody = msg.renderedBody
			m.state = prStateConfirm
			m.refreshViewportContent(true)
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
			loadingStyle.Render("Generating pull request message..."))

		return fmt.Sprintf("%s\n\n%s", formatPRContext(m.diffSummary, m.commitLines), loadingText)

	case prStateConfirm:
		prompt := promptStyle.Render("Create this pull request? (y)es / (n)o")
		if m.viewReady {
			return fmt.Sprintf("%s\n\n%s", m.viewport.View(), prompt)
		}
		return fmt.Sprintf("%s\n\n%s", m.buildPRContent(), prompt)

	case prStateError:
		return errorStyle.Render(fmt.Sprintf("✗ Error: %v", m.err))

	case prStateDone:
		return ""
	}

	return ""
}

func (m *prModel) Run() (*ai.PullRequestContent, bool, error) {
	p := tea.NewProgram(m, tea.WithMouseCellMotion())
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

func (m *prModel) buildPRContent() string {
	header := titleStyle.Render("📝 Generated Pull Request:")
	title := messageStyle.Render(m.content.Title)
	body := m.content.Body
	if m.render && m.renderedBody != "" {
		body = m.renderedBody
	}

	sections := []string{}
	context := formatPRContext(m.diffSummary, m.commitLines)
	if context != "" {
		sections = append(sections, context)
	}
	sections = append(sections, header, title, body)

	return strings.Join(sections, "\n\n")
}

func (m *prModel) updateViewportSize() {
	if m.viewWidth <= 0 || m.viewHeight <= 0 {
		return
	}

	if !m.viewReady {
		m.viewport = viewport.New(m.viewWidth, m.viewportHeight())
		m.viewReady = true
	} else {
		m.viewport.Width = m.viewWidth
		m.viewport.Height = m.viewportHeight()
	}

	m.refreshViewportContent(false)
}

func (m *prModel) refreshViewportContent(reset bool) {
	if !m.viewReady || m.state != prStateConfirm || m.content == nil {
		return
	}

	content := m.buildPRContent()
	if m.viewWidth > 0 {
		content = ansi.Wrap(content, m.viewWidth, "")
	}
	content = strings.TrimRight(content, "\n")

	m.viewport.SetContent(content)
	if reset {
		m.viewport.GotoTop()
	}
}

func (m *prModel) viewportHeight() int {
	reserved := 2
	height := m.viewHeight - reserved
	if height < 1 {
		return 1
	}
	return height
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
