package git

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLimitDiff(t *testing.T) {
	const diffTruncationMarkerLength = len(diffTruncationMarker)

	tests := []struct {
		name      string
		diff      string
		maxBytes  int
		truncated bool
	}{
		{
			name:      "keeps diff below limit",
			diff:      "diff --git a/file b/file",
			maxBytes:  100,
			truncated: false,
		},
		{
			name:      "does not truncate when limit is disabled",
			diff:      "large diff",
			maxBytes:  0,
			truncated: false,
		},
		{
			name:      "truncates at a line boundary",
			diff:      "diff --git a/file b/file\n" + strings.Repeat("+line\n", 20),
			maxBytes:  len("diff --git a/file b/file\n"+strings.Repeat("+line\n", 2)) + diffTruncationMarkerLength,
			truncated: true,
		},
		{
			name:      "does not split UTF-8 characters",
			diff:      strings.Repeat("日本語", 20),
			maxBytes:  40,
			truncated: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limited, truncated := LimitDiff(test.diff, test.maxBytes)

			if truncated != test.truncated {
				t.Fatalf("truncated = %v, want %v", truncated, test.truncated)
			}
			if !truncated && limited != test.diff {
				t.Fatalf("diff changed without truncation: %q", limited)
			}
			if test.truncated && len(limited) > test.maxBytes {
				t.Fatalf("limited diff is %d bytes, want at most %d", len(limited), test.maxBytes)
			}
			if test.truncated && !utf8.ValidString(limited) {
				t.Fatal("limited diff is not valid UTF-8")
			}
			if test.name == "truncates at a line boundary" {
				if !strings.HasSuffix(limited, diffTruncationMarker) {
					t.Fatalf("limited diff does not have truncation marker: %q", limited)
				}
				if strings.Contains(limited, "+line\n+line\n+line\n") {
					t.Fatalf("limited diff contains a line beyond the limit: %q", limited)
				}
			}
		})
	}
}
