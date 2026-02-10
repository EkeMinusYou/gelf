package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
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
