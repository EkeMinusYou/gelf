package ui

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type PRChoice int

const (
	PRChoiceNone PRChoice = iota
	PRChoiceYes
	PRChoicePrompt
	PRChoiceEdit
	PRChoiceNo
)

func (s *Session) YesNo(prompt string) (bool, error) {
	prompt = s.ErrStyles.Prompt.Render(prompt)
	if s.terminalInput() {
		m := &yesNoModel{prompt: prompt}
		p := tea.NewProgram(m, tea.WithContext(s.Context), tea.WithInput(s.In), tea.WithOutput(s.Err))
		if _, err := p.Run(); err != nil {
			return false, err
		}
		return m.confirmed, nil
	}
	if _, err := fmt.Fprintf(s.Err, "%s ", prompt); err != nil {
		return false, err
	}
	line, err := s.readLine()
	if err != nil {
		return false, err
	}
	line = strings.ToLower(line)
	return line == "y" || line == "yes", nil
}

func (s *Session) readLine() (string, error) {
	if err := s.Context.Err(); err != nil {
		return "", err
	}
	type result struct {
		line string
		err  error
	}
	completed := make(chan result, 1)
	go func() {
		line, err := s.reader.ReadString('\n')
		if err == io.EOF {
			err = nil
		}
		completed <- result{line: strings.TrimSpace(line), err: err}
	}()
	select {
	case <-s.Context.Done():
		return "", s.Context.Err()
	case read := <-completed:
		return read.line, read.err
	}
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

func (s *Session) PRChoice(prompt string) (PRChoice, error) {
	prompt = s.Styles.Prompt.Render(prompt)
	if s.terminalInput() {
		m := &prChoiceModel{prompt: prompt}
		p := tea.NewProgram(m, tea.WithContext(s.Context), tea.WithInput(s.In), tea.WithOutput(s.Out))
		if _, err := p.Run(); err != nil {
			return PRChoiceNone, err
		}
		return m.choice, nil
	}
	if _, err := fmt.Fprintf(s.Out, "%s ", prompt); err != nil {
		return PRChoiceNone, err
	}
	line, err := s.readLine()
	if err != nil {
		return PRChoiceNone, err
	}
	switch strings.ToLower(line) {
	case "y", "yes":
		return PRChoiceYes, nil
	case "p", "prompt":
		return PRChoicePrompt, nil
	case "e", "edit":
		return PRChoiceEdit, nil
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
		case "p", "P":
			m.choice = PRChoicePrompt
			return m, tea.Quit
		case "e", "E":
			m.choice = PRChoiceEdit
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

func (s *Session) Line(prompt, placeholder string) (string, bool, error) {
	prompt = s.Styles.Prompt.Render(prompt)
	if s.terminalInput() {
		ti := textinput.New()
		ti.Placeholder, ti.CharLimit, ti.Width = placeholder, 0, 80
		ti.Focus()
		m := &lineInputModel{prompt: prompt, input: ti, styles: s.Styles}
		p := tea.NewProgram(m, tea.WithContext(s.Context), tea.WithInput(s.In), tea.WithOutput(s.Out))
		if _, err := p.Run(); err != nil {
			return "", false, err
		}
		return strings.TrimSpace(m.input.Value()), m.submitted, nil
	}
	if _, err := fmt.Fprintf(s.Out, "%s ", prompt); err != nil {
		return "", false, err
	}
	line, err := s.readLine()
	return line, line != "", err
}

type lineInputModel struct {
	styles    Styles
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
	hint := m.styles.EditPrompt.Render("(Enter to submit, Esc to cancel)")
	return fmt.Sprintf("%s\n%s\n%s", m.prompt, m.input.View(), hint)
}
