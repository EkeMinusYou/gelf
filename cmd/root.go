package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

// version will be set at build time via ldflags
var version = "dev"

var rootCmd = &cobra.Command{
	Use:   "gelf",
	Short: "AI-powered Git commit message generator using Vertex AI (Gemini)",
	Long: `gelf is a CLI tool that generates Git commit messages using Vertex AI (Gemini).
It analyzes staged changes and creates appropriate commit messages through an interactive TUI.`,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of gelf",
	Run: func(cmd *cobra.Command, args []string) {
		if version == "dev" {
			// Try to get git commit hash when installed via go install
			if hash := getGitCommitHash(); hash != "" {
				fmt.Println(hash)
			} else {
				fmt.Println(version)
			}
		} else {
			fmt.Println(version)
		}
	},
}

func getGitCommitHash() string {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.AddCommand(commitCmd)
	rootCmd.AddCommand(reviewCmd)
	rootCmd.AddCommand(docCmd)
	rootCmd.AddCommand(prCmd)
	rootCmd.AddCommand(versionCmd)

	// Add completion commands
	rootCmd.AddCommand(&cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate completion script",
		Long: `Generate completion script for the specified shell.

Examples:
  # bash completion
  gelf completion bash > /usr/local/etc/bash_completion.d/gelf
  
  # zsh completion
  gelf completion zsh > "${fpath[1]}/_gelf"
  
  # fish completion
  gelf completion fish > ~/.config/fish/completions/gelf.fish
  
  # powershell completion
  gelf completion powershell > gelf.ps1`,
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		Run: func(cmd *cobra.Command, args []string) {
			switch args[0] {
			case "bash":
				cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh":
				cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell":
				cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
			}
		},
	})
}
