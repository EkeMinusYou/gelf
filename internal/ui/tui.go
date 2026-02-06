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

// TUI styles shared across commands.
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("6"))

	messageStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("15")).
			Bold(true).
			Italic(true)

	promptStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("4")).
			Bold(true)

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2")).
			Bold(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1")).
			Bold(true)

	loadingStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")).
			Bold(true)

	editPromptStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("3")).
			Bold(true)

	diffStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("7"))

	fileStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("5")).
			Bold(true)

	addedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2"))

	deletedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1"))
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
}
