package ui

import (
	"fmt"
	"strings"
)

// RenderSuccessHeader applies success styling to a header line.
func RenderSuccessHeader(text string) string {
	return successStyle.Render(text)
}

// RenderSuccessMessage applies success styling to a message line.
func RenderSuccessMessage(text string) string {
	return messageStyle.Render(text)
}

// RenderPRSuccess renders a pull request success block: the header and title
// share the first line (styled distinctly), followed by an indented link line
// when a URL is available.
func RenderPRSuccess(header, title, suffix, url string) string {
	line := fmt.Sprintf("%s %s", successStyle.Render(header+":"), messageStyle.Render(title))
	if suffix != "" {
		line = fmt.Sprintf("%s %s", line, subtleStyle.Render(suffix))
	}
	if strings.TrimSpace(url) != "" {
		line += fmt.Sprintf("\n  🔗 %s", urlStyle.Render(url))
	}
	return line
}
