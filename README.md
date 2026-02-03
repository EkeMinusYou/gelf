# 🚀 gelf

gelf is a Go-based CLI tool that automatically generates Git commit messages and provides AI-powered code reviews using Vertex AI (Gemini). It analyzes git changes and provides intelligent feedback through a modern, interactive TUI interface built with Bubble Tea.

## ✨ Features

- 🤖 **AI-Powered**: Intelligent commit message generation using Vertex AI (Gemini)
- 🔍 **Code Review**: AI-powered code review with streaming real-time feedback
- 📝 **PR Creation**: Generate pull request titles and descriptions with AI
- 🎨 **Clean TUI**: Simple and intuitive user interface built with Bubble Tea  
- ⚡ **Fast Processing**: Real-time progress indicators and streaming responses
- 🛡️ **Safe Operations**: Only operates on staged changes for secure workflow
- 🌐 **Cross-Platform**: Works seamlessly across different operating systems
- 🌍 **Multi-language Support**: Generate commit messages and code reviews in multiple languages

## 🛠️ Installation

### Prerequisites

- Go 1.24.3 or higher
- Google Cloud account with Vertex AI API enabled
- Git (required for commit operations)

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
  location: "us-central1"  # optional, default: us-central1

model:
  flash: gemini-2.5-flash
  pro: gemini-2.5-pro

language: "english"  # optional, default: english

commit:
  model: "flash"     # optional, default: flash
  language: "english"  # optional, inherits from global language

review:
  model: "pro"       # optional, default: pro
  language: "english"  # optional, inherits from global language

pr:
  model: "pro"       # optional, default: pro
  language: "english"  # optional, inherits from global language

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

# Vertex AI location (optional, default: us-central1)
export VERTEXAI_LOCATION="us-central1"
```

**Note**: Model configuration and language settings can only be configured via configuration file, not environment variables.

### 2. Google Cloud Authentication

1. Create a service account in Google Cloud Console
2. Grant the "Vertex AI User" role
3. Download the JSON key file
4. Set the `GELF_CREDENTIALS` environment variable to the file path (recommended) or use `GOOGLE_APPLICATION_CREDENTIALS`

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

3. Interactive TUI operations:
   - Review the AI-generated commit message
   - Press `y` to approve or `n` to cancel
   - Press `e` to edit the commit message
   - Press `q` or `Ctrl+C` to cancel during generation
   - The commit will be executed automatically upon approval
   - Success message displays after TUI exits

### Code Review

1. Review unstaged changes:
```bash
gelf review
```

2. Review staged changes:
```bash
gelf review --staged
```

The review feature provides:
   - Real-time streaming AI analysis
   - Comprehensive code review feedback
   - Security vulnerability detection
   - Performance and maintainability suggestions
   - No interactive prompts - displays results directly

### Pull Request Creation

Generate pull requests with AI-generated titles and descriptions based on committed changes:

```bash
gelf pr create
```

Options:
- `--draft` to create a draft PR
- `--dry-run` to print the generated title/body without creating a PR
- `--model` to override the model for PR generation
- `--language` to set the output language

### Documentation Generation

Generate AI-powered documentation for your codebase:

```bash
# Generate README documentation
gelf doc --src . --dst README.md --template readme

# Generate API documentation 
gelf doc --src ./api --dst docs/api.md --template api

# Generate architecture documentation
gelf doc --src . --dst docs/architecture.md --template architecture

# Generate changelog from git history
gelf doc --src . --dst CHANGELOG.md --template changelog

# Generate Go-style documentation
gelf doc --src ./pkg --dst docs/godoc.md --template godoc
```

The documentation feature provides:
   - AI-powered analysis of source code structure
   - Multiple documentation templates (readme, api, changelog, architecture, godoc)
   - Support for multiple output formats (markdown, html, json)
   - Interactive TUI with progress indicators
   - Customizable language output

### Command Options

```bash
# Show help
gelf --help

# Show commit command help
gelf commit --help

# Show review command help
gelf review --help

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

# Review unstaged changes (default)
gelf review

# Review staged changes
gelf review --staged

# Use specific model for review
gelf review --model gemini-2.0-flash-exp

# Disable markdown styling in review output (for plain text)
gelf review --no-style

# Generate code review in a specific language
gelf review --language japanese

# Create a pull request with AI-generated title/body
gelf pr create

# Create a draft pull request
gelf pr create --draft

# Preview generated PR title/body without creating a PR
gelf pr create --dry-run

# Use specific model and language for PR generation
gelf pr create --model gemini-2.0-flash-exp --language japanese

# Generate documentation
gelf doc --src . --dst README.md --template readme

# Generate documentation with specific format
gelf doc --src ./src --dst docs/api.html --template api --format html

# Generate documentation with specific model and language
gelf doc --src . --dst README_JP.md --template readme --model gemini-2.0-flash-exp --language japanese

# Show documentation command help
gelf doc --help
```

## 🌍 Language Support

gelf supports generating commit messages, code reviews, and documentation in multiple languages. You can configure language settings both through configuration files and command-line options.

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
gelf review --language spanish
gelf pr create --language french
gelf doc --src . --dst README.md --template readme --language french

# Use different languages for different operations
gelf commit --language english
gelf review --language japanese
gelf doc --src . --dst docs/README_ES.md --template readme --language spanish
```

#### 2. Configuration File
```yaml
language: "japanese"  # Global default language

commit:
  language: "japanese"  # Language for commit messages

review:
  language: "english"   # Language for code reviews

pr:
  language: "english"   # Language for pull request titles and descriptions

doc:
  language: "english"   # Language for documentation generation
```

#### 3. Defaults
If no language is specified, commit, review, and documentation generation will use English.

### Priority Order
1. Command-line `--language` flag (highest priority)
2. Configuration file command-specific settings (`commit.language`/`review.language`/`pr.language`/`doc.language`)
3. Configuration file global setting (`language`)
4. Default value (`english`)

This allows you to set a global default language, override it for specific commands, and still override everything on a per-command basis.

## 🔧 Technical Specifications

### Architecture

- **Commit Target**: Staged changes only (`git diff --staged`)
- **Review Target**: Both staged (`git diff --staged`) and unstaged (`git diff`) changes
- **AI Provider**: Vertex AI (Gemini models)
- **Default Flash Model**: gemini-2.5-flash
- **Default Pro Model**: gemini-2.5-pro
- **UI Framework**: Bubble Tea (TUI)
- **CLI Framework**: Cobra
- **Streaming**: Real-time AI response streaming for code reviews

### Project Structure

```
cmd/
├── root.go          # Root command definition
├── commit.go        # Commit command implementation
├── review.go        # Review command implementation
├── pr.go            # Pull request command implementation
└── doc.go           # Documentation generation command implementation
internal/
├── git/
│   ├── diff.go      # Git operations (staged and unstaged diffs)
│   └── branch.go    # Branch and commit range helpers
├── github/
│   └── template.go  # GitHub PR template resolution
├── ai/
│   └── vertex.go    # Vertex AI integration (commit messages, code review, and documentation)
├── ui/
│   └── tui.go       # Bubble Tea TUI implementation (commit, review, and documentation)
├── doc/
│   └── analyzer.go  # Source code analysis for documentation generation
└── config/
    └── config.go    # Configuration management (API keys etc)
main.go             # Application entry point
```

## 🎨 User Interface

The application provides a clean, interactive terminal interface:

### Commit Workflow
- Loading indicator while generating commit messages
- Review screen for generated commit messages with approval options
- Success confirmation after successful commits

### Review Workflow  
- Real-time streaming AI analysis display
- Comprehensive code review feedback without interactive prompts

### Documentation Workflow
- Loading indicator while analyzing source code
- Real-time progress updates during AI document generation
- Success confirmation with output file location

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
  location: string       # Vertex AI location (default: us-central1)

model:
  flash: string          # Gemini Flash model to use (default: gemini-2.5-flash)
  pro: string            # Gemini Pro model to use (default: gemini-2.5-pro)

language: string         # Global default language (default: english)

commit:
  model: string          # Model for commits: "flash", "pro", or custom (default: flash)
  language: string       # Language for commit messages (inherits from global if not set)

review:
  model: string          # Model for reviews: "flash", "pro", or custom (default: pro)
  language: string       # Language for code reviews (inherits from global if not set)

pr:
  model: string          # Model for pull requests: "flash", "pro", or custom (default: pro)
  language: string       # Language for pull request titles and descriptions (inherits from global if not set)

doc:
  model: string          # Model for documentation: "flash", "pro", or custom (default: pro)
  language: string       # Language for documentation generation (inherits from global if not set)

color: string            # Color output setting: "always" or "never" (default: always)
```

### Environment Variables

| Variable | Description | Default Value | Required |
|----------|-------------|---------------|----------|
| `GELF_CREDENTIALS` | Path to service account key file (gelf-specific, takes priority) | - | ✅* |
| `GOOGLE_APPLICATION_CREDENTIALS` | Path to service account key file (fallback) | - | ✅* |
| `VERTEXAI_PROJECT` or `GOOGLE_CLOUD_PROJECT` | Google Cloud project ID | - | ✅ |
| `VERTEXAI_LOCATION` | Vertex AI location | `us-central1` | ❌ |

*Either `GELF_CREDENTIALS` or `GOOGLE_APPLICATION_CREDENTIALS` is required. If both are set, `GELF_CREDENTIALS` takes priority.

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

### Available Commands

```bash
go build                     # Build the project
go test ./...                # Run tests
go mod tidy                  # Tidy dependencies
go run main.go commit        # Run commit command in development
go run main.go commit --dry-run  # Run message generation only
go run main.go review        # Run review command in development
go run main.go review --staged  # Run review for staged changes
go run main.go doc --src . --dst README.md --template readme  # Run documentation generation
```

## 📦 Dependencies

### Main Dependencies
- [`google.golang.org/genai`](https://pkg.go.dev/google.golang.org/genai) - Official Gemini Go client
- [`github.com/charmbracelet/bubbletea`](https://github.com/charmbracelet/bubbletea) - TUI framework
- [`github.com/charmbracelet/lipgloss`](https://github.com/charmbracelet/lipgloss) - Styling and layout
- [`github.com/charmbracelet/bubbles`](https://github.com/charmbracelet/bubbles) - TUI components (spinner)
- [`github.com/charmbracelet/glamour`](https://github.com/charmbracelet/glamour) - Markdown rendering for code reviews
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
