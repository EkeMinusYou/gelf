package ui

import (
	"fmt"
	"strings"
)

// RenderPRSuccess renders a pull request success block: a header line, the PR
// title on its own line, and a link line when a URL is available. The title is
// indented so its text lines up with the header and URL text after their
// single-width leading symbols.
func (s *Session) RenderPRSuccess(header, title, suffix, url string) string {
	headerLine := s.Styles.Success.Render(header)
	if suffix != "" {
		headerLine = fmt.Sprintf("%s %s", headerLine, s.Styles.Subtle.Render(suffix))
	}
	lines := []string{headerLine, "  " + s.Styles.Message.Render(title)}
	if strings.TrimSpace(url) != "" {
		lines = append(lines, fmt.Sprintf("↳ %s", s.Styles.URL.Render(url)))
	}
	return strings.Join(lines, "\n")
}
