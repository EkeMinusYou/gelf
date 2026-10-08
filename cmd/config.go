package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newConfigCommand(deps dependencies) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Manage gelf configuration"}
	cmd.AddCommand(&cobra.Command{Use: "list", Short: "List current configuration", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := deps.LoadConfig()
			if err != nil {
				return fmt.Errorf("failed to load configuration: %w", err)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Current Configuration:\n======================\nProject ID:        %s\nLocation:          %s\nFlash Model:       %s\nPro Model:         %s\nCommit Model:      %s\nCommit Language:   %s\nCommit Diff Limit: %d bytes\nPR Model:          %s\nPR Language:       %s\nPR Title Language: %s\nPR Body Language:  %s\nPR Diff Limit:     %d bytes\nColor:             %s\n", cfg.ProjectID, cfg.Location, cfg.FlashModel, cfg.ProModel, cfg.CommitModel, cfg.CommitLanguage, cfg.CommitMaxDiffBytes, cfg.PRModel, cfg.PRLanguage, cfg.PRTitleLanguage, cfg.PRBodyLanguage, cfg.PRMaxDiffBytes, cfg.Color)
			fmt.Fprintf(out, "PR Session Logs:   %t\nPR Session Count:  %d\nPR Session Limit:  %d bytes\n", cfg.PRSessionLogs, cfg.PRSessionLogCount, cfg.PRMaxSessionLogBytes)
			fmt.Fprintln(out, "\nEnvironment Variables:\n======================")
			for _, name := range []string{"VERTEXAI_PROJECT", "GOOGLE_CLOUD_PROJECT", "VERTEXAI_LOCATION", "GELF_CREDENTIALS", "GOOGLE_APPLICATION_CREDENTIALS"} {
				value := os.Getenv(name)
				if value == "" {
					value = "(not set)"
				}
				fmt.Fprintf(out, "%-30s %s\n", name+":", value)
			}
			return nil
		},
	})
	return cmd
}
