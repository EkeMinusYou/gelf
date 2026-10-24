package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

const DefaultCommitMaxDiffBytes = 100_000
const DefaultPRMaxDiffBytes = 100_000
const DefaultPRMaxSessionLogBytes = 20_000
const DefaultPRSessionLogCount = 3

// DefaultCommitThinking keeps commit message generation fast; the task rarely benefits from reasoning.
const DefaultCommitThinking = "minimal"

const DefaultCommitTimeout = 60 * time.Second
const DefaultAPIVersion = "v1"
const DefaultFlashModel = "gemini-3.8-flash"
const DefaultProModel = "gemini-4-argon"

// Model keeps a model ID and its serving location together through alias resolution.
type Model struct {
	Name     string `yaml:"name"`
	Location string `yaml:"location"`
}

// UnmarshalYAML accepts the original scalar model ID as well as a model/location pair.
func (m *Model) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		*m = Model{Name: node.Value}
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("model must be a model name or a mapping with name and location")
	}
	type plainModel Model
	var value plainModel
	if err := node.Decode(&value); err != nil {
		return err
	}
	*m = Model(value)
	return nil
}

type Config struct {
	ProjectID            string
	Location             string
	APIVersion           string
	FlashModel           Model
	ProModel             Model
	CommitLanguage       string
	CommitModel          Model
	CommitMaxDiffBytes   int
	CommitThinking       string
	CommitTimeout        time.Duration
	CommitCache          bool
	PRLanguage           string
	PRTitleLanguage      string
	PRBodyLanguage       string
	PRMaxDiffBytes       int
	PRSessionLogs        bool
	PRSessionLogCount    int
	PRMaxSessionLogBytes int
	PRModel              Model
	PRProvider           string
	GiteaLogin           string
	Color                string
}

type FileConfig struct {
	VertexAI struct {
		ProjectID  string `yaml:"project_id"`
		Location   string `yaml:"location"`
		APIVersion string `yaml:"api_version"`
	} `yaml:"vertex_ai"`
	Model struct {
		Flash Model `yaml:"flash"`
		Pro   Model `yaml:"pro"`
	} `yaml:"model"`
	Language string `yaml:"language"`
	Color    string `yaml:"color"`
	Gitea    struct {
		Login string `yaml:"login"`
	} `yaml:"gitea"`
	Commit struct {
		Model        string `yaml:"model"`
		Language     string `yaml:"language"`
		MaxDiffBytes int    `yaml:"max_diff_bytes"`
		Thinking     string `yaml:"thinking"`
		Timeout      string `yaml:"timeout"`
		Cache        *bool  `yaml:"cache"`
	} `yaml:"commit"`
	PR struct {
		Provider           string `yaml:"provider"`
		SessionLogs        bool   `yaml:"session_logs"`
		SessionLogCount    *int   `yaml:"session_log_count"`
		MaxSessionLogBytes int    `yaml:"max_session_log_bytes"`
		MaxDiffBytes       int    `yaml:"max_diff_bytes"`
		Model              string `yaml:"model"`
		Language           string `yaml:"language"`
		TitleLanguage      string `yaml:"title_language"`
		BodyLanguage       string `yaml:"body_language"`
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
	apiVersion := fileConfig.VertexAI.APIVersion
	if apiVersion == "" {
		apiVersion = DefaultAPIVersion
	}

	// Define model names
	flashModel := fileConfig.Model.Flash
	if flashModel.Name == "" {
		flashModel.Name = DefaultFlashModel
	}
	if flashModel.Location == "" {
		flashModel.Location = location
	}

	proModel := fileConfig.Model.Pro
	if proModel.Name == "" {
		proModel.Name = DefaultProModel
	}
	if proModel.Location == "" {
		proModel.Location = location
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
	commitTimeout := DefaultCommitTimeout
	if fileConfig.Commit.Timeout != "" {
		commitTimeout, err = time.ParseDuration(fileConfig.Commit.Timeout)
		if err != nil || commitTimeout <= 0 {
			return nil, fmt.Errorf("invalid commit.timeout %q: expected a positive duration (e.g. 60s or 2m)", fileConfig.Commit.Timeout)
		}
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
		ProjectID: projectID, Location: location, APIVersion: apiVersion,
		FlashModel: flashModel, ProModel: proModel,
		CommitLanguage: commitLanguage, CommitMaxDiffBytes: commitMaxDiffBytes, CommitThinking: commitThinking,
		CommitTimeout: commitTimeout,
		CommitCache:   true,
		PRLanguage:    prLanguage, PRTitleLanguage: prTitleLanguage, PRBodyLanguage: prBodyLanguage,
		PRMaxDiffBytes: fileConfig.PR.MaxDiffBytes, Color: color,
		PRProvider: fileConfig.PR.Provider, GiteaLogin: fileConfig.Gitea.Login,
		PRSessionLogs:        fileConfig.PR.SessionLogs,
		PRSessionLogCount:    DefaultPRSessionLogCount,
		PRMaxSessionLogBytes: fileConfig.PR.MaxSessionLogBytes,
	}
	if fileConfig.Commit.Cache != nil {
		cfg.CommitCache = *fileConfig.Commit.Cache
	}
	if cfg.PRProvider == "" {
		cfg.PRProvider = "auto"
	}
	if cfg.PRProvider != "auto" && cfg.PRProvider != "github" && cfg.PRProvider != "gitea" {
		return nil, fmt.Errorf("invalid pr.provider setting %q: expected auto, github, or gitea", cfg.PRProvider)
	}
	if fileConfig.PR.SessionLogCount != nil {
		if *fileConfig.PR.SessionLogCount <= 0 {
			return nil, fmt.Errorf("session_log_count must be greater than zero")
		}
		cfg.PRSessionLogCount = *fileConfig.PR.SessionLogCount
	}
	if cfg.PRMaxSessionLogBytes <= 0 {
		cfg.PRMaxSessionLogBytes = DefaultPRMaxSessionLogBytes
	}
	if fileConfig.PR.MaxSessionLogBytes < 0 {
		return nil, fmt.Errorf("max_session_log_bytes must not be negative")
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

func (c *Config) ResolveModel(name string) Model {
	switch name {
	case "", "flash":
		return c.FlashModel
	case "pro":
		return c.ProModel
	default:
		if name == c.FlashModel.Name {
			return c.FlashModel
		}
		if name == c.ProModel.Name {
			return c.ProModel
		}
		return Model{Name: name, Location: c.Location}
	}
}
