package ui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/EkeMinusYou/gelf/internal/ai"
	"github.com/EkeMinusYou/gelf/internal/git"
)

func BenchmarkCommitLoadingView(b *testing.B) {
	for _, count := range []int{100, 1000} {
		b.Run(fmt.Sprintf("files=%d", count), func(b *testing.B) {
			summary := git.DiffSummary{}
			for i := 0; i < count; i++ {
				summary.Files = append(summary.Files, git.FileDiff{Name: fmt.Sprintf("internal/package/file-%04d.go", i), AddedLines: 12, DeletedLines: 3})
			}
			session := NewSession(context.Background(), strings.NewReader(""), io.Discard, io.Discard, true, true)
			model := NewTUI(session, nil, ai.CommitInput{}, summary, nil)
			b.Cleanup(model.cancel)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = model.View()
			}
		})
	}
}
