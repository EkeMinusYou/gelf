package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

const diffTruncationMarker = "\n\n[diff truncated]"

func (r *Repository) StagedDiff(ctx context.Context) (string, error) {
	out, err := r.run(ctx, "--no-pager", "diff", "--staged", "--no-ext-diff", "--no-color", "-U5", "--")
	return strings.TrimSpace(out), err
}

func (r *Repository) Commit(ctx context.Context, message string) error {
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("commit message must not be empty")
	}
	_, err := r.run(ctx, "commit", "-m", message)
	return err
}

// LimitDiff returns a diff that fits within maxBytes and reports whether it was truncated.
func LimitDiff(diff string, maxBytes int) (string, bool) {
	if maxBytes <= 0 || len(diff) <= maxBytes {
		return diff, false
	}

	if maxBytes <= len(diffTruncationMarker) {
		return validUTF8Prefix(diff, maxBytes), true
	}

	prefixLimit := maxBytes - len(diffTruncationMarker)
	prefix := diff[:prefixLimit]
	if newline := strings.LastIndexByte(prefix, '\n'); newline > 0 {
		prefix = prefix[:newline]
	}
	prefix = validUTF8Prefix(prefix, len(prefix))

	return prefix + diffTruncationMarker, true
}

func validUTF8Prefix(value string, maxBytes int) string {
	if len(value) > maxBytes {
		value = value[:maxBytes]
	}

	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}

	return value
}

type DiffSummary struct {
	Files []FileDiff
}

type FileDiff struct {
	OldName      string
	Binary       bool
	Name         string
	AddedLines   int
	DeletedLines int
}

func (r *Repository) StagedSummary(ctx context.Context) (DiffSummary, error) {
	out, err := r.run(ctx, "diff", "--staged", "--no-ext-diff", "--numstat", "-z", "--")
	if err != nil {
		return DiffSummary{}, err
	}
	return parseNumstat(out)
}

func (r *Repository) CommittedSummary(ctx context.Context, base, head string) (DiffSummary, error) {
	out, err := r.run(ctx, "diff", "--no-ext-diff", "--numstat", "-z", base+"..."+head, "--")
	if err != nil {
		return DiffSummary{}, err
	}
	return parseNumstat(out)
}

func parseNumstat(out string) (DiffSummary, error) {
	summary := DiffSummary{Files: []FileDiff{}}
	records := strings.Split(out, "\x00")
	for i := 0; i < len(records) && records[i] != ""; i++ {
		fields := strings.SplitN(records[i], "\t", 3)
		if len(fields) != 3 {
			return summary, fmt.Errorf("invalid numstat record %q", records[i])
		}
		file := FileDiff{Name: fields[2]}
		if file.Name == "" {
			if i+2 >= len(records) {
				return summary, fmt.Errorf("incomplete rename record")
			}
			file.OldName, file.Name = records[i+1], records[i+2]
			i += 2
		}
		if fields[0] == "-" && fields[1] == "-" {
			file.Binary = true
		} else {
			var err error
			file.AddedLines, err = strconv.Atoi(fields[0])
			if err != nil {
				return summary, err
			}
			file.DeletedLines, err = strconv.Atoi(fields[1])
			if err != nil {
				return summary, err
			}
		}
		summary.Files = append(summary.Files, file)
	}
	return summary, nil
}
