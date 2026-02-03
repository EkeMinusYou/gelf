package cmd

import (
	"fmt"
	"os"

	"github.com/EkeMinusYou/gelf/internal/config"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage gelf configuration",
	Long:  "Manage gelf configuration settings",
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "List current configuration",
	Long:  "Display current configuration values from file and environment variables",
	RunE:  runConfigList,
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configListCmd)
}

func runConfigList(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	fmt.Println("Current Configuration:")
	fmt.Println("======================")
	fmt.Printf("Project ID:        %s\n", cfg.ProjectID)
	fmt.Printf("Location:          %s\n", cfg.Location)
	fmt.Printf("Flash Model:       %s\n", cfg.FlashModel)
	fmt.Printf("Pro Model:         %s\n", cfg.ProModel)
	fmt.Printf("Commit Model:      %s\n", cfg.CommitModel)
	fmt.Printf("Commit Language:   %s\n", cfg.CommitLanguage)
	fmt.Printf("Review Model:      %s\n", cfg.ReviewModel)
	fmt.Printf("Review Language:   %s\n", cfg.ReviewLanguage)
	fmt.Printf("PR Model:          %s\n", cfg.PRModel)
	fmt.Printf("PR Language:       %s\n", cfg.PRLanguage)

	fmt.Println("\nEnvironment Variables:")
	fmt.Println("======================")
	printEnvVar("VERTEXAI_PROJECT")
	printEnvVar("GOOGLE_CLOUD_PROJECT")
	printEnvVar("VERTEXAI_LOCATION")
	printEnvVar("GELF_CREDENTIALS")
	printEnvVar("GOOGLE_APPLICATION_CREDENTIALS")

	return nil
}

func printEnvVar(name string) {
	value := os.Getenv(name)
	if value != "" {
		fmt.Printf("%-30s %s\n", name+":", value)
	} else {
		fmt.Printf("%-30s (not set)\n", name+":")
	}
}
