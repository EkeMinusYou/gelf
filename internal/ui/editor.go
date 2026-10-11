package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/EkeMinusYou/gelf/internal/ai"
)

const commitEditHelp = `
# Edit the commit message, then save and quit the editor.
# Lines starting with '#' are ignored. An empty message keeps the previous one.
`

// editSession is a temporary file opened in the user's editor.
type editSession struct {
	dir, path string
	cmd       *exec.Cmd
}

// newEditSession writes content to a temporary file called name and prepares the
// editor git would use. The file name lets editors pick a filetype (e.g. gitcommit).
func newEditSession(ctx context.Context, name, content string) (*editSession, error) {
	dir, err := os.MkdirTemp("", "gelf-edit-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary directory: %w", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("failed to write temporary file: %w", err)
	}
	editor := resolveEditor(ctx)
	// Run through the shell like git does, so editors with arguments (e.g. "code --wait") work.
	cmd := exec.CommandContext(ctx, "sh", "-c", editor+` "$@"`, editor, path)
	return &editSession{dir: dir, path: path, cmd: cmd}, nil
}

func (e *editSession) content() (string, error) {
	data, err := os.ReadFile(e.path)
	if err != nil {
		return "", fmt.Errorf("failed to read edited file: %w", err)
	}
	return string(data), nil
}

func (e *editSession) cleanup() {
	_ = os.RemoveAll(e.dir)
}

// resolveEditor follows git's order: GIT_EDITOR, core.editor, VISUAL, EDITOR, then vi.
func resolveEditor(ctx context.Context) string {
	if out, err := exec.CommandContext(ctx, "git", "var", "GIT_EDITOR").Output(); err == nil {
		if editor := strings.TrimSpace(string(out)); editor != "" {
			return editor
		}
	}
	for _, name := range []string{"GIT_EDITOR", "VISUAL", "EDITOR"} {
		if editor := strings.TrimSpace(os.Getenv(name)); editor != "" {
			return editor
		}
	}
	return "vi"
}

func commitEditContent(message string) string {
	return message + "\n" + commitEditHelp
}

// parseCommitEdit drops comment lines and surrounding blank lines, like git's default cleanup.
func parseCommitEdit(content string) string {
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, strings.TrimRight(line, " \t\r"))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func prEditContent(content *ai.PullRequestContent) string {
	return content.Title + "\n\n" + content.Body + "\n"
}

// parsePREdit reads the first line as the title and the rest as the body.
func parsePREdit(content string) (*ai.PullRequestContent, error) {
	title, body, _ := strings.Cut(strings.TrimLeft(content, "\r\n"), "\n")
	result := &ai.PullRequestContent{Title: strings.TrimSpace(title), Body: strings.TrimSpace(body)}
	if result.Title == "" || result.Body == "" {
		return nil, fmt.Errorf("the first line must be the title, followed by the body")
	}
	return result, nil
}
