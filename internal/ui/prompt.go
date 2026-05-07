package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

type PRChoice int

const (
	PRChoiceNone PRChoice = iota
	PRChoiceYes
	PRChoiceRevise
	PRChoiceNo
)

func PromptYesNoStyled(prompt string) (bool, error) {
	return PromptYesNoStyledWithWriter(prompt, os.Stdout)
}

func PromptYesNo(prompt string) (bool, error) {
	return PromptYesNoWithWriter(prompt, os.Stdout)
}

func PromptYesNoStyledWithWriter(prompt string, out io.Writer) (bool, error) {
	return PromptYesNoWithWriter(promptStyle.Render(prompt), out)
}

func PromptYesNoWithWriter(prompt string, out io.Writer) (bool, error) {
	if out == nil {
		out = os.Stdout
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		m := &yesNoModel{prompt: prompt}
		p := tea.NewProgram(m, tea.WithOutput(out))
		if _, err := p.Run(); err != nil {
			return false, err
		}
		return m.confirmed, nil
	}

	fmt.Fprintf(out, "%s ", prompt)

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}

	line = strings.TrimSpace(line)
	if strings.HasPrefix(strings.ToLower(line), "y") {
		return true, nil
	}
	return false, nil
}

type yesNoModel struct {
	prompt    string
	confirmed bool
}

func (m *yesNoModel) Init() tea.Cmd {
	return nil
}

func (m *yesNoModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "y", "Y":
			m.confirmed = true
			return m, tea.Quit
		case "n", "N", "q", "Q", "ctrl+c", "ctrl+d", "esc":
			m.confirmed = false
			return m, tea.Quit
		}
	}

	return m, nil
}

func (m *yesNoModel) View() string {
	return fmt.Sprintf("%s ", m.prompt)
}

// PromptPRChoiceStyled prompts the user to choose between yes / revise / no
// for the PR creation flow. The prompt is rendered with the standard prompt
// style.
func PromptPRChoiceStyled(prompt string) (PRChoice, error) {
	return promptPRChoiceWithWriter(promptStyle.Render(prompt), os.Stdout)
}

func promptPRChoiceWithWriter(prompt string, out io.Writer) (PRChoice, error) {
	if out == nil {
		out = os.Stdout
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		m := &prChoiceModel{prompt: prompt}
		p := tea.NewProgram(m, tea.WithOutput(out))
		if _, err := p.Run(); err != nil {
			return PRChoiceNone, err
		}
		return m.choice, nil
	}

	fmt.Fprintf(out, "%s ", prompt)

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return PRChoiceNone, err
	}

	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return PRChoiceYes, nil
	case "r", "revise":
		return PRChoiceRevise, nil
	default:
		return PRChoiceNo, nil
	}
}

type prChoiceModel struct {
	prompt string
	choice PRChoice
}

func (m *prChoiceModel) Init() tea.Cmd {
	return nil
}

func (m *prChoiceModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "y", "Y":
			m.choice = PRChoiceYes
			return m, tea.Quit
		case "r", "R":
			m.choice = PRChoiceRevise
			return m, tea.Quit
		case "n", "N", "q", "Q", "ctrl+c", "ctrl+d", "esc":
			m.choice = PRChoiceNo
			return m, tea.Quit
		}
	}

	return m, nil
}

func (m *prChoiceModel) View() string {
	return fmt.Sprintf("%s ", m.prompt)
}

// PromptLineStyled prompts the user for a single-line text input. Returns the
// trimmed entered text. If the user cancels (esc / ctrl+c), the second return
// value is false.
func PromptLineStyled(prompt, placeholder string) (string, bool, error) {
	return promptLineWithWriter(promptStyle.Render(prompt), placeholder, os.Stdout)
}

func promptLineWithWriter(prompt, placeholder string, out io.Writer) (string, bool, error) {
	if out == nil {
		out = os.Stdout
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		ti := textinput.New()
		ti.Placeholder = placeholder
		ti.CharLimit = 0
		ti.Width = 80
		ti.Focus()

		m := &lineInputModel{prompt: prompt, input: ti}
		p := tea.NewProgram(m, tea.WithOutput(out))
		if _, err := p.Run(); err != nil {
			return "", false, err
		}
		if !m.submitted {
			return "", false, nil
		}
		return strings.TrimSpace(m.input.Value()), true, nil
	}

	fmt.Fprintf(out, "%s ", prompt)

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", false, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", false, nil
	}
	return line, true, nil
}

type lineInputModel struct {
	prompt    string
	input     textinput.Model
	submitted bool
}

func (m *lineInputModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m *lineInputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			if strings.TrimSpace(m.input.Value()) == "" {
				return m, nil
			}
			m.submitted = true
			return m, tea.Quit
		case "esc", "ctrl+c", "ctrl+d":
			m.submitted = false
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *lineInputModel) View() string {
	hint := editPromptStyle.Render("(Enter to submit, Esc to cancel)")
	return fmt.Sprintf("%s\n%s\n%s", m.prompt, m.input.View(), hint)
}
