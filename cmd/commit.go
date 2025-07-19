package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/EkeMinusYou/gelf/internal/ai"
	"github.com/EkeMinusYou/gelf/internal/config"
	"github.com/EkeMinusYou/gelf/internal/git"
	"github.com/EkeMinusYou/gelf/internal/ui"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var commitCmd = &cobra.Command{
	Use:   "commit",
	Short: "Generate and commit with an AI-powered commit message",
	Long:  `Analyzes staged changes and generates a commit message using Vertex AI (Gemini).`,
	RunE:  runCommit,
}

var (
	dryRun         bool
	quiet          bool
	model          string
	commitLanguage string
)

var warningStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("3")). // イエロー
	Bold(true)

func init() {
	commitCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Generate message only without committing")
	commitCmd.Flags().BoolVar(&quiet, "quiet", false, "Don't show diff output (only with --dry-run)")
	commitCmd.Flags().StringVar(&model, "model", "", "Override default model for this generation")
	commitCmd.Flags().StringVar(&commitLanguage, "language", "", "Language for commit message generation (e.g., english, japanese)")
}

func runCommit(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	if !cfg.UseColor() {
		warningStyle = lipgloss.NewStyle() // No color
	}

	if model != "" {
		cfg.FlashModel = model
	}
	// Note: cfg.FlashModel is already set to the configured commit model in config.Load()

	if commitLanguage != "" {
		cfg.CommitLanguage = commitLanguage
	}

	diff, err := git.GetStagedDiff()
	if err != nil {
		return fmt.Errorf("failed to get staged changes: %w", err)
	}

	if diff == "" {
		message := warningStyle.Render("⚠ No staged changes found. Please stage some changes first with 'git add'.")
		if dryRun {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", message)
			return fmt.Errorf("no staged changes")
		} else {
			fmt.Print(message + "\n")
			return nil
		}
	}

	aiClient, err := ai.NewVertexAIClient(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to create AI client: %w", err)
	}

	if dryRun {
		if !quiet {
			diffSummary := git.ParseDiffSummary(diff)
			if len(diffSummary.Files) > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "=== Changed Files ===\n")
				for _, file := range diffSummary.Files {
					var changes []string
					if file.AddedLines > 0 {
						changes = append(changes, fmt.Sprintf("+%d", file.AddedLines))
					}
					if file.DeletedLines > 0 {
						changes = append(changes, fmt.Sprintf("-%d", file.DeletedLines))
					}

					if len(changes) > 0 {
						fmt.Fprintf(cmd.ErrOrStderr(), "%s (%s)\n", file.Name, strings.Join(changes, ", "))
					} else {
						fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", file.Name)
					}
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "\n=== Full Diff ===\n%s\n\n", diff)
			} else {
				fmt.Fprintf(cmd.ErrOrStderr(), "=== Staged Changes ===\n%s\n\n", diff)
			}
		}

		message, err := aiClient.GenerateCommitMessage(ctx, diff, cfg.CommitLanguage)
		if err != nil {
			return fmt.Errorf("failed to generate commit message: %w", err)
		}

		fmt.Print(message)
		return nil
	}

	if !cfg.UseColor() {
		ui.DisableColor()
	}
	tui := ui.NewTUI(aiClient, diff, cfg.CommitLanguage)
	if err := tui.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}
