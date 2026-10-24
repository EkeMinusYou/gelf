package cmd

import (
	"fmt"
	"sync"

	"github.com/EkeMinusYou/gelf/internal/ai"
	"github.com/EkeMinusYou/gelf/internal/git"
	"github.com/EkeMinusYou/gelf/internal/ui"
	"github.com/spf13/cobra"
)

// recentCommitCount is how many commit subjects are given to the AI as style examples.
const recentCommitCount = 5

type commitOptions struct {
	DryRun, Quiet, Yes bool
	NoCache            bool
	Model, Language    string
}

func newCommitCommand(deps dependencies) *cobra.Command {
	var opts commitOptions
	cmd := &cobra.Command{Use: "commit", Short: "Generate and commit with an AI-powered commit message", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runCommit(cmd, opts, deps) },
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Generate message only without committing")
	cmd.Flags().BoolVar(&opts.Quiet, "quiet", false, "Don't show diff output (only with --dry-run)")
	cmd.Flags().StringVar(&opts.Model, "model", "", "Override default model for this generation")
	cmd.Flags().StringVar(&opts.Language, "language", "", "Language for commit message generation (e.g., english, japanese)")
	cmd.Flags().BoolVar(&opts.Yes, "yes", false, "Automatically approve commit message without interactive confirmation")
	cmd.Flags().BoolVar(&opts.NoCache, "no-cache", false, "Generate a fresh message without reading or writing the commit cache")
	return cmd
}

func runCommit(cmd *cobra.Command, opts commitOptions, deps dependencies) error {
	ctx := cmd.Context()
	cfg, err := deps.LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}
	session := ui.NewSession(ctx, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), cfg.UseColor(cmd.OutOrStdout()), cfg.UseColor(cmd.ErrOrStderr()))
	model, language := cfg.CommitModel, cfg.CommitLanguage
	if opts.Model != "" {
		model = cfg.ResolveModel(opts.Model)
	}
	if opts.Language != "" {
		language = opts.Language
	}
	// Keep the selected model and endpoint together without mutating shared configuration.
	resolved := *cfg
	resolved.Location = model.Location
	cfg = &resolved
	diff, err := deps.Git.StagedDiff(ctx)
	if err != nil {
		return fmt.Errorf("failed to get staged changes: %w", err)
	}
	if diff == "" {
		fmt.Fprintln(session.Err, "⚠ No staged changes found. Please stage some changes first with 'git add'.")
		if opts.DryRun {
			return fmt.Errorf("no staged changes")
		}
		return nil
	}
	// These reads and client setup are independent; keep their results separate
	// so errors retain the same precedence as the sequential workflow.
	var summary git.DiffSummary
	var diffStat, recentCommits, branch, root string
	var summaryErr, statErr, recentErr, clientErr error
	var client ai.Client
	var pending sync.WaitGroup
	needsSummary := (!opts.DryRun && !opts.Yes) || (opts.DryRun && !opts.Quiet)
	useCache := cfg.CommitCache && !opts.NoCache && deps.CommitCache != nil
	if needsSummary {
		pending.Go(func() { summary, summaryErr = deps.Git.StagedSummary(ctx) })
	}
	pending.Go(func() { diffStat, statErr = deps.Git.StagedDiffStat(ctx) })
	pending.Go(func() { recentCommits, recentErr = deps.Git.RecentCommitSubjects(ctx, recentCommitCount) })
	// Branch is optional context, so a detached HEAD is not an error here.
	pending.Go(func() { branch, _ = deps.Git.CurrentBranch(ctx) })
	pending.Go(func() { client, clientErr = deps.NewAI(ctx, cfg, model.Name) })
	if useCache {
		pending.Go(func() { root, _ = deps.Git.Root(ctx) })
	}
	pending.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if summaryErr != nil {
		return fmt.Errorf("failed to summarize staged changes: %w", summaryErr)
	}
	if statErr != nil {
		return fmt.Errorf("failed to get staged diff stat: %w", statErr)
	}
	if recentErr != nil {
		return fmt.Errorf("failed to get recent commits: %w", recentErr)
	}
	if clientErr != nil {
		return fmt.Errorf("failed to create AI client: %w", clientErr)
	}
	aiDiff := limitDiffWithWarning(diff, cfg.CommitMaxDiffBytes, "staged", session)
	input := ai.CommitInput{Diff: aiDiff, DiffStat: diffStat, Branch: branch, RecentCommits: recentCommits, Language: language}
	if useCache && root != "" {
		client = deps.CommitCache.Wrap(client, cfg, model.Name, root, diff)
	}
	if opts.DryRun || opts.Yes {
		if opts.DryRun && !opts.Quiet {
			for _, file := range summary.Files {
				fmt.Fprintf(session.Err, "%s (+%d, -%d)\n", file.Name, file.AddedLines, file.DeletedLines)
			}
			header := "=== Full Diff ==="
			if aiDiff != diff {
				header = "=== Diff Sent to AI (truncated) ==="
			}
			fmt.Fprintf(session.Err, "\n%s\n%s\n\n", header, aiDiff)
		}
		message, err := client.GenerateCommitMessage(ctx, input)
		if err != nil {
			return fmt.Errorf("failed to generate commit message: %w", err)
		}
		if opts.DryRun {
			_, err = fmt.Fprint(session.Out, message)
			return err
		}
		fmt.Fprintf(session.Out, "Generated commit message:\n%s\n\n", session.FormatCommitMessage(message))
		if err := deps.Git.Commit(ctx, message); err != nil {
			return fmt.Errorf("failed to commit changes: %w", err)
		}
		fmt.Fprintln(session.Out, session.Styles.Success.Render("✓ Commit successful"))
		return nil
	}
	return ui.NewTUI(session, client, input, summary, deps.Git.Commit).Run()
}

func limitDiffWithWarning(diff string, maxBytes int, kind string, session *ui.Session) string {
	limited, truncated := git.LimitDiff(diff, maxBytes)
	if truncated {
		fmt.Fprintf(session.Err, "Warning: %s diff is %d bytes; limiting AI input to %d bytes.\n", kind, len(diff), maxBytes)
	}
	return limited
}
