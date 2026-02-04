package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

func PromptYesNoStyled(prompt string) (bool, error) {
	return PromptYesNo(promptStyle.Render(prompt))
}

func PromptYesNo(prompt string) (bool, error) {
	fmt.Printf("%s ", prompt)

	if term.IsTerminal(int(os.Stdin.Fd())) {
		oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err == nil {
			defer func() {
				_ = term.Restore(int(os.Stdin.Fd()), oldState)
			}()

			reader := bufio.NewReader(os.Stdin)
			for {
				b, err := reader.ReadByte()
				if err != nil {
					return false, err
				}

				switch b {
				case '\r', '\n':
					continue
				}

				ch := strings.ToLower(string(b))
				if ch == "y" {
					fmt.Println()
					return true, nil
				}
				if ch == "n" {
					fmt.Println()
					return false, nil
				}
			}
		}
	}

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
