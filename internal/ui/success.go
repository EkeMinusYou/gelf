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

// RenderPRSuccess renders a pull request success block: a header line, the PR
// title on its own line, and a link line when a URL is available. The title is
// indented so its text lines up with the header and URL text after their
// single-width leading symbols.
func RenderPRSuccess(header, title, suffix, url string) string {
	headerLine := successStyle.Render(header)
	if suffix != "" {
		headerLine = fmt.Sprintf("%s %s", headerLine, subtleStyle.Render(suffix))
	}
	lines := []string{headerLine, "  " + messageStyle.Render(title)}
	if strings.TrimSpace(url) != "" {
		lines = append(lines, fmt.Sprintf("↳ %s", urlStyle.Render(url)))
	}
	return strings.Join(lines, "\n")
}
