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
	statePrompting
	stateCommitting
	stateSuccess
	stateError
)

type model struct {
	session       *Session
	styles        Styles
	ctx           context.Context
	cancel        context.CancelFunc
	commit        func(context.Context, string) error
	aiClient      ai.Client
	input         ai.CommitInput
	diffSummary   git.DiffSummary
	commitMessage string
	err           error
	state         state
	spinner       spinner.Model
	revisionInput textinput.Model
	// revising marks the loading state as a revision, whose failure returns to confirmation.
	revising bool
	notice   string
}

type msgCommitGenerated struct {
	message  string
	err      error
	revision bool
}

type msgEdited struct {
	edit *editSession
	err  error
}

type msgCommitDone struct {
	err error
}

func NewTUI(session *Session, aiClient ai.Client, input ai.CommitInput, summary git.DiffSummary, commit func(context.Context, string) error) *model {
	ctx, cancel := context.WithCancel(session.Context)
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = session.Styles.Loading

	ri := textinput.New()
	ri.Placeholder = "e.g. mention the config change in the body"
	ri.CharLimit = 0
	ri.Width = 80

	return &model{
		session: session, styles: session.Styles, ctx: ctx, cancel: cancel, commit: commit,
		aiClient:      aiClient,
		input:         input,
		diffSummary:   summary,
		state:         stateLoading,
		spinner:       s,
		revisionInput: ri,
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
			m.notice = ""
			switch msg.String() {
			case "y", "Y":
				m.state = stateCommitting
				return m, tea.Batch(m.spinner.Tick, m.commitChanges())
			case "e", "E":
				edit, err := newEditSession(m.ctx, "COMMIT_EDITMSG", commitEditContent(m.commitMessage))
				if err != nil {
					m.notice = fmt.Sprintf("✗ Failed to open editor: %v", err)
					return m, nil
				}
				m.state = stateEditing
				return m, tea.ExecProcess(edit.cmd, func(err error) tea.Msg { return msgEdited{edit: edit, err: err} })
			case "p", "P":
				m.revisionInput.SetValue("")
				m.state = statePrompting
				return m, m.revisionInput.Focus()
			case "n", "N", "q", "ctrl+c":
				return m, tea.Quit
			}
		case statePrompting:
			switch msg.String() {
			case "enter":
				instructions := strings.TrimSpace(m.revisionInput.Value())
				m.revisionInput.Blur()
				if instructions == "" {
					m.state = stateConfirm
					return m, nil
				}
				m.state, m.revising = stateLoading, true
				return m, tea.Batch(m.spinner.Tick, m.reviseCommitMessage(instructions))
			case "esc":
				m.revisionInput.Blur()
				m.state = stateConfirm
			default:
				m.revisionInput, cmd = m.revisionInput.Update(msg)
				return m, cmd
			}
		case stateSuccess, stateError:
			return m, tea.Quit
		}

	case msgCommitGenerated:
		m.revising = false
		if msg.err != nil && msg.revision {
			// Keep the current message so a failed revision can be retried or approved.
			m.notice = fmt.Sprintf("✗ Failed to apply the prompt; kept the previous message: %v", msg.err)
			m.state = stateConfirm
		} else if msg.err != nil {
			m.err = msg.err
			m.state = stateError
			return m, tea.Quit
		} else {
			m.commitMessage = msg.message
			m.state = stateConfirm
		}

	case msgEdited:
		m.applyEdit(msg)

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
		status := "Generating commit message..."
		if m.revising {
			status = "Applying your prompt..."
		}
		loadingText := fmt.Sprintf("%s %s", m.spinner.View(), m.styles.Loading.Render(status))

		diffSummary := m.formatDiffSummary()
		if diffSummary != "" {
			return fmt.Sprintf("%s\n\n%s", diffSummary, loadingText)
		}
		return loadingText

	case stateConfirm:
		diffSummary := m.formatDiffSummary()
		header := m.styles.Title.Render("📝 Generated Commit Message:")
		message := formatCommitMessage(m.commitMessage, m.styles)
		prompt := m.styles.Prompt.Render("Commit this message? (y)es / (e)dit / (p)rompt / (n)o")
		if m.notice != "" {
			prompt = m.styles.Error.Render(m.notice) + "\n\n" + prompt
		}

		if diffSummary != "" {
			return fmt.Sprintf("%s\n\n%s\n\n%s\n\n%s", diffSummary, header, message, prompt)
		}
		return fmt.Sprintf("%s\n\n%s\n\n%s", header, message, prompt)

	case stateEditing:
		return m.styles.EditPrompt.Render("✏️  Waiting for the editor to close...")

	case statePrompting:
		diffSummary := m.formatDiffSummary()
		header := m.styles.Title.Render("📝 Current Commit Message:")
		message := formatCommitMessage(m.commitMessage, m.styles)
		question := m.styles.Title.Render("💬 Enter a prompt to refine the commit message:")
		prompt := m.styles.EditPrompt.Render("Press Enter to send, Esc to cancel")
		view := fmt.Sprintf("%s\n\n%s\n\n%s\n%s\n\n%s", header, message, question, m.revisionInput.View(), prompt)
		if diffSummary != "" {
			return fmt.Sprintf("%s\n\n%s", diffSummary, view)
		}
		return view

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
		message, err := m.aiClient.GenerateCommitMessage(ctx, m.input)
		return msgCommitGenerated{
			message: strings.TrimSpace(message),
			err:     err,
		}
	})
}

// applyEdit keeps the current message when the editor fails or the result is empty.
func (m *model) applyEdit(msg msgEdited) {
	defer msg.edit.cleanup()
	m.state = stateConfirm
	if msg.err != nil {
		m.notice = fmt.Sprintf("✗ Editor failed; kept the previous message: %v", msg.err)
		return
	}
	content, err := msg.edit.content()
	if err != nil {
		m.notice = fmt.Sprintf("✗ %v", err)
		return
	}
	if edited := parseCommitEdit(content); edited != "" {
		m.commitMessage = edited
	} else {
		m.notice = "Empty message; kept the previous one"
	}
}

func (m *model) reviseCommitMessage(instructions string) tea.Cmd {
	previous := m.commitMessage
	return tea.Cmd(func() tea.Msg {
		message, err := m.aiClient.ReviseCommitMessage(m.ctx, m.input, previous, instructions)
		return msgCommitGenerated{message: strings.TrimSpace(message), err: err, revision: true}
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
		subject, _, _ := strings.Cut(m.commitMessage, "\n")
		message := formatSubject(subject, m.styles, m.styles.Message)

		fmt.Fprintf(m.session.Out, "%s\n%s\n", header, message)
	}

	if err != nil {
		return err
	}
	return m.err
}
