# 🚀 gelf

gelf is a Go-based CLI tool that generates Git commit messages and AI-assisted pull request titles/descriptions using Vertex AI (Gemini). It analyzes git changes and provides a modern, interactive TUI interface built with Bubble Tea.

## ✨ Features

- 🤖 **AI-Powered**: Intelligent commit message generation using Vertex AI (Gemini)
- 📝 **PR Creation**: Generate pull request titles and descriptions with AI
- 💬 **Interactive Revisions**: Refine generated PR titles and bodies with chat-style instructions
- 🎨 **Clean TUI**: Simple and intuitive user interface built with Bubble Tea  
- ⚡ **Fast Processing**: Real-time progress indicators during generation
- 🛡️ **Safe Operations**: Commit generation uses staged changes for a secure workflow
- 🌐 **Cross-Platform**: Works seamlessly across different operating systems
- 🌍 **Multi-language Support**: Generate commit messages and PRs in multiple languages

## 🛠️ Installation

### Prerequisites

- Go 1.27.1 or higher
- Google Cloud account with Vertex AI API enabled
- Git (required for commit and PR operations)
- GitHub CLI (`gh`), authenticated with `gh auth login` (required for PR operations)

### Build from Source

```bash
git clone https://github.com/EkeMinusYou/gelf.git
cd gelf
go build
```

### Install Binary

```bash
go install github.com/EkeMinusYou/gelf@latest
```

### Install via Homebrew

```bash
brew tap ekeminusyou/gelf
brew install ekeminusyou/gelf/gelf
```

If macOS blocks execution due to the quarantine attribute, remove it with:

```bash
sudo xattr -d com.apple.quarantine "$(which gelf)"
```

## ⚙️ Setup

### 1. Configuration Options

gelf supports both configuration files and environment variables. Configuration files provide a more organized approach for managing settings.

#### Configuration File (Recommended)

Create a `gelf.yml` file in one of the following locations (in order of priority):

1. `./gelf.yml` - Project-specific configuration
2. `$XDG_CONFIG_HOME/gelf/gelf.yml` - XDG config directory
3. `~/.config/gelf/gelf.yml` - Default XDG config location
4. `~/.gelf.yml` - Legacy home directory location

```yaml
vertex_ai:
  project_id: "your-gcp-project-id"
  location: "global"  # optional, default: global

model:
  flash: gemini-3.8-flash
  pro: gemini-3.1-pro-preview

language: "english"  # optional, default: english

commit:
  model: "flash"     # optional, default: flash
  language: "english"  # optional, inherits from global language
  thinking: "minimal"  # optional, default: minimal

pr:
  model: "pro"       # optional, default: pro
  language: "english"  # optional, inherits from global language
  title_language: "english"  # optional, inherits from pr.language
  body_language: "english"   # optional, inherits from pr.language

color: "always"  # optional, default: always
```

#### Environment Variables (Alternative)

You can also configure using environment variables:

```bash
# Path to your service account key file (gelf-specific, takes priority)
export GELF_CREDENTIALS="/path/to/your/service-account-key.json"

# Alternative: Standard Google Cloud credentials (used if GELF_CREDENTIALS is not set)
export GOOGLE_APPLICATION_CREDENTIALS="/path/to/your/service-account-key.json"

# Google Cloud project ID
export VERTEXAI_PROJECT="your-project-id"

# Vertex AI location (optional, default: global)
export VERTEXAI_LOCATION="global"
```

**Note**: Model configuration and language settings can only be configured via configuration file, not environment variables.
**Note**: If Application Default Credentials (ADC) are already available (e.g., via `gcloud auth application-default login`, Workload Identity, or GCE/GKE metadata), you can omit both credential environment variables.

### 2. Google Cloud Authentication

1. Create a service account in Google Cloud Console
2. Grant the "Vertex AI User" role
3. Download the JSON key file
4. Set the `GELF_CREDENTIALS` environment variable to the file path (recommended), or provide ADC via `GOOGLE_APPLICATION_CREDENTIALS` or `gcloud auth application-default login` / Workload Identity / GCE/GKE metadata

## 🚀 Usage

### Commit Message Generation

1. Stage your changes:
```bash
git add .
```

2. Generate and commit with AI:
```bash
gelf commit
```

Generated messages have a Conventional Commits subject line and, for material changes, a body separated by a blank line. The AI receives the staged diff, its diffstat, the current branch name, and the five most recent commit subjects so that the generated message follows the repository's existing scope and wording style.

3. Interactive TUI operations:
   - Review the AI-generated commit message
   - Press `y` to approve or `n` to cancel
   - Press `e` to edit the commit message (the cursor starts at the end of the subject line; Enter inserts a new line, `Ctrl+S` confirms, `Esc` cancels)
   - Press `r` to tell the AI how to revise the message (e.g. "add a body", "write it in Japanese"); `Esc` cancels
   - Press `q` or `Ctrl+C` to cancel during generation
   - The commit will be executed automatically upon approval
   - Success message with the commit subject displays after TUI exits

### Pull Request Creation

Generate pull requests with AI-generated titles and descriptions based on committed changes:

```bash
gelf pr create
```

The generated body follows the PR template when available; otherwise, it briefly explains the purpose and key changes, using headings or bullet points when helpful rather than fixed sections.

The PR head is the repository and branch selected by the push remote. gelf respects `branch.<branch>.pushRemote`, `remote.pushDefault`, and the branch's upstream remote, falling back to `origin`. The base repository is the fork's parent when applicable, and the comparison uses that repository's default branch. An update uses the existing PR's base branch instead.

Before generating content, gelf fetches the required base and head refs. This refreshes local refs and objects without changing working files or the current branch. If the base repository has no configured remote, gelf fetches its clone URL without adding a remote. `--dry-run` also fetches these refs but does not push or create/update a PR.

A branch that is only behind the remote must be brought up to date manually before creating or updating a PR. Divergent history requires an explicit force-push confirmation, including with `--yes`. The force push uses a lease tied to the remote commit observed during preparation, so a later remote update is rejected.

Only open PRs (including drafts) with the exact head repository and branch count as existing PRs. Closed and merged PRs do not prevent a new PR. Use `--update` to replace an existing open PR's title and body, or create a new PR if none exists. Multiple matching open PRs cause an error.

After the PR title and description are generated, the interactive prompt lets you:

- Press `y` to create the pull request with the generated content
- Press `r` to enter chat-style revision instructions (e.g. "shorten the title", "clarify the summary in Japanese"). gelf re-generates the title/body using your feedback and asks again — repeat as many times as you like.
- Press `n` (or `Esc` / `q`) to cancel without creating a PR

Options:
- `--draft` to create a draft PR
- `--dry-run` to print the generated title/body without pushing or creating/updating a PR (required refs are fetched)
- `--render` to render markdown in dry-run output (default: true)
- `--no-render` to disable markdown rendering
- `--model` to override the model for PR generation
- `--language` to set the output language for both title and body
- `--title-language` to set the language for PR title only
- `--body-language` to set the language for PR body only
- `--yes` to approve normal push and PR creation/update automatically (also skips revisions; force push still requires confirmation)
- `--update` to update the matching open PR, or create a new one if none exists

### Command Options

```bash
# Show help
gelf --help

# Show commit command help
gelf commit --help

# Generate commit message with TUI interface (default behavior)
gelf commit

# Generate commit message only with diff display (for debugging)
gelf commit --dry-run

# Generate commit message only without diff (for external tool integration)
gelf commit --dry-run --quiet

# Use specific model temporarily
gelf commit --model gemini-2.0-flash-exp

# Generate commit message in a specific language
gelf commit --language japanese

# Automatically approve commit message
gelf commit --yes

# Create a pull request with AI-generated title/body
gelf pr create

# Create a draft pull request
gelf pr create --draft

# Preview generated PR title/body without creating a PR
gelf pr create --dry-run

# Preview without markdown rendering
gelf pr create --dry-run --no-render

# Use specific model and language for PR generation
gelf pr create --model gemini-2.0-flash-exp --language japanese

# Use different languages for title and body
gelf pr create --title-language english --body-language japanese

# Skip normal push and PR confirmation prompts
gelf pr create --yes

# Regenerate the title and body of the matching open PR, or create a new PR
gelf pr create --update

```

## 🌍 Language Support

gelf supports generating commit messages and pull request content in multiple languages. You can configure language settings both through configuration files and command-line options.

### Supported Languages

While gelf can work with any language supported by Gemini models, common examples include:
- `english` (default)
- `japanese`
- `spanish` 
- `french`
- `german`
- `chinese`
- `korean`
- And many more...

### Configuration Options

#### 1. Command Line (Highest Priority)
```bash
# Set language for specific commands
gelf commit --language japanese
gelf pr create --language french

# Use different languages for different operations
gelf commit --language english
gelf pr create --language japanese

# Use different languages for PR title and body
gelf pr create --title-language english --body-language japanese
```

#### 2. Configuration File
```yaml
language: "japanese"  # Global default language

commit:
  language: "japanese"  # Language for commit messages

pr:
  language: "english"   # Language for pull request titles and descriptions
  title_language: "english"  # Override language for PR title only
  body_language: "japanese"  # Override language for PR body only
```

#### 3. Defaults
If no language is specified, commit messages and PR content will use English.

### Priority Order
1. Command-line flags (highest priority)
   - `--language` sets both title and body language
   - `--title-language` overrides title language specifically
   - `--body-language` overrides body language specifically
2. Configuration file command-specific settings (`commit.language`/`pr.language`/`pr.title_language`/`pr.body_language`)
3. Configuration file global setting (`language`)
4. Default value (`english`)

This allows you to set a global default language, override it for specific commands, and even use different languages for PR titles and bodies.

## 🔧 Technical Specifications

### Architecture

- **Commit Target**: Staged changes only (`git diff --staged`)
- **PR Target**: Committed changes between base branch and `HEAD`
- **AI Provider**: Vertex AI (Gemini models)
- **Default Flash Model**: gemini-3.8-flash
- **Default Pro Model**: gemini-3.1-pro-preview
- **UI Framework**: Bubble Tea (TUI)
- **CLI Framework**: Cobra

### Project Structure

```
cmd/
├── root.go          # Command construction and build version
├── config.go        # Configuration inspection
├── commit.go        # Commit command implementation
└── pr.go            # Pull request command implementation
internal/
├── git/
│   ├── diff.go      # Staged diffs, complete file summaries, and input limits
│   ├── branch.go    # Branch and commit range helpers
│   ├── push.go      # Push target resolution, status, and leases
│   └── remote.go    # Remote URLs and base ref fetching
├── github/
│   ├── gh.go        # GitHub repository and PR API operations
│   └── template.go  # GitHub PR template resolution
├── ai/
│   └── vertex.go    # Vertex AI integration (commit messages and PR generation)
├── ui/
│   ├── session.go   # Per-command input, output, and styles
│   └── tui.go       # Bubble Tea TUI implementation (commit)
├── process/
│   └── runner.go    # Context-aware subprocess execution
└── config/
    └── config.go    # Configuration loading and model resolution
main.go             # Application entry point
```

## 🎨 User Interface

The application provides a clean, interactive terminal interface for commit and PR generation:

### Commit Workflow
- Loading indicator while generating commit messages
- Review screen for generated commit messages with approval options
- Success confirmation after successful commits

### Pull Request Workflow
- Loading indicator while generating PR title and description
- Review screen showing the generated title and (optionally rendered) body
- Choose between creating, revising via chat instructions, or cancelling
- When revising, type your feedback (e.g. "make it more concise") and gelf regenerates the title/body — loop until you are satisfied

The interface features color-coded states, animated progress indicators, and intuitive keyboard controls for a smooth user experience.

## ⚙️ Configuration Reference

### Configuration Priority

Settings are applied in the following order (highest to lowest priority):

1. **Environment variables** (for Vertex AI settings only)
2. **Configuration file** (`gelf.yml`)
3. **Default values**

### Configuration File Options

```yaml
vertex_ai:
  project_id: string     # Google Cloud project ID
  location: string       # Vertex AI location (default: global)

model:
  flash: string          # Gemini Flash model to use (default: gemini-3.8-flash)
  pro: string            # Gemini Pro model to use (default: gemini-3.1-pro-preview)

language: string         # Global default language (default: english)

commit:
  model: string          # Model for commits: "flash", "pro", or custom (default: flash)
  language: string       # Language for commit messages (inherits from global if not set)
  max_diff_bytes: number # Maximum staged diff size sent to the AI (default: 100000)
  thinking: string       # Gemini thinking level for commits: "minimal", "low", "medium", "high", or "default" (default: minimal)
                         # Unsupported levels fall back automatically (minimal → low → model default), e.g. for Pro or Gemini 2.5

pr:
  model: string          # Model for pull requests: "flash", "pro", or custom (default: pro)
  language: string       # Language for pull request titles and descriptions (inherits from global if not set)
  title_language: string # Language for PR title only (inherits from pr.language if not set)
  body_language: string  # Language for PR body only (inherits from pr.language if not set)
  max_diff_bytes: number # Maximum committed diff size sent to the AI (default: 100000)

color: string            # Color output setting: "auto", "always", or "never" (default: always)
```

Both diff limits apply only to AI input. Oversized diffs are truncated at a line/UTF-8 boundary with a warning, while interactive changed-file summaries still include every file and its full line counts. Omitted or zero limits use 100,000 bytes; negative values are invalid.

Configuration files that are missing are skipped during discovery. Malformed YAML and other read errors stop the command and report the affected file instead of silently using defaults. `color: auto` enables color only for terminal output and respects `NO_COLOR` and `TERM=dumb`; `always` is the default and `never` disables styling.

### Environment Variables

| Variable | Description | Default Value | Required |
|----------|-------------|---------------|----------|
| `GELF_CREDENTIALS` | Path to service account key file (gelf-specific, takes priority) | - | ⚠️* |
| `GOOGLE_APPLICATION_CREDENTIALS` | Path to service account key file (ADC fallback) | - | ⚠️* |
| `VERTEXAI_PROJECT` or `GOOGLE_CLOUD_PROJECT` | Google Cloud project ID | - | ✅ |
| `VERTEXAI_LOCATION` | Vertex AI location | `global` | ❌ |

*Either `GELF_CREDENTIALS` or `GOOGLE_APPLICATION_CREDENTIALS` is required unless ADC is already available (e.g., `gcloud auth application-default login`, Workload Identity, or GCE/GKE metadata). If both are set, `GELF_CREDENTIALS` takes priority.

**Note**: Model configuration and language settings are only available through configuration files.

## 🔨 Development

### Development Environment Setup

```bash
# Install dependencies
go mod download

# Build the project
go build

# Run tests
go test ./...

# Tidy dependencies
go mod tidy
```

Pull request and commit errors are returned as a nonzero exit status, including errors in the interactive UI. External command errors include Git/GitHub diagnostics. `gelf version` reports release, module, or embedded VCS build information independently of the repository where it runs.

Commands receive their Git, GitHub, configuration, and AI dependencies, while UI sessions own their readers, writers, and styles. This keeps command construction independent between executions and lets tests exercise failure paths without external services.

CI runs tests with race detection, `go vet`, Staticcheck, and a build on Linux and macOS. Tests use temporary local Git repositories and mocked AI/GitHub responses; they require no cloud credentials and do not publish real PRs.

### Available Commands

```bash
go build                     # Build the project
go test ./...                # Run tests
go mod tidy                  # Tidy dependencies
go run main.go commit        # Run commit command in development
go run main.go commit --dry-run  # Run message generation only
go run main.go pr create     # Run PR creation in development
```

## 📦 Dependencies

### Main Dependencies
- [`google.golang.org/genai`](https://pkg.go.dev/google.golang.org/genai) - Official Gemini Go client
- [`github.com/charmbracelet/bubbletea`](https://github.com/charmbracelet/bubbletea) - TUI framework
- [`github.com/charmbracelet/lipgloss`](https://github.com/charmbracelet/lipgloss) - Styling and layout
- [`github.com/charmbracelet/bubbles`](https://github.com/charmbracelet/bubbles) - TUI components (spinner)
- [`github.com/charmbracelet/glamour`](https://github.com/charmbracelet/glamour) - Markdown rendering for pull request bodies
- [`github.com/spf13/cobra`](https://github.com/spf13/cobra) - CLI framework
- [`gopkg.in/yaml.v3`](https://gopkg.in/yaml.v3) - YAML configuration file support

## 🤝 Contributing

Pull requests and issues are welcome!

1. Fork this repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Create a pull request

## 📄 License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.

## 🙏 Acknowledgments

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) - For enabling beautiful TUI experiences
- [Vertex AI](https://cloud.google.com/vertex-ai) - For providing powerful AI capabilities
- [Cobra](https://github.com/spf13/cobra) - For excellent CLI experience

---

**Made with ❤️ by [EkeMinusYou](https://github.com/EkeMinusYou)**
