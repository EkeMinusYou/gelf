package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
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
)

func init() {
	prCreateCmd.Flags().BoolVar(&prDraft, "draft", false, "Create the pull request as a draft")
	prCreateCmd.Flags().BoolVar(&prDryRun, "dry-run", false, "Print the generated title and body without creating a pull request")
	prCreateCmd.Flags().StringVar(&prModel, "model", "", "Override default model for PR generation")
	prCreateCmd.Flags().StringVar(&prLanguage, "language", "", "Language for PR generation (e.g., english, japanese)")
	prCreateCmd.Flags().BoolVar(&prRender, "render", true, "Render pull request markdown body")
	prCreateCmd.Flags().BoolVar(&prNoRender, "no-render", false, "Disable markdown rendering in dry-run output")
	prCreateCmd.Flags().BoolVar(&prYes, "yes", false, "Automatically approve PR creation without confirmation")

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

	repoInfo, err := github.RepoInfoFromGH(ctx)
	if err != nil {
		return err
	}

	headBranch, err := git.GetCurrentBranch()
	if err != nil {
		return fmt.Errorf("failed to determine current branch: %w", err)
	}

	headRef := fmt.Sprintf("%s:%s", repoInfo.Owner, headBranch)
	repoFullName := fmt.Sprintf("%s/%s", repoInfo.Owner, repoInfo.Name)
	existingPR, err := github.FindOpenPullRequest(ctx, repoFullName, headRef)
	if err != nil {
		return err
	}
	if existingPR != nil {
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

	template, err := github.FindPullRequestTemplate(ctx, repoRoot, token, repoInfo.Owner)
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
		shouldContinue, contextPrinted, err = ensureBranchPushed(cmd, headBranch, prContext)
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
		prTUI := ui.NewPRTUI(aiClient, ai.PullRequestInput{
			BaseBranch: baseBranch,
			HeadBranch: headBranch,
			CommitLog:  commitLog,
			DiffStat:   diffStat,
			Diff:       diff,
			Template:   templateContent,
			Language:   cfg.PRLanguage,
		}, prRender, cfg.UseColor())

		content, confirmed, err := prTUI.Run()
		if err != nil {
			return err
		}
		if !confirmed {
			return nil
		}
		prContent = content
	}

	ghArgs := []string{"pr", "create", "--title", prContent.Title, "--body-file", "-", "--base", baseBranch}
	if prDraft {
		ghArgs = append(ghArgs, "--draft")
	}

	ghCmd := exec.Command("gh", ghArgs...)
	ghCmd.Stdin = strings.NewReader(prContent.Body)
	ghCmd.Stdout = cmd.OutOrStdout()
	ghCmd.Stderr = cmd.ErrOrStderr()
	if err := ghCmd.Run(); err != nil {
		return fmt.Errorf("failed to create pull request: %w", err)
	}

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
		fmt.Fprintln(cmd.ErrOrStderr(), prContext)
		fmt.Fprintln(cmd.ErrOrStderr())
	}

	prompt := fmt.Sprintf("Current branch is not pushed to %s. Push now? (y)es / (n)o", remoteName)
	confirmed, err := ui.PromptYesNoStyled(prompt)
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
	if err := pushCmd.Run(); err != nil {
		trimmed := strings.TrimSpace(pushOutput.String())
		if trimmed == "" {
			return false, fmt.Errorf("failed to push branch: %w", err)
		}
		return false, fmt.Errorf("failed to push branch: %w\n%s", err, trimmed)
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Push succeeded.")

	return true, nil
}
