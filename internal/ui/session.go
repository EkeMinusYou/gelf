package ui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/EkeMinusYou/gelf/internal/git"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

type Styles struct {
	Title, Message, Prompt, Success, Error, Loading, EditPrompt lipgloss.Style
	Diff, File, Added, Deleted, Subtle, URL                     lipgloss.Style
	// Breaking marks breaking changes ("feat!:" or a BREAKING CHANGE footer).
	Breaking lipgloss.Style
	// CommitTypes colors Conventional Commits prefixes such as "feat(ui):".
	CommitTypes map[string]lipgloss.Style
}

func NewStyles(color bool) Styles {
	if !color {
		return Styles{}
	}
	renderer := lipgloss.NewRenderer(io.Discard)
	renderer.SetColorProfile(termenv.TrueColor)
	return Styles{
		Title:      renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("6")),
		Message:    renderer.NewStyle().Bold(true).Italic(true).Foreground(lipgloss.Color("15")),
		Prompt:     renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("4")),
		Success:    renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("2")),
		Error:      renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("1")),
		Loading:    renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("6")),
		EditPrompt: renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("3")),
		Diff:       renderer.NewStyle().Foreground(lipgloss.Color("7")),
		File:       renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("5")),
		Added:      renderer.NewStyle().Foreground(lipgloss.Color("2")),
		Deleted:    renderer.NewStyle().Foreground(lipgloss.Color("1")),
		Subtle:     renderer.NewStyle().Foreground(lipgloss.Color("8")),
		URL:        renderer.NewStyle().Underline(true).Foreground(lipgloss.Color("4")),
		Breaking:   renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("1")),
		CommitTypes: map[string]lipgloss.Style{
			"feat":     renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("2")),
			"fix":      renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("1")),
			"docs":     renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("4")),
			"style":    renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("13")),
			"refactor": renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("6")),
			"perf":     renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("3")),
			"test":     renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("5")),
			"build":    renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("12")),
			"ci":       renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("14")),
			"chore":    renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("8")),
			"revert":   renderer.NewStyle().Bold(true).Foreground(lipgloss.Color("9")),
		},
	}
}

var (
	conventionalPrefix = regexp.MustCompile(`^([A-Za-z]+)(\([^)]*\))?(!)?:`)
	breakingFooter     = regexp.MustCompile(`(?m)^BREAKING[ -]CHANGE:`)
)

// formatSubject colors a Conventional Commits prefix by type and renders the rest with rest.
func formatSubject(subject string, styles Styles, rest lipgloss.Style) string {
	return formatSubjectWith(subject, styles, rest, false)
}

// formatSubjectWith highlights the prefix as breaking when it has "!" or breaking is set.
func formatSubjectWith(subject string, styles Styles, rest lipgloss.Style, breaking bool) string {
	match := conventionalPrefix.FindStringSubmatchIndex(subject)
	if match == nil {
		return rest.Render(subject)
	}
	prefix := subject[:match[1]]
	if breaking || match[6] >= 0 {
		return styles.Breaking.Render(prefix) + rest.Render(subject[match[1]:])
	}
	style, ok := styles.CommitTypes[strings.ToLower(subject[match[2]:match[3]])]
	if !ok {
		return rest.Render(subject)
	}
	return style.Render(prefix) + rest.Render(subject[match[1]:])
}

// formatCommitMessage highlights the subject line and breaking-change footers, leaving the rest of the body plain.
func formatCommitMessage(message string, styles Styles) string {
	subject, body, hasBody := strings.Cut(message, "\n")
	breaking := hasBody && breakingFooter.MatchString(body)
	formatted := formatSubjectWith(subject, styles, styles.Message, breaking)
	if hasBody {
		formatted += "\n" + breakingFooter.ReplaceAllStringFunc(body, func(footer string) string { return styles.Breaking.Render(footer) })
	}
	return formatted
}

// FormatCommitMessage renders a commit message for this session's output.
func (s *Session) FormatCommitMessage(message string) string {
	return formatCommitMessage(message, s.Styles)
}

type Session struct {
	Context           context.Context
	In                io.Reader
	Out, Err          io.Writer
	Styles, ErrStyles Styles
	UseColor          bool
	reader            *bufio.Reader
}

func NewSession(ctx context.Context, in io.Reader, out, err io.Writer, color, errColor bool) *Session {
	return &Session{Context: ctx, In: in, Out: out, Err: err, Styles: NewStyles(color), ErrStyles: NewStyles(errColor), UseColor: color, reader: bufio.NewReader(in)}
}

func (s *Session) terminalInput() bool {
	f, ok := s.In.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func formatDiffSummary(summary git.DiffSummary, styles Styles) string {
	if len(summary.Files) == 0 {
		return ""
	}
	parts := []string{styles.Diff.Render("📄 Changed Files:")}
	for _, file := range summary.Files {
		name := displayName(file.Name)
		if file.OldName != "" {
			name = displayName(file.OldName) + " → " + name
		}
		name = styles.File.Render(name)
		var changes []string
		if file.Binary {
			changes = append(changes, "binary")
		}
		if file.AddedLines > 0 {
			changes = append(changes, styles.Added.Render(fmt.Sprintf("+%d", file.AddedLines)))
		}
		if file.DeletedLines > 0 {
			changes = append(changes, styles.Deleted.Render(fmt.Sprintf("-%d", file.DeletedLines)))
		}
		if len(changes) > 0 {
			parts = append(parts, fmt.Sprintf(" • %s (%s)", name, strings.Join(changes, ", ")))
		} else {
			parts = append(parts, " • "+name)
		}
	}
	return strings.Join(parts, "\n")
}

func displayName(name string) string {
	if strings.ContainsAny(name, "\r\n\t\x1b") {
		return strconv.Quote(name)
	}
	return name
}
