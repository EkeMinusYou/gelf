package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/EkeMinusYou/gelf/internal/ai"
	"github.com/EkeMinusYou/gelf/internal/config"
	"github.com/EkeMinusYou/gelf/internal/git"
	"github.com/EkeMinusYou/gelf/internal/github"
	"github.com/EkeMinusYou/gelf/internal/sessionlog"
	"github.com/EkeMinusYou/gelf/internal/ui"
	"github.com/spf13/cobra"
)

type prOptions struct {
	Draft, DryRun, Render, NoRender, Yes, Update, SessionLogs, NoSessionLogs bool
	SessionLogCount                                                          int
	Model, Language, TitleLanguage, BodyLanguage                             string
}

func newPRCommand(deps dependencies) *cobra.Command {
	var opts prOptions
	parent := &cobra.Command{Use: "pr", Short: "Manage pull requests"}
	cmd := &cobra.Command{Use: "create", Short: "Create a pull request with AI-generated title and description", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runPRCreate(cmd, opts, deps) },
	}
	cmd.Flags().BoolVar(&opts.Draft, "draft", false, "Create the pull request as a draft")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Print generated content without pushing or creating a pull request (fetches required refs)")
	cmd.Flags().BoolVar(&opts.Render, "render", true, "Render pull request markdown body")
	cmd.Flags().BoolVar(&opts.NoRender, "no-render", false, "Disable markdown rendering")
	cmd.Flags().BoolVar(&opts.SessionLogs, "session-logs", false, "Enable automatic Claude/Codex session log context (overrides configuration)")
	cmd.Flags().BoolVar(&opts.NoSessionLogs, "no-session-logs", false, "Disable automatic Claude/Codex session log context")
	cmd.Flags().IntVar(&opts.SessionLogCount, "session-log-count", config.DefaultPRSessionLogCount, "Maximum recent sessions to reference when session logs are enabled (overrides configuration)")
	cmd.Flags().BoolVar(&opts.Yes, "yes", false, "Approve normal push and PR creation (force push still requires confirmation)")
	cmd.Flags().BoolVar(&opts.Update, "update", false, "Update the matching open pull request, or create one if none exists")
	cmd.Flags().StringVar(&opts.Model, "model", "", "Override default model for PR generation")
	cmd.Flags().StringVar(&opts.Language, "language", "", "Language for PR generation")
	cmd.Flags().StringVar(&opts.TitleLanguage, "title-language", "", "Language for PR title")
	cmd.Flags().StringVar(&opts.BodyLanguage, "body-language", "", "Language for PR body")
	parent.AddCommand(cmd)
	return parent
}

type prContext struct {
	Base, Head github.RepoInfo
	BaseBranch string
	Target     git.PushTarget
	Status     git.PushStatus
	Existing   *github.PullRequestInfo
	GitHub     *github.Client
	Input      ai.PullRequestInput
	Summary    git.DiffSummary
	Template   *github.PullRequestTemplate
}

func runPRCreate(cmd *cobra.Command, opts prOptions, deps dependencies) error {
	ctx := cmd.Context()
	cfg, err := deps.LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}
	// Resolve CLI overrides on a copy so shared configuration is not mutated.
	resolved := *cfg
	cfg = &resolved
	if cmd.Flags().Changed("session-logs") {
		cfg.PRSessionLogs = opts.SessionLogs
	}
	if cmd.Flags().Changed("session-log-count") {
		if opts.SessionLogCount <= 0 {
			return fmt.Errorf("--session-log-count must be greater than zero")
		}
		cfg.PRSessionLogCount = opts.SessionLogCount
	}
	session := ui.NewSession(ctx, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), cfg.UseColor(cmd.OutOrStdout()), cfg.UseColor(cmd.ErrOrStderr()))
	opts.Render = opts.Render && !opts.NoRender
	pr, err := resolvePR(ctx, deps)
	if err != nil {
		return err
	}
	if pr.Existing != nil && !opts.Update {
		fmt.Fprintf(session.Err, "Pull request already exists for %s:%s: #%d %s (%s)\n", pr.Head.FullName(), pr.Target.Branch, pr.Existing.Number, pr.Existing.Title, pr.Existing.URL)
		return nil
	}
	opts.Update = opts.Update && pr.Existing != nil
	if err := preparePR(ctx, deps, pr, cfg, opts, session); err != nil {
		return err
	}
	if !opts.DryRun {
		confirmed, err := ensureBranchPushed(ctx, deps.Git, pr.Status, opts.Yes, session)
		if err != nil {
			return err
		}
		if !confirmed {
			return nil
		}
	}
	model := cfg.PRModel
	if opts.Model != "" {
		model = cfg.ResolveModel(opts.Model)
	}
	client, err := deps.NewAI(ctx, cfg, model)
	if err != nil {
		return fmt.Errorf("failed to create AI client: %w", err)
	}
	content, confirmed, err := generatePR(client, pr, opts, session)
	if err != nil || !confirmed || opts.DryRun {
		return err
	}
	return publishPR(ctx, pr, content, opts, session)
}

func resolvePR(ctx context.Context, deps dependencies) (*prContext, error) {
	current, parent, err := deps.GitHub.Repo(ctx, "")
	if err != nil {
		return nil, err
	}
	base := current
	if parent != nil {
		base = parent
	}
	if base.DefaultBranch == "" {
		base, _, err = deps.GitHub.Repo(ctx, base.GHName())
		if err != nil {
			return nil, err
		}
	}
	if base.DefaultBranch == "" {
		return nil, fmt.Errorf("base repository has no default branch")
	}
	branch, err := deps.Git.CurrentBranch(ctx)
	if err != nil {
		return nil, err
	}
	target, err := deps.Git.PushTarget(ctx, branch)
	if err != nil {
		return nil, err
	}
	pushURL, err := deps.Git.RemoteURL(ctx, target.RemoteName, true)
	if err != nil {
		return nil, err
	}
	head, err := github.RepoInfoFromRemoteURL(pushURL)
	if err != nil {
		return nil, err
	}
	gh := *deps.GitHub
	gh.Host = base.Host
	existing, err := gh.FindPullRequest(ctx, *base, *head, target.Branch)
	if err != nil {
		return nil, err
	}
	baseBranch := base.DefaultBranch
	if existing != nil {
		baseBranch = existing.Base.Ref
	}
	return &prContext{Base: *base, Head: *head, BaseBranch: baseBranch, Target: target, Existing: existing, GitHub: &gh}, nil
}

func preparePR(ctx context.Context, deps dependencies, pr *prContext, cfg *config.Config, opts prOptions, session *ui.Session) error {
	remote, err := baseRemote(ctx, deps.Git, pr.Base)
	if err != nil {
		return err
	}
	baseSHA, err := deps.Git.FetchBase(ctx, remote, pr.BaseBranch)
	if err != nil {
		return err
	}
	pr.Status, err = deps.Git.PushStatus(ctx, pr.Target)
	if err != nil {
		return err
	}
	headSHA := pr.Status.HeadSHA
	log, err := deps.Git.CommitLog(ctx, baseSHA, headSHA)
	if err != nil {
		return fmt.Errorf("failed to get commit log: %w", err)
	}
	if log == "" {
		return fmt.Errorf("no commits found between %s and %s", pr.BaseBranch, pr.Target.Branch)
	}
	stat, err := deps.Git.CommittedDiffStat(ctx, baseSHA, headSHA)
	if err != nil {
		return err
	}
	diff, err := deps.Git.CommittedDiff(ctx, baseSHA, headSHA)
	if err != nil {
		return err
	}
	if diff == "" {
		return fmt.Errorf("no committed changes found between %s and %s", pr.BaseBranch, pr.Target.Branch)
	}
	pr.Summary, err = deps.Git.CommittedSummary(ctx, baseSHA, headSHA)
	if err != nil {
		return err
	}
	root, err := deps.Git.Root(ctx)
	if err != nil {
		return err
	}
	pr.Template, err = pr.GitHub.FindPullRequestTemplate(ctx, root, pr.Base.Owner)
	if err != nil {
		return fmt.Errorf("failed to resolve pull request template: %w", err)
	}
	template := ""
	if pr.Template != nil {
		template = pr.Template.Content
	}
	titleLanguage, bodyLanguage := cfg.PRTitleLanguage, cfg.PRBodyLanguage
	if opts.Language != "" {
		titleLanguage, bodyLanguage = opts.Language, opts.Language
	}
	if opts.TitleLanguage != "" {
		titleLanguage = opts.TitleLanguage
	}
	if opts.BodyLanguage != "" {
		bodyLanguage = opts.BodyLanguage
	}
	pr.Input = ai.PullRequestInput{
		BaseBranch: pr.BaseBranch, HeadBranch: pr.Target.Branch, CommitLog: log, DiffStat: stat,
		Diff: limitDiffWithWarning(diff, cfg.PRMaxDiffBytes, "PR", session), Template: template,
		Language: cfg.PRLanguage, TitleLanguage: titleLanguage, BodyLanguage: bodyLanguage,
	}
	if cfg.PRSessionLogs && !opts.NoSessionLogs && deps.FindSessions != nil {
		logs, err := deps.FindSessions(ctx, root, pr.Target.LocalBranch, sessionlog.Options{MaxSessions: cfg.PRSessionLogCount, MaxBytes: cfg.PRMaxSessionLogBytes})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			fmt.Fprintf(session.Err, "Warning: could not read some agent session logs: %v\n", err)
		}
		pr.Input.SessionContext = logs.Context
		for _, source := range logs.Sources {
			fmt.Fprintf(session.Err, "Using %s session log: %q\n", source.Agent, source.Path)
		}
		if logs.Truncated {
			fmt.Fprintln(session.Err, "Warning: session context was truncated; retaining recent messages.")
		}
	}
	return nil
}

func baseRemote(ctx context.Context, repo *git.Repository, base github.RepoInfo) (string, error) {
	remotes, err := repo.Remotes(ctx)
	if err != nil {
		return "", err
	}
	for _, remote := range remotes {
		remoteURL, err := repo.RemoteURL(ctx, remote, false)
		if err != nil {
			return "", err
		}
		info, err := github.RepoInfoFromRemoteURL(remoteURL)
		if err == nil && info.Equal(base) {
			return remote, nil
		}
	}
	if base.URL == "" {
		return "", fmt.Errorf("no remote or clone URL found for %s", base.FullName())
	}
	return strings.TrimSuffix(base.URL, ".git") + ".git", nil
}

func ensureBranchPushed(ctx context.Context, repo *git.Repository, status git.PushStatus, yes bool, session *ui.Session) (bool, error) {
	if status.UpToDate() {
		return true, nil
	}
	if status.BehindOnly() {
		return false, fmt.Errorf("local branch is %d commit(s) behind %s/%s; integrate the remote changes before creating or updating a PR", status.Behind, status.Target.RemoteName, status.Target.Branch)
	}
	force := status.Diverged()
	if force || !yes {
		prompt := fmt.Sprintf("Push current branch to %s/%s? (y)es / (n)o", status.Target.RemoteName, status.Target.Branch)
		if force {
			prompt = fmt.Sprintf("Branch history has diverged (%d ahead, %d behind). Force push with lease to %s/%s? (y)es / (n)o", status.Ahead, status.Behind, status.Target.RemoteName, status.Target.Branch)
		}
		confirmed, err := session.YesNo(prompt)
		if err != nil || !confirmed {
			return false, err
		}
	}
	message := "Pushing branch..."
	if force {
		message = "Force pushing branch..."
	}
	stop := session.Spinner(message, true)
	err := repo.Push(ctx, status, force)
	stop()
	if err != nil {
		return false, fmt.Errorf("failed to push branch; refresh remote status before retrying: %w", err)
	}
	fmt.Fprintf(session.Out, "%s\n\n", session.Styles.Success.Render("✓ Push succeeded"))
	return true, nil
}

func generatePR(client ai.Client, pr *prContext, opts prOptions, session *ui.Session) (*ai.PullRequestContent, bool, error) {
	if !opts.DryRun && !opts.Yes {
		prompt := "Create this pull request? (y)es / (e)dit / (p)rompt / (n)o"
		if opts.Update {
			prompt = "Update this pull request? (y)es / (e)dit / (p)rompt / (n)o"
		}
		return ui.NewPRTUI(session, client, pr.Input, pr.Summary, opts.Render, prompt).Run()
	}
	content, err := client.GeneratePullRequestContent(session.Context, pr.Input)
	if err != nil {
		return nil, false, err
	}
	if opts.DryRun {
		if pr.Template != nil {
			fmt.Fprintf(session.Err, "Using %s template: %s\n", pr.Template.Source, pr.Template.Path)
		}
		body := content.Body
		if opts.Render {
			rendered, err := ui.RenderMarkdown(body, session.UseColor)
			if err != nil {
				fmt.Fprintf(session.Err, "Failed to render markdown: %v\n", err)
			} else {
				body = rendered
			}
		}
		fmt.Fprintf(session.Out, "Title:\n%s\n\nBody:\n%s\n", content.Title, body)
	}
	return content, true, nil
}

func publishPR(ctx context.Context, pr *prContext, content *ai.PullRequestContent, opts prOptions, session *ui.Session) error {
	message, header := "Creating pull request...", "✓ Pull request created"
	if opts.Update {
		message, header = "Updating pull request...", "✓ Pull request updated"
	}
	stop := session.Spinner(message, false)
	defer stop()
	var published *github.PullRequestInfo
	var err error
	if opts.Update {
		published, err = pr.GitHub.Update(ctx, pr.Base, pr.Existing.Number, content)
	} else {
		published, err = pr.GitHub.Create(ctx, pr.Base, pr.Head, pr.Target.Branch, pr.BaseBranch, content, opts.Draft)
	}
	if err != nil {
		return err
	}
	stop()
	suffix := fmt.Sprintf("(#%d)", published.Number)
	if published.IsDraft {
		suffix += " (draft)"
	}
	_, err = fmt.Fprintln(session.Out, session.RenderPRSuccess(header, content.Title, suffix, published.URL))
	return err
}
