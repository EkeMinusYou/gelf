package ui

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"golang.org/x/term"
)

// StartSpinner renders a simple loading spinner on the given writer.
// It returns a stop function that clears the line and prints a newline.
func StartSpinner(message string, out io.Writer) func() {
	return startSpinner(message, out, true)
}

// StartSpinnerInline renders a simple loading spinner on the given writer.
// It returns a stop function that clears the line without printing a newline.
func StartSpinnerInline(message string, out io.Writer) func() {
	return startSpinner(message, out, false)
}

func startSpinner(message string, out io.Writer, newline bool) func() {
	if out == nil {
		out = os.Stderr
	}
	if !isTerminalWriter(out) {
		return func() {}
	}

	frames := spinner.Dot.Frames
	styled := loadingStyle.Render(message)
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)

	fmt.Fprintf(out, "\r%s %s", frames[0], styled)

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
				fmt.Fprintf(out, "\r%s %s", frame, styled)
			}
		}
	}()

	return func() {
		close(done)
		wg.Wait()
		if newline {
			fmt.Fprint(out, "\r\033[2K\n")
			return
		}
		fmt.Fprint(out, "\r\033[2K")
	}
}

func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
