package ui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EkeMinusYou/gelf/internal/ai"
	"github.com/EkeMinusYou/gelf/internal/git"
	tea "github.com/charmbracelet/bubbletea"
)

type fakeAI struct {
	commitError  error
	reviseError  error
	instructions string
	revisedFrom  string
	input        ai.PullRequestInput
}

func (f *fakeAI) GenerateCommitMessage(ctx context.Context, input ai.CommitInput) (string, error) {
	return "fix: test", f.commitError
}
func (f *fakeAI) ReviseCommitMessage(ctx context.Context, input ai.CommitInput, previous, instructions string) (string, error) {
	f.revisedFrom, f.instructions = previous, instructions
	return "fix: revised\n\n- body", f.reviseError
}
func (f *fakeAI) GeneratePullRequestContent(ctx context.Context, input ai.PullRequestInput) (*ai.PullRequestContent, error) {
	f.input = input
	return &ai.PullRequestContent{Title: "original", Body: "original body"}, nil
}
func (f *fakeAI) RevisePullRequestContent(ctx context.Context, input ai.PullRequestInput, previous *ai.PullRequestContent, instructions string) (*ai.PullRequestContent, error) {
	f.instructions = instructions
	return &ai.PullRequestContent{Title: "revised", Body: "revised body"}, nil
}

func TestPipedPRRevisionKeepsAllInputAndSummary(t *testing.T) {
	var out bytes.Buffer
	session := NewSession(context.Background(), strings.NewReader("p\nshorten title\ny\n"), &out, io.Discard, false, false)
	client := &fakeAI{}
	summary := git.DiffSummary{Files: []git.FileDiff{{Name: "first.txt"}, {Name: "日本語.txt", AddedLines: 2}}}
	tui := NewPRTUI(session, client, ai.PullRequestInput{Diff: "truncated diff"}, summary, false, "")
	content, confirmed, err := tui.Run()
	if err != nil || !confirmed || content.Title != "revised" || client.instructions != "shorten title" || !strings.Contains(out.String(), "日本語.txt (+2)") {
		t.Fatalf("content=%+v confirmed=%v err=%v instructions=%q output=%q", content, confirmed, err, client.instructions, out.String())
	}
}

func TestPromptSequenceAndEOF(t *testing.T) {
	session := NewSession(context.Background(), strings.NewReader("yes\np\nrevision\ny\n"), io.Discard, io.Discard, false, false)
	if ok, err := session.YesNo("push"); err != nil || !ok {
		t.Fatalf("push=%v %v", ok, err)
	}
	if choice, err := session.PRChoice("choice"); err != nil || choice != PRChoicePrompt {
		t.Fatalf("choice=%v %v", choice, err)
	}
	if line, ok, err := session.Line("revision", ""); err != nil || !ok || line != "revision" {
		t.Fatalf("line=%q %v %v", line, ok, err)
	}
	if choice, err := session.PRChoice("choice"); err != nil || choice != PRChoiceYes {
		t.Fatalf("choice=%v %v", choice, err)
	}
	if ok, err := session.YesNo("EOF"); err != nil || ok {
		t.Fatalf("EOF=%v %v", ok, err)
	}
}

func TestTUIPropagatesGenerationAndCommitErrors(t *testing.T) {
	failure := errors.New("expected failure")
	for _, generation := range []bool{true, false} {
		t.Run(map[bool]string{true: "generation", false: "commit"}[generation], func(t *testing.T) {
			client := &fakeAI{}
			if generation {
				client.commitError = failure
			}
			session := NewSession(context.Background(), strings.NewReader(""), io.Discard, io.Discard, false, false)
			tui := NewTUI(session, client, ai.CommitInput{Diff: "diff", Language: "english"}, git.DiffSummary{}, func(context.Context, string) error { return failure })
			filter := tea.WithFilter(func(model tea.Model, msg tea.Msg) tea.Msg {
				if generated, ok := msg.(msgCommitGenerated); ok && generated.err == nil {
					// Deliver approval after the generated message, without needing a real TTY.
					model.Update(msg)
					return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}
				}
				return msg
			})
			err := tui.Run(tea.WithoutRenderer(), filter)
			if !errors.Is(err, failure) {
				t.Fatalf("TUI swallowed %v error: %v", generation, err)
			}
		})
	}
}

// fakeEditor makes git and gelf use a script that runs script against the edited file.
func fakeEditor(t *testing.T, script string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "editor")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_EDITOR", path)
}

func TestTUIEditingAppliesEditorResult(t *testing.T) {
	session := NewSession(context.Background(), strings.NewReader(""), io.Discard, io.Discard, false, false)
	tui := NewTUI(session, &fakeAI{}, ai.CommitInput{Diff: "truncated", Language: "english"}, git.DiffSummary{Files: []git.FileDiff{{Name: "later.txt", AddedLines: 3}}}, nil)
	defer tui.cancel()
	tui.Update(msgCommitGenerated{message: "fix: original"})
	if !strings.Contains(tui.View(), "later.txt (+3)") {
		t.Fatal("full summary missing")
	}
	edit := func(script string, runErr error) {
		t.Helper()
		fakeEditor(t, script)
		session, err := newEditSession(context.Background(), "COMMIT_EDITMSG", commitEditContent(tui.commitMessage))
		if err != nil {
			t.Fatal(err)
		}
		if err := session.cmd.Run(); err != nil {
			t.Fatal(err)
		}
		tui.state = stateEditing
		tui.Update(msgEdited{edit: session, err: runErr})
		if tui.state != stateConfirm {
			t.Fatalf("state=%v", tui.state)
		}
		if _, err := os.Stat(session.dir); !os.IsNotExist(err) {
			t.Fatal("temporary file was not removed")
		}
	}
	// The editor sees the message with git-style help comments, which are stripped.
	edit(`grep -q "^# Lines starting with '#' are ignored" "$1" && printf 'feat: edited\n\n- body  \n# note\n' > "$1"`, nil)
	if tui.commitMessage != "feat: edited\n\n- body" {
		t.Fatalf("edited=%q", tui.commitMessage)
	}
	edit(`printf '# only comments\n\n' > "$1"`, nil)
	if tui.commitMessage != "feat: edited\n\n- body" || !strings.Contains(tui.View(), "kept the previous one") {
		t.Fatalf("empty edit replaced the message: %q", tui.commitMessage)
	}
	edit(`printf 'fix: ignored\n' > "$1"`, errors.New("exit status 1"))
	if tui.commitMessage != "feat: edited\n\n- body" || !strings.Contains(tui.View(), "Editor failed") {
		t.Fatalf("failed editor replaced the message: %q", tui.commitMessage)
	}
}

func TestPREditParsing(t *testing.T) {
	original := &ai.PullRequestContent{Title: "feat: title", Body: "## Summary\n\nbody"}
	parsed, err := parsePREdit(prEditContent(original))
	if err != nil || *parsed != *original {
		t.Fatalf("round trip=%+v %v", parsed, err)
	}
	for _, content := range []string{"", "title only\n", "\n\n"} {
		if _, err := parsePREdit(content); err == nil {
			t.Fatalf("invalid edit accepted: %q", content)
		}
	}
	if parsed, err := parsePREdit("\n\nnew title\nbody"); err != nil || parsed.Title != "new title" || parsed.Body != "body" {
		t.Fatalf("leading blank lines: %+v %v", parsed, err)
	}
}

func TestPipedPREditRequiresTerminal(t *testing.T) {
	var stderr bytes.Buffer
	session := NewSession(context.Background(), strings.NewReader("e\ny\n"), io.Discard, &stderr, false, false)
	content, confirmed, err := NewPRTUI(session, &fakeAI{}, ai.PullRequestInput{}, git.DiffSummary{}, false, "").Run()
	if err != nil || !confirmed || content.Title != "original" || !strings.Contains(stderr.String(), "interactive terminal") {
		t.Fatalf("content=%+v confirmed=%v err=%v stderr=%q", content, confirmed, err, stderr.String())
	}
}

func TestCommitTypeColors(t *testing.T) {
	colored, plain := NewStyles(true), NewStyles(false)
	feat := formatSubject("feat(ui)!: add view", colored, colored.Message)
	fix := formatSubject("fix: bug", colored, colored.Message)
	if !strings.Contains(feat, "feat(ui)!:") || !strings.Contains(feat, "\x1b[") || strings.SplitN(feat, "feat", 2)[0] == strings.SplitN(fix, "fix", 2)[0] {
		t.Fatalf("feat and fix should have distinct colors: %q %q", feat, fix)
	}
	if got := formatSubject("feat(ui): add view", plain, plain.Message); got != "feat(ui): add view" {
		t.Fatalf("plain output styled: %q", got)
	}
	if got := formatCommitMessage("unknown: x\n\nbody", plain); got != "unknown: x\n\nbody" {
		t.Fatalf("plain message changed: %q", got)
	}
}

func TestTUIRevisionKeepsMessageOnFailure(t *testing.T) {
	session := NewSession(context.Background(), strings.NewReader(""), io.Discard, io.Discard, false, false)
	client := &fakeAI{}
	tui := NewTUI(session, client, ai.CommitInput{Diff: "diff"}, git.DiffSummary{}, nil)
	defer tui.cancel()
	tui.Update(msgCommitGenerated{message: "fix: original"})
	tui.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	tui.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if tui.state != stateConfirm {
		t.Fatal("empty instructions should return to confirmation")
	}
	tui.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	tui.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("add a body")})
	_, cmd := tui.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if tui.state != stateLoading || !strings.Contains(tui.View(), "Applying your prompt") || cmd == nil {
		t.Fatalf("revision did not start: state=%v", tui.state)
	}
	tui.Update(tui.reviseCommitMessage("add a body")())
	if tui.commitMessage != "fix: revised\n\n- body" || tui.state != stateConfirm || client.revisedFrom != "fix: original" || client.instructions != "add a body" {
		t.Fatalf("revision=%q state=%v client=%+v", tui.commitMessage, tui.state, client)
	}
	client.reviseError = errors.New("quota")
	tui.Update(tui.reviseCommitMessage("again")())
	if tui.commitMessage != "fix: revised\n\n- body" || tui.state != stateConfirm || !strings.Contains(tui.View(), "quota") {
		t.Fatalf("failed revision should keep the message: %q state=%v", tui.commitMessage, tui.state)
	}
	tui.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if strings.Contains(tui.View(), "quota") {
		t.Fatal("revision error should clear on the next key")
	}
}

func TestTUISuccessShowsOnlySubject(t *testing.T) {
	var out bytes.Buffer
	session := NewSession(context.Background(), strings.NewReader(""), &out, io.Discard, false, false)
	var committed string
	tui := NewTUI(session, &fakeAI{}, ai.CommitInput{}, git.DiffSummary{}, func(_ context.Context, message string) error { committed = message; return nil })
	filter := tea.WithFilter(func(model tea.Model, msg tea.Msg) tea.Msg {
		if _, ok := msg.(msgCommitGenerated); ok {
			model.Update(msgCommitGenerated{message: "fix: subject\n\n- body"})
			return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}
		}
		return msg
	})
	if err := tui.Run(tea.WithoutRenderer(), filter); err != nil {
		t.Fatal(err)
	}
	if committed != "fix: subject\n\n- body" || !strings.Contains(out.String(), "fix: subject") || strings.Contains(out.String(), "- body") {
		t.Fatalf("committed=%q output=%q", committed, out.String())
	}
}

func TestPromptCancellationWhileWaiting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	session := NewSession(ctx, input, io.Discard, io.Discard, false, false)
	done := make(chan error, 1)
	go func() { _, err := session.YesNo("push"); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("prompt ignored cancellation")
	}
}

func TestColorStylesAreIndependentAndNeverIsPlain(t *testing.T) {
	colored := NewStyles(true)
	plain := NewStyles(false)
	if !strings.Contains(colored.Success.Render("success"), "\x1b[") {
		t.Fatal("always did not enable color")
	}
	if strings.Contains(plain.Success.Render("success"), "\x1b[") {
		t.Fatal("never added terminal styling")
	}
	if !strings.Contains(colored.Success.Render("success"), "\x1b[") {
		t.Fatal("creating plain styles mutated another session")
	}
}

type blockingAI struct {
	fakeAI
	started, finished chan struct{}
}

func (b *blockingAI) GenerateCommitMessage(ctx context.Context, input ai.CommitInput) (string, error) {
	close(b.started)
	<-ctx.Done()
	close(b.finished)
	return "", ctx.Err()
}

func TestTUIQuitCancelsPendingAI(t *testing.T) {
	client := &blockingAI{started: make(chan struct{}), finished: make(chan struct{})}
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	go func() { <-client.started; _, _ = io.WriteString(writer, "q") }()
	session := NewSession(context.Background(), input, io.Discard, io.Discard, false, false)
	tui := NewTUI(session, client, ai.CommitInput{Diff: "diff", Language: "english"}, git.DiffSummary{}, nil)
	if err := tui.Run(tea.WithoutRenderer()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.finished:
	case <-time.After(time.Second):
		t.Fatal("AI request was not canceled after quitting")
	}
}
