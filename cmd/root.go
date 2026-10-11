package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"

	"github.com/EkeMinusYou/gelf/internal/ai"
	"github.com/EkeMinusYou/gelf/internal/config"
	"github.com/EkeMinusYou/gelf/internal/git"
	"github.com/EkeMinusYou/gelf/internal/github"
	"github.com/EkeMinusYou/gelf/internal/process"
	"github.com/EkeMinusYou/gelf/internal/sessionlog"
	"github.com/spf13/cobra"
)

// version is set by release builds using ldflags.
var version = "dev"

type dependencies struct {
	Git          *git.Repository
	GitHub       *github.Client
	LoadConfig   func() (*config.Config, error)
	NewAI        func(context.Context, *config.Config, string) (ai.Client, error)
	FindSessions func(context.Context, string, string, sessionlog.Options) (sessionlog.Result, error)
}

func defaultDependencies() dependencies {
	runner := process.CommandRunner{}
	return dependencies{
		Git: &git.Repository{Runner: runner}, GitHub: github.NewClient(runner), LoadConfig: config.Load,
		FindSessions: sessionlog.Discover,
		NewAI: func(ctx context.Context, cfg *config.Config, model string) (ai.Client, error) {
			return ai.NewVertexAIClient(ctx, cfg, model)
		},
	}
}

func NewRootCommand() *cobra.Command { return newRootCommand(defaultDependencies()) }

func newRootCommand(deps dependencies) *cobra.Command {
	root := &cobra.Command{
		Use: "gelf", Short: "AI-powered Git commit and pull request generator using Vertex AI (Gemini)",
		SilenceUsage: true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddCommand(newCommitCommand(deps), newPRCommand(deps), newConfigCommand(deps))
	root.AddCommand(&cobra.Command{Use: "version", Short: "Print the version number of gelf", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			info, _ := debug.ReadBuildInfo()
			_, err := fmt.Fprintln(cmd.OutOrStdout(), buildVersion(info, version))
			return err
		},
	})
	root.AddCommand(&cobra.Command{
		Use: "completion [bash|zsh|fish|powershell]", Short: "Generate completion script",
		DisableFlagsInUseLine: true, ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		Args: cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(out)
			case "zsh":
				return cmd.Root().GenZshCompletion(out)
			case "fish":
				return cmd.Root().GenFishCompletion(out, true)
			default:
				return cmd.Root().GenPowerShellCompletionWithDesc(out)
			}
		},
	})
	return root
}

func buildVersion(info *debug.BuildInfo, release string) string {
	if release != "" && release != "dev" {
		return release
	}
	if info != nil {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
		revision, modified := "", false
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				modified = setting.Value == "true"
			}
		}
		if revision != "" {
			if len(revision) > 12 {
				revision = revision[:12]
			}
			if modified {
				revision += "-dirty"
			}
			return revision
		}
	}
	return strings.TrimSpace(release)
}

func Execute() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	return NewRootCommand().ExecuteContext(ctx)
}
