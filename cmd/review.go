package cmd

import (
	"context"
	"fmt"

	"github.com/EkeMinusYou/gelf/internal/ai"
	"github.com/EkeMinusYou/gelf/internal/config"
	"github.com/EkeMinusYou/gelf/internal/git"
	"github.com/EkeMinusYou/gelf/internal/ui"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var reviewCmd = &cobra.Command{
	Use:   "review",
	Short: "Review code changes using AI",
	Long:  `Analyzes git diff and provides code review feedback using Vertex AI (Gemini).`,
	RunE:  runReview,
}

var (
	reviewStaged   bool
	reviewModel    string
	noStyle        bool
	reviewLanguage string
)

var reviewWarningStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("3")). // イエロー
	Bold(true)

func init() {
	reviewCmd.Flags().BoolVar(&reviewStaged, "staged", false, "Review staged changes instead of unstaged changes")
	reviewCmd.Flags().StringVar(&reviewModel, "model", "", "Override default model for this review")
	reviewCmd.Flags().BoolVar(&noStyle, "no-style", false, "Disable markdown styling")
	reviewCmd.Flags().StringVar(&reviewLanguage, "language", "", "Language for code review generation (e.g., english, japanese)")
}

func runReview(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Create warning style based on color settings
	if !cfg.UseColor() {
		reviewWarningStyle = lipgloss.NewStyle() // No color
	}

	if reviewModel != "" {
		cfg.FlashModel = reviewModel
	} else {
		// Use the configured review model (pro model by default)
		cfg.FlashModel = cfg.ProModel
	}

	if reviewLanguage != "" {
		cfg.ReviewLanguage = reviewLanguage
	}

	var diff string
	if reviewStaged {
		diff, err = git.GetStagedDiff()
		if err != nil {
			return fmt.Errorf("failed to get staged changes: %w", err)
		}
	} else {
		diff, err = git.GetUnstagedDiff()
		if err != nil {
			return fmt.Errorf("failed to get unstaged changes: %w", err)
		}
	}

	if diff == "" {
		var message string
		if reviewStaged {
			message = reviewWarningStyle.Render("⚠ No staged changes found. Please stage some changes first with 'git add'.")
		} else {
			message = reviewWarningStyle.Render("⚠ No unstaged changes found.")
		}
		fmt.Print(message + "\n")
		return nil
	}

	aiClient, err := ai.NewVertexAIClient(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to create AI client: %w", err)
	}

	if !cfg.UseColor() {
		ui.DisableColor()
	}
	// Use TUI for loading and display experience
	reviewTUI := ui.NewReviewTUI(aiClient, diff, noStyle, cfg.ReviewLanguage)
	_, err = reviewTUI.Run()
	if err != nil {
		return fmt.Errorf("failed to generate code review: %w", err)
	}

	return nil
}
