package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/EkeMinusYou/gelf/internal/ai"
	"github.com/EkeMinusYou/gelf/internal/git"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
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
	session         *Session
	styles          Styles
	ctx             context.Context
	cancel          context.CancelFunc
	commit          func(context.Context, string) error
	aiClient        ai.Client
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

func NewTUI(session *Session, aiClient ai.Client, diff string, summary git.DiffSummary, commitLanguage string, commit func(context.Context, string) error) *model {
	ctx, cancel := context.WithCancel(session.Context)
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = session.Styles.Loading

	ti := textinput.New()
	ti.Placeholder = "Enter your commit message..."
	ti.CharLimit = 0
	ti.Width = 60

	return &model{
		session: session, styles: session.Styles, ctx: ctx, cancel: cancel, commit: commit,
		aiClient:       aiClient,
		diff:           diff,
		diffSummary:    summary,
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
		if msg.String() == "ctrl+c" && m.state != stateCommitting {
			return m, tea.Quit
		}
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
			return m, tea.Quit
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
			m.styles.Loading.Render("Generating commit message..."))

		diffSummary := m.formatDiffSummary()
		if diffSummary != "" {
			return fmt.Sprintf("%s\n\n%s", diffSummary, loadingText)
		}
		return loadingText

	case stateConfirm:
		diffSummary := m.formatDiffSummary()
		header := m.styles.Title.Render("📝 Generated Commit Message:")
		message := m.styles.Message.Render(m.commitMessage)
		prompt := m.styles.Prompt.Render("Commit this message? (y)es / (e)dit / (n)o")

		if diffSummary != "" {
			return fmt.Sprintf("%s\n\n%s\n\n%s\n\n%s", diffSummary, header, message, prompt)
		}
		return fmt.Sprintf("%s\n\n%s\n\n%s", header, message, prompt)

	case stateEditing:
		diffSummary := m.formatDiffSummary()
		header := m.styles.Title.Render("✏️  Edit Commit Message:")
		inputView := m.textInput.View()
		prompt := m.styles.EditPrompt.Render("Press Enter to confirm, Esc to cancel")

		if diffSummary != "" {
			return fmt.Sprintf("%s\n\n%s\n\n%s\n\n%s", diffSummary, header, inputView, prompt)
		}
		return fmt.Sprintf("%s\n\n%s\n\n%s", header, inputView, prompt)

	case stateCommitting:
		return fmt.Sprintf("%s %s",
			m.spinner.View(),
			m.styles.Loading.Render("Committing changes..."))

	case stateSuccess:
		return ""

	case stateError:
		return m.styles.Error.Render(fmt.Sprintf("✗ Error: %v", m.err))
	}

	return ""
}

func (m *model) generateCommitMessage() tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		ctx := m.ctx
		message, err := m.aiClient.GenerateCommitMessage(ctx, m.diff, m.commitLanguage)
		return msgCommitGenerated{
			message: strings.TrimSpace(message),
			err:     err,
		}
	})
}

func (m *model) commitChanges() tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		err := m.commit(m.ctx, m.commitMessage)
		return msgCommitDone{err: err}
	})
}

func (m *model) formatDiffSummary() string {
	return formatDiffSummary(m.diffSummary, m.styles)
}

func (m *model) Run(options ...tea.ProgramOption) error {
	defer m.cancel()
	options = append([]tea.ProgramOption{tea.WithContext(m.ctx), tea.WithInput(m.session.In), tea.WithOutput(m.session.Out)}, options...)
	p := tea.NewProgram(m, options...)
	_, err := p.Run()

	// Print success message after TUI exits so it remains visible
	if m.state == stateSuccess {
		header := m.styles.Success.Render("✓ Commit successful")
		message := m.styles.Message.Render(m.commitMessage)

		fmt.Fprintf(m.session.Out, "%s\n%s\n", header, message)
	}

	if err != nil {
		return err
	}
	return m.err
}
