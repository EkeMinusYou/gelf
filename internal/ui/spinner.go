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

func (s *Session) Spinner(message string, inline bool) func() {
	return startSpinner(message, s.Err, !inline, s.ErrStyles)
}

func startSpinner(message string, out io.Writer, newline bool, styles Styles) func() {
	if out == nil {
		out = io.Discard
	}
	if !isTerminalWriter(out) {
		return func() {}
	}

	frames := spinner.Dot.Frames
	styled := styles.Loading.Render(message)
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

	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			wg.Wait()
			if newline {
				fmt.Fprint(out, "\r\033[2K\n")
				return
			}
			fmt.Fprint(out, "\r\033[2K")
		})
	}
}

func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
