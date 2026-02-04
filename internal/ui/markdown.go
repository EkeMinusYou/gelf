package ui

import "github.com/charmbracelet/glamour"

func RenderMarkdown(markdown string, useColor bool) (string, error) {
	opts := []glamour.TermRendererOption{
		glamour.WithWordWrap(0),
	}

	if useColor {
		opts = append(opts, glamour.WithAutoStyle())
	} else {
		opts = append(opts, glamour.WithStandardStyle("ascii"))
	}

	renderer, err := glamour.NewTermRenderer(opts...)
	if err != nil {
		return "", err
	}

	return renderer.Render(markdown)
}
