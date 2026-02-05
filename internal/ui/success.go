package ui

// RenderSuccessHeader applies success styling to a header line.
func RenderSuccessHeader(text string) string {
	return successStyle.Render(text)
}

// RenderSuccessMessage applies success styling to a message line.
func RenderSuccessMessage(text string) string {
	return messageStyle.Render(text)
}
