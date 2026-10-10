package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

const DefaultCommitMaxDiffBytes = 100_000
const DefaultPRMaxDiffBytes = 100_000

// DefaultCommitThinking keeps commit message generation fast; the task rarely benefits from reasoning.
const DefaultCommitThinking = "minimal"

type Config struct {
	ProjectID          string
	Location           string
	FlashModel         string
	ProModel           string
	CommitLanguage     string
	CommitModel        string
	CommitMaxDiffBytes int
	CommitThinking     string
	PRLanguage         string
	PRTitleLanguage    string
	PRBodyLanguage     string
	PRMaxDiffBytes     int
	PRModel            string
	Color              string
}

type FileConfig struct {
	VertexAI struct {
		ProjectID string `yaml:"project_id"`
		Location  string `yaml:"location"`
	} `yaml:"vertex_ai"`
	Model struct {
		Flash string `yaml:"flash"`
		Pro   string `yaml:"pro"`
	} `yaml:"model"`
	Language string `yaml:"language"`
	Color    string `yaml:"color"`
	Commit   struct {
		Model        string `yaml:"model"`
		Language     string `yaml:"language"`
		MaxDiffBytes int    `yaml:"max_diff_bytes"`
		Thinking     string `yaml:"thinking"`
	} `yaml:"commit"`
	PR struct {
		MaxDiffBytes  int    `yaml:"max_diff_bytes"`
		Model         string `yaml:"model"`
		Language      string `yaml:"language"`
		TitleLanguage string `yaml:"title_language"`
		BodyLanguage  string `yaml:"body_language"`
	} `yaml:"pr"`
}

func Load() (*Config, error) {
	// Load from file first (lowest priority)
	fileConfig, err := loadFromFile()
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		fileConfig = &FileConfig{}
	}

	// Environment variables override file config
	projectID := os.Getenv("VERTEXAI_PROJECT")
	if projectID == "" {
		projectID = os.Getenv("GOOGLE_CLOUD_PROJECT")
	}
	if projectID == "" {
		projectID = fileConfig.VertexAI.ProjectID
	}

	location := os.Getenv("VERTEXAI_LOCATION")
	if location == "" {
		location = fileConfig.VertexAI.Location
	}
	if location == "" {
		location = "global"
	}

	// Define model names
	flashModel := fileConfig.Model.Flash
	if flashModel == "" {
		flashModel = "gemini-3.8-flash"
	}

	proModel := fileConfig.Model.Pro
	if proModel == "" {
		proModel = "gemini-3.1-pro-preview"
	}

	// Default language
	defaultLanguage := fileConfig.Language
	if defaultLanguage == "" {
		defaultLanguage = "english"
	}

	// Commit settings
	commitModel := fileConfig.Commit.Model
	if commitModel == "" {
		commitModel = "flash" // default to flash model
	}

	commitLanguage := fileConfig.Commit.Language
	if commitLanguage == "" {
		commitLanguage = defaultLanguage
	}

	commitMaxDiffBytes := fileConfig.Commit.MaxDiffBytes
	if commitMaxDiffBytes <= 0 {
		commitMaxDiffBytes = DefaultCommitMaxDiffBytes
	}

	commitThinking := fileConfig.Commit.Thinking
	if commitThinking == "" {
		commitThinking = DefaultCommitThinking
	}

	// PR settings
	prModel := fileConfig.PR.Model
	if prModel == "" {
		prModel = "pro" // default to pro model
	}

	prLanguage := fileConfig.PR.Language
	if prLanguage == "" {
		prLanguage = defaultLanguage
	}

	// PR title language (defaults to pr.language, then global language)
	prTitleLanguage := fileConfig.PR.TitleLanguage
	if prTitleLanguage == "" {
		prTitleLanguage = prLanguage
	}

	// PR body language (defaults to pr.language, then global language)
	prBodyLanguage := fileConfig.PR.BodyLanguage
	if prBodyLanguage == "" {
		prBodyLanguage = prLanguage
	}

	// Color settings
	color := fileConfig.Color
	if color == "" {
		color = "always" // default to always
	}

	cfg := &Config{
		ProjectID: projectID, Location: location,
		FlashModel: flashModel, ProModel: proModel,
		CommitLanguage: commitLanguage, CommitMaxDiffBytes: commitMaxDiffBytes, CommitThinking: commitThinking,
		PRLanguage: prLanguage, PRTitleLanguage: prTitleLanguage, PRBodyLanguage: prBodyLanguage,
		PRMaxDiffBytes: fileConfig.PR.MaxDiffBytes, Color: color,
	}
	if cfg.PRMaxDiffBytes <= 0 {
		cfg.PRMaxDiffBytes = DefaultPRMaxDiffBytes
	}
	if fileConfig.Commit.MaxDiffBytes < 0 || fileConfig.PR.MaxDiffBytes < 0 {
		return nil, fmt.Errorf("max_diff_bytes must not be negative")
	}
	switch commitThinking {
	case "default", "minimal", "low", "medium", "high":
	default:
		return nil, fmt.Errorf("invalid commit.thinking setting %q: expected default, minimal, low, medium, or high", commitThinking)
	}
	if color != "always" && color != "never" && color != "auto" {
		return nil, fmt.Errorf("invalid color setting %q: expected always, never, or auto", color)
	}
	cfg.CommitModel = cfg.ResolveModel(commitModel)
	cfg.PRModel = cfg.ResolveModel(prModel)
	return cfg, nil
}

func loadFromFile() (*FileConfig, error) {
	// Try to find gelf.yml in current directory, XDG config, or home directory
	configPaths := []string{
		"gelf.yml",
		"gelf.yaml",
	}

	// Add XDG config directory paths
	if xdgConfigHome := os.Getenv("XDG_CONFIG_HOME"); xdgConfigHome != "" {
		configPaths = append(configPaths,
			filepath.Join(xdgConfigHome, "gelf", "gelf.yml"),
			filepath.Join(xdgConfigHome, "gelf", "gelf.yaml"),
		)
	} else if homeDir, err := os.UserHomeDir(); err == nil {
		// Fallback to ~/.config if XDG_CONFIG_HOME is not set
		configPaths = append(configPaths,
			filepath.Join(homeDir, ".config", "gelf", "gelf.yml"),
			filepath.Join(homeDir, ".config", "gelf", "gelf.yaml"),
		)
	}

	// Add home directory paths
	if homeDir, err := os.UserHomeDir(); err == nil {
		configPaths = append(configPaths,
			filepath.Join(homeDir, ".gelf.yml"),
			filepath.Join(homeDir, ".gelf.yaml"),
		)
	}

	var config FileConfig
	for _, path := range configPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("failed to read configuration %s: %w", path, err)
		}

		if err := yaml.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("invalid configuration %s: %w", path, err)
		}
		return &config, nil
	}

	return nil, os.ErrNotExist
}

// UseColor evaluates auto independently for each output stream.
func (c *Config) UseColor(out io.Writer) bool {
	switch c.Color {
	case "never":
		return false
	case "auto":
		f, ok := out.(*os.File)
		return ok && term.IsTerminal(int(f.Fd())) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	default:
		return true
	}
}

func (c *Config) ResolveModel(name string) string {
	switch name {
	case "", "flash":
		return c.FlashModel
	case "pro":
		return c.ProModel
	default:
		return name
	}
}
