package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/EkeMinusYou/gelf/internal/ai"
	"github.com/EkeMinusYou/gelf/internal/git"
	"github.com/charmbracelet/bubbles/spinner"
	"golang.org/x/term"
)

type prModel struct {
	aiClient       *ai.VertexAIClient
	input          ai.PullRequestInput
	diffSummary    git.DiffSummary
	commitLines    []string
	render         bool
	useColor       bool
	renderedBody   string
	content        *ai.PullRequestContent
	printedContext bool
}

func NewPRTUI(aiClient *ai.VertexAIClient, input ai.PullRequestInput, render bool, useColor bool) *prModel {
	diffSummary := git.ParseDiffSummary(input.Diff)
	commitLines := parseCommitLines(input.CommitLog)

	return &prModel{
		aiClient:    aiClient,
		input:       input,
		diffSummary: diffSummary,
		commitLines: commitLines,
		render:      render,
		useColor:    useColor,
	}
}

func (m *prModel) Run() (*ai.PullRequestContent, bool, error) {
	ctx := context.Background()
	loadingContext := formatPRContext(m.diffSummary, m.commitLines)
	stopSpinner := m.startLoadingIndicator(loadingContext)
	content, err := m.aiClient.GeneratePullRequestContent(ctx, m.input)
	stopSpinner()
	if err != nil {
		return nil, false, err
	}

	m.content = content

	if m.render {
		rendered, err := RenderMarkdown(content.Body, m.useColor)
		if err == nil {
			m.renderedBody = strings.TrimRight(rendered, "\n")
		}
	}

	fmt.Printf("%s\n\n", m.buildPRContent())

	confirmed, err := PromptYesNoStyled("Create this pull request? (y)es / (n)o")
	return content, confirmed, err
}

func (m *prModel) startLoadingIndicator(context string) func() {
	if !term.IsTerminal(int(os.Stderr.Fd())) {
		return func() {}
	}

	frames := spinner.Dot.Frames
	message := loadingStyle.Render("Generating pull request message...")
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)

	if strings.TrimSpace(context) != "" {
		fmt.Fprintln(os.Stderr, context)
		fmt.Fprintln(os.Stderr)
		m.printedContext = true
	}
	fmt.Fprintf(os.Stderr, "\r%s %s", frames[0], message)

	go func() {
		defer wg.Done()
		ticker := time.NewTicker(spinner.Dot.FPS)
		defer ticker.Stop()

		i := 1
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				frame := frames[i%len(frames)]
				i++
				fmt.Fprintf(os.Stderr, "\r%s %s", frame, message)
			}
		}
	}()

	return func() {
		close(done)
		wg.Wait()
		fmt.Fprint(os.Stderr, "\r\033[2K\n")
	}
}

func (m *prModel) buildPRContent() string {
	header := titleStyle.Render("📝 Generated Pull Request:")
	title := messageStyle.Render(m.content.Title)
	body := m.content.Body
	if m.render && m.renderedBody != "" {
		body = m.renderedBody
	}

	sections := []string{}
	if !m.printedContext {
		context := formatPRContext(m.diffSummary, m.commitLines)
		if context != "" {
			sections = append(sections, context)
		}
	}
	sections = append(sections, header, title, body)

	return strings.Join(sections, "\n\n")
}

func formatPRDiffSummary(summary git.DiffSummary) string {
	if len(summary.Files) == 0 {
		return ""
	}

	var parts []string
	parts = append(parts, diffStyle.Render("📄 Changed Files:"))

	for _, file := range summary.Files {
		fileName := fileStyle.Render(file.Name)

		var changes []string
		if file.AddedLines > 0 {
			changes = append(changes, addedStyle.Render(fmt.Sprintf("+%d", file.AddedLines)))
		}
		if file.DeletedLines > 0 {
			changes = append(changes, deletedStyle.Render(fmt.Sprintf("-%d", file.DeletedLines)))
		}

		if len(changes) > 0 {
			parts = append(parts, fmt.Sprintf(" • %s (%s)", fileName, strings.Join(changes, ", ")))
		} else {
			parts = append(parts, fmt.Sprintf(" • %s", fileName))
		}
	}

	return strings.Join(parts, "\n")
}

func formatPRContext(summary git.DiffSummary, commitLines []string) string {
	sections := []string{}

	diffSummary := formatPRDiffSummary(summary)
	if diffSummary != "" {
		sections = append(sections, diffSummary)
	}

	if len(commitLines) > 0 {
		sections = append(sections, formatPRCommitLog(commitLines))
	}

	return strings.Join(sections, "\n\n")
}

func formatPRCommitLog(commitLines []string) string {
	parts := []string{diffStyle.Render("🧾 Commits:")}
	for _, line := range commitLines {
		parts = append(parts, fmt.Sprintf(" • %s", line))
	}
	return strings.Join(parts, "\n")
}

func parseCommitLines(log string) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func FormatPRContext(diff string, commitLog string) string {
	diffSummary := git.ParseDiffSummary(diff)
	commitLines := parseCommitLines(commitLog)
	return formatPRContext(diffSummary, commitLines)
}
