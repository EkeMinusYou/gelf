package ui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/EkeMinusYou/gelf/internal/ai"
	"github.com/EkeMinusYou/gelf/internal/git"
	tea "github.com/charmbracelet/bubbletea"
)

type fakeAI struct {
	commitError  error
	instructions string
	input        ai.PullRequestInput
}

func (f *fakeAI) GenerateCommitMessage(ctx context.Context, input ai.CommitInput) (string, error) {
	return "fix: test", f.commitError
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
	session := NewSession(context.Background(), strings.NewReader("r\nshorten title\ny\n"), &out, io.Discard, false, false)
	client := &fakeAI{}
	summary := git.DiffSummary{Files: []git.FileDiff{{Name: "first.txt"}, {Name: "日本語.txt", AddedLines: 2}}}
	tui := NewPRTUI(session, client, ai.PullRequestInput{Diff: "truncated diff"}, summary, false, "")
	content, confirmed, err := tui.Run()
	if err != nil || !confirmed || content.Title != "revised" || client.instructions != "shorten title" || !strings.Contains(out.String(), "日本語.txt (+2)") {
		t.Fatalf("content=%+v confirmed=%v err=%v instructions=%q output=%q", content, confirmed, err, client.instructions, out.String())
	}
}

func TestPromptSequenceAndEOF(t *testing.T) {
	session := NewSession(context.Background(), strings.NewReader("yes\nr\nrevision\ny\n"), io.Discard, io.Discard, false, false)
	if ok, err := session.YesNo("push"); err != nil || !ok {
		t.Fatalf("push=%v %v", ok, err)
	}
	if choice, err := session.PRChoice("choice"); err != nil || choice != PRChoiceRevise {
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

func TestTUIEditingCancellationAndFullSummary(t *testing.T) {
	session := NewSession(context.Background(), strings.NewReader(""), io.Discard, io.Discard, false, false)
	tui := NewTUI(session, &fakeAI{}, ai.CommitInput{Diff: "truncated", Language: "english"}, git.DiffSummary{Files: []git.FileDiff{{Name: "later.txt", AddedLines: 3}}}, nil)
	defer tui.cancel()
	tui.Update(msgCommitGenerated{message: "original"})
	tui.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	tui.textInput.SetValue("edited")
	tui.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if tui.commitMessage != "original" || tui.state != stateConfirm || !strings.Contains(tui.View(), "later.txt (+3)") {
		t.Fatal("editing or full summary incorrect")
	}
	tui.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	_, quit := tui.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if quit == nil {
		t.Fatal("Ctrl+C in editing did not quit")
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
