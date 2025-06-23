package git

import (
	"os/exec"
	"regexp"
	"strings"
)

func GetStagedDiff() (string, error) {
	cmd := exec.Command("git", "--no-pager", "diff", "--staged", "-U5")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

func GetUnstagedDiff() (string, error) {
	cmd := exec.Command("git", "--no-pager", "diff", "-U5")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(output)), nil
}

func CommitChanges(message string) error {
	cmd := exec.Command("git", "commit", "-m", message)
	return cmd.Run()
}

type DiffSummary struct {
	Files []FileDiff
}

type FileDiff struct {
	Name         string
	AddedLines   int
	DeletedLines int
}

func ParseDiffSummary(diff string) DiffSummary {
	summary := DiffSummary{Files: []FileDiff{}}

	fileRegex := regexp.MustCompile(`^diff --git a/(.*) b/(.*)$`)
	addedRegex := regexp.MustCompile(`^\+[^+].*$`)
	deletedRegex := regexp.MustCompile(`^-[^-].*$`)

	lines := strings.Split(diff, "\n")
	var currentFile *FileDiff

	for _, line := range lines {
		if matches := fileRegex.FindStringSubmatch(line); matches != nil {
			if currentFile != nil {
				summary.Files = append(summary.Files, *currentFile)
			}
			currentFile = &FileDiff{
				Name:         matches[1],
				AddedLines:   0,
				DeletedLines: 0,
			}
		} else if currentFile != nil {
			if addedRegex.MatchString(line) {
				currentFile.AddedLines++
			} else if deletedRegex.MatchString(line) {
				currentFile.DeletedLines++
			}
		}
	}

	if currentFile != nil {
		summary.Files = append(summary.Files, *currentFile)
	}

	return summary
}
