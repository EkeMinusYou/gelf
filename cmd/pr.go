package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/EkeMinusYou/gelf/internal/ai"
	"github.com/EkeMinusYou/gelf/internal/config"
	"github.com/EkeMinusYou/gelf/internal/git"
	"github.com/EkeMinusYou/gelf/internal/github"
	"github.com/EkeMinusYou/gelf/internal/ui"
	"github.com/spf13/cobra"
)

var prCmd = &cobra.Command{
	Use:   "pr",
	Short: "Manage pull requests",
}

var prCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a pull request with AI-generated title and description",
	RunE:  runPRCreate,
}

var (
	prDraft    bool
	prDryRun   bool
	prModel    string
	prLanguage string
	prRender   bool
	prNoRender bool
	prYes      bool
	prUpdate   bool
)

func init() {
	prCreateCmd.Flags().BoolVar(&prDraft, "draft", false, "Create the pull request as a draft")
	prCreateCmd.Flags().BoolVar(&prDryRun, "dry-run", false, "Print the generated title and body without creating a pull request")
	prCreateCmd.Flags().StringVar(&prModel, "model", "", "Override default model for PR generation")
	prCreateCmd.Flags().StringVar(&prLanguage, "language", "", "Language for PR generation (e.g., english, japanese)")
	prCreateCmd.Flags().BoolVar(&prRender, "render", true, "Render pull request markdown body")
	prCreateCmd.Flags().BoolVar(&prNoRender, "no-render", false, "Disable markdown rendering in dry-run output")
	prCreateCmd.Flags().BoolVar(&prYes, "yes", false, "Automatically approve PR creation without confirmation")
	prCreateCmd.Flags().BoolVar(&prUpdate, "update", false, "Update existing pull request when one already exists")

	prCmd.AddCommand(prCreateCmd)
}

func runPRCreate(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	if prLanguage != "" {
		cfg.PRLanguage = prLanguage
	}

	if prNoRender {
		prRender = false
	}

	if !cfg.UseColor() {
		ui.DisableColor()
	}

	modelToUse := cfg.PRModel
	if prModel != "" {
		modelToUse = prModel
	}
	cfg.FlashModel = cfg.ResolveModel(modelToUse)

	currentRepo, parentRepo, err := github.RepoInfoFromGHWithParent(ctx)
	if err != nil {
		return err
	}

	headBranch, err := git.GetCurrentBranch()
	if err != nil {
		return fmt.Errorf("failed to determine current branch: %w", err)
	}

	baseRepo := currentRepo
	if parentRepo != nil {
		baseRepo = parentRepo
	}

	repoFullName := fmt.Sprintf("%s/%s", baseRepo.Owner, baseRepo.Name)
	headOwners := make([]string, 0, 2)
	status, err := git.GetPushStatus(headBranch)
	if err != nil {
		return fmt.Errorf("failed to determine upstream status: %w", err)
	}

	remoteName := status.RemoteName
	if remoteName == "" {
		remoteName = "origin"
	}

	if remoteURL, err := git.GetRemoteURL(remoteName); err == nil {
		if remoteRepoInfo, err := github.RepoInfoFromRemoteURL(remoteURL); err == nil && remoteRepoInfo != nil {
			headOwners = append(headOwners, remoteRepoInfo.Owner)
		}
	}
	if currentRepo.Owner != "" {
		alreadyAdded := false
		for _, owner := range headOwners {
			if owner == currentRepo.Owner {
				alreadyAdded = true
				break
			}
		}
		if !alreadyAdded {
			headOwners = append(headOwners, currentRepo.Owner)
		}
	}
	if baseRepo.Owner != "" {
		alreadyAdded := false
		for _, owner := range headOwners {
			if owner == baseRepo.Owner {
				alreadyAdded = true
				break
			}
		}
		if !alreadyAdded {
			headOwners = append(headOwners, baseRepo.Owner)
		}
	}

	existingPR, err := github.FindPullRequest(ctx, repoFullName, headBranch, headOwners)
	if err != nil {
		return err
	}

	updateExisting := existingPR != nil && prUpdate
	if existingPR != nil && !prUpdate {
		stateLabel := existingPR.State
		if existingPR.IsDraft {
			stateLabel = "DRAFT"
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Pull request already exists for branch %s (%s): #%d %s (%s)\n", headBranch, stateLabel, existingPR.Number, existingPR.Title, existingPR.URL)
		return nil
	}

	token, err := github.AuthToken(ctx)
	if err != nil {
		return err
	}

	repoRoot, err := git.GetRepoRoot()
	if err != nil {
		return err
	}

	template, err := github.FindPullRequestTemplate(ctx, repoRoot, token, baseRepo.Owner)
	if err != nil {
		return fmt.Errorf("failed to resolve pull request template: %w", err)
	}

	baseBranch, err := git.GetDefaultBaseBranch()
	if err != nil {
		return fmt.Errorf("failed to determine base branch: %w", err)
	}

	baseRef := "origin/" + baseBranch
	commitLog, err := git.GetCommitLog(baseRef, "HEAD")
	if err != nil {
		return fmt.Errorf("failed to get commit log: %w", err)
	}
	if commitLog == "" {
		return fmt.Errorf("no commits found between %s and %s", baseRef, headBranch)
	}

	diffStat, err := git.GetCommittedDiffStat(baseRef, "HEAD")
	if err != nil {
		return fmt.Errorf("failed to get diff stat: %w", err)
	}

	diff, err := git.GetCommittedDiff(baseRef, "HEAD")
	if err != nil {
		return fmt.Errorf("failed to get diff: %w", err)
	}
	if diff == "" {
		return fmt.Errorf("no committed changes found between %s and %s", baseRef, headBranch)
	}

	if !prDryRun {
		prContext := ui.FormatPRContext(diff, commitLog)
		var shouldContinue bool
		shouldContinue, err = ensureBranchPushed(cmd, headBranch, prContext)
		if err != nil {
			return err
		}
		if !shouldContinue {
			return nil
		}
	}

	aiClient, err := ai.NewVertexAIClient(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to create AI client: %w", err)
	}

	templateContent := ""
	templatePath := ""
	templateSource := ""
	if template != nil {
		templateContent = template.Content
		templatePath = template.Path
		templateSource = template.Source
	}

	if prDryRun {
		prContent, err := aiClient.GeneratePullRequestContent(ctx, ai.PullRequestInput{
			BaseBranch: baseBranch,
			HeadBranch: headBranch,
			CommitLog:  commitLog,
			DiffStat:   diffStat,
			Diff:       diff,
			Template:   templateContent,
			Language:   cfg.PRLanguage,
		})
		if err != nil {
			return err
		}

		if templateContent != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "Using %s template: %s\n", templateSource, templatePath)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Title:\n%s\n\n", prContent.Title)
		if prRender {
			fmt.Fprintf(cmd.OutOrStdout(), "Body:\n")
			rendered, err := ui.RenderMarkdown(prContent.Body, cfg.UseColor())
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Failed to render markdown: %v\n", err)
				fmt.Fprintf(cmd.OutOrStdout(), "%s\n", prContent.Body)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\n", rendered)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Body:\n%s\n", prContent.Body)
		}
		return nil
	}

	var prContent *ai.PullRequestContent
	if prYes {
		prContent, err = aiClient.GeneratePullRequestContent(ctx, ai.PullRequestInput{
			BaseBranch: baseBranch,
			HeadBranch: headBranch,
			CommitLog:  commitLog,
			DiffStat:   diffStat,
			Diff:       diff,
			Template:   templateContent,
			Language:   cfg.PRLanguage,
		})
		if err != nil {
			return err
		}
	} else {
		confirmPrompt := "Create this pull request? (y)es / (n)o"
		if updateExisting {
			confirmPrompt = "Update this pull request? (y)es / (n)o"
		}
		prTUI := ui.NewPRTUI(aiClient, ai.PullRequestInput{
			BaseBranch: baseBranch,
			HeadBranch: headBranch,
			CommitLog:  commitLog,
			DiffStat:   diffStat,
			Diff:       diff,
			Template:   templateContent,
			Language:   cfg.PRLanguage,
		}, prRender, cfg.UseColor(), confirmPrompt)

		content, confirmed, err := prTUI.Run()
		if err != nil {
			return err
		}
		if !confirmed {
			return nil
		}
		prContent = content
	}

	if updateExisting {
		ghArgs := []string{"pr", "edit", fmt.Sprintf("%d", existingPR.Number), "--title", prContent.Title, "--body-file", "-"}

		ghCmd := exec.Command("gh", ghArgs...)
		ghCmd.Stdin = strings.NewReader(prContent.Body)
		ghOut, ghErr, err := runCommandWithSpinnerCapture(ghCmd, "Updating pull request...", cmd.ErrOrStderr())
		if err != nil {
			if strings.TrimSpace(ghOut) != "" {
				fmt.Fprint(cmd.OutOrStdout(), ghOut)
			}
			if strings.TrimSpace(ghErr) != "" {
				fmt.Fprint(cmd.ErrOrStderr(), ghErr)
			}
			return fmt.Errorf("failed to update pull request: %w", err)
		}
		successHeader := "✓ Pull request updated"
		if existingPR.Number > 0 {
			successHeader = fmt.Sprintf("✓ Pull request updated (#%d)", existingPR.Number)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", ui.RenderSuccessHeader(successHeader))
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", ui.RenderSuccessMessage(prContent.Title))
		if existingPR.URL != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\n", existingPR.URL)
		}
		return nil
	}

	ghArgs := []string{"pr", "create", "--title", prContent.Title, "--body-file", "-", "--base", baseBranch}
	if prDraft {
		ghArgs = append(ghArgs, "--draft")
	}

	ghCmd := exec.Command("gh", ghArgs...)
	ghCmd.Stdin = strings.NewReader(prContent.Body)
	ghOut, ghErr, err := runCommandWithSpinnerCapture(ghCmd, "Creating pull request...", cmd.ErrOrStderr())
	if err != nil {
		if strings.TrimSpace(ghOut) != "" {
			fmt.Fprint(cmd.OutOrStdout(), ghOut)
		}
		if strings.TrimSpace(ghErr) != "" {
			fmt.Fprint(cmd.ErrOrStderr(), ghErr)
		}
		return fmt.Errorf("failed to create pull request: %w", err)
	}

	ghOutTrim := strings.TrimSpace(ghOut)
	ghErrTrim := strings.TrimSpace(ghErr)
	combinedOutput := strings.TrimSpace(strings.Join([]string{ghOutTrim, ghErrTrim}, "\n"))
	prURL := extractFirstURL(combinedOutput)
	if prURL == "" {
		if ghOutTrim != "" {
			fmt.Fprint(cmd.OutOrStdout(), ghOut)
		}
		if ghErrTrim != "" {
			fmt.Fprint(cmd.ErrOrStderr(), ghErr)
		}
		return nil
	}

	if ghErrTrim != "" {
		errWithoutURL := strings.TrimSpace(strings.ReplaceAll(ghErrTrim, prURL, ""))
		if errWithoutURL != "" {
			fmt.Fprint(cmd.ErrOrStderr(), errWithoutURL)
		}
	}

	prNumber := pullNumberFromURL(prURL)
	successHeader := "✓ Pull request created"
	if prNumber != "" {
		successHeader = fmt.Sprintf("✓ Pull request created (#%s)", prNumber)
	}
	if prDraft {
		successHeader = fmt.Sprintf("%s (draft)", successHeader)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s\n", ui.RenderSuccessHeader(successHeader))
	fmt.Fprintf(cmd.OutOrStdout(), "%s\n", ui.RenderSuccessMessage(prContent.Title))
	fmt.Fprintf(cmd.OutOrStdout(), "%s\n", prURL)

	return nil
}

func ensureBranchPushed(cmd *cobra.Command, branch string, prContext string) (bool, error) {
	status, err := git.GetPushStatus(branch)
	if err != nil {
		return false, fmt.Errorf("failed to check if branch is pushed: %w", err)
	}
	if status.HeadPushed {
		return true, nil
	}

	remoteName := status.RemoteName
	if remoteName == "" {
		remoteName = "origin"
	}

	if strings.TrimSpace(prContext) != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s\n\n", prContext)
	}

	prompt := fmt.Sprintf("Current branch is not pushed to %s. Push now? (y)es / (n)o", remoteName)
	confirmed, err := ui.PromptYesNoStyledWithWriter(prompt, cmd.ErrOrStderr())
	if err != nil {
		return false, err
	}
	if !confirmed {
		return false, nil
	}

	args := []string{"push"}
	if !status.HasUpstream {
		args = []string{"push", "-u", remoteName, branch}
	}

	pushCmd := exec.Command("git", args...)
	var pushOutput bytes.Buffer
	pushCmd.Stdout = &pushOutput
	pushCmd.Stderr = &pushOutput
	stopSpinner := ui.StartSpinnerInline("Pushing branch...", cmd.ErrOrStderr())
	if err := pushCmd.Run(); err != nil {
		stopSpinner()
		trimmed := strings.TrimSpace(pushOutput.String())
		if trimmed == "" {
			return false, fmt.Errorf("failed to push branch: %w", err)
		}
		return false, fmt.Errorf("failed to push branch: %w\n%s", err, trimmed)
	}
	stopSpinner()

	fmt.Fprintf(cmd.OutOrStdout(), "%s\n", ui.RenderSuccessHeader("✓ Push succeeded"))

	return true, nil
}

func runCommandWithSpinner(cmd *exec.Cmd, message string, stdout, stderr io.Writer) error {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	stopSpinner := ui.StartSpinner(message, stderr)
	err := cmd.Run()
	stopSpinner()

	if outBuf.Len() > 0 {
		fmt.Fprint(stdout, outBuf.String())
	}
	if errBuf.Len() > 0 {
		fmt.Fprint(stderr, errBuf.String())
	}

	return err
}

func runCommandWithSpinnerCapture(cmd *exec.Cmd, message string, stderr io.Writer) (string, string, error) {
	if stderr == nil {
		stderr = io.Discard
	}

	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	stopSpinner := ui.StartSpinner(message, stderr)
	err := cmd.Run()
	stopSpinner()

	return outBuf.String(), errBuf.String(), err
}

func extractFirstURL(output string) string {
	re := regexp.MustCompile(`https?://\S+`)
	return re.FindString(output)
}

func pullNumberFromURL(prURL string) string {
	parsed, err := url.Parse(prURL)
	if err != nil {
		return ""
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i := 0; i+1 < len(segments); i++ {
		if segments[i] == "pull" || segments[i] == "pulls" {
			if _, err := strconv.Atoi(segments[i+1]); err == nil {
				return segments[i+1]
			}
		}
	}
	return ""
}
