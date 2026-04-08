package ui

import "charm.land/glamour/v2"

func RenderMarkdown(markdown string, useColor bool) (string, error) {
	opts := []glamour.TermRendererOption{
		glamour.WithWordWrap(0),
	}

	if useColor {
		opts = append(opts, glamour.WithEnvironmentConfig())
	} else {
		opts = append(opts, glamour.WithStandardStyle("ascii"))
	}

	renderer, err := glamour.NewTermRenderer(opts...)
	if err != nil {
		return "", err
	}

	return renderer.Render(markdown)
}
