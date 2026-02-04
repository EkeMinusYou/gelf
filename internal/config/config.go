package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ProjectID      string
	Location       string
	FlashModel     string
	ProModel       string
	BaseFlashModel string
	BaseProModel   string
	CommitLanguage string
	ReviewLanguage string
	CommitModel    string
	ReviewModel    string
	PRLanguage     string
	PRModel        string
	Color          string
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
		Model    string `yaml:"model"`
		Language string `yaml:"language"`
	} `yaml:"commit"`
	Review struct {
		Model    string `yaml:"model"`
		Language string `yaml:"language"`
	} `yaml:"review"`
	PR struct {
		Model    string `yaml:"model"`
		Language string `yaml:"language"`
	} `yaml:"pr"`
}

func Load() (*Config, error) {
	// Load from file first (lowest priority)
	fileConfig, err := loadFromFile()
	if err != nil {
		// File not found or invalid is not an error - use defaults
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
		location = "us-central1"
	}

	// Define model names
	flashModel := fileConfig.Model.Flash
	if flashModel == "" {
		flashModel = "gemini-2.5-flash"
	}

	proModel := fileConfig.Model.Pro
	if proModel == "" {
		proModel = "gemini-2.5-pro"
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

	// Review settings
	reviewModel := fileConfig.Review.Model
	if reviewModel == "" {
		reviewModel = "pro" // default to pro model
	}

	reviewLanguage := fileConfig.Review.Language
	if reviewLanguage == "" {
		reviewLanguage = defaultLanguage
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

	// Color settings
	color := fileConfig.Color
	if color == "" {
		color = "always" // default to always
	}

	// Resolve actual model names
	var actualFlashModel, actualProModel string
	if commitModel == "flash" {
		actualFlashModel = flashModel
	} else if commitModel == "pro" {
		actualFlashModel = proModel
	} else {
		// Custom model name
		actualFlashModel = commitModel
	}

	if reviewModel == "flash" {
		actualProModel = flashModel
	} else if reviewModel == "pro" {
		actualProModel = proModel
	} else {
		// Custom model name
		actualProModel = reviewModel
	}

	return &Config{
		ProjectID:      projectID,
		Location:       location,
		FlashModel:     actualFlashModel,
		ProModel:       actualProModel,
		BaseFlashModel: flashModel,
		BaseProModel:   proModel,
		CommitLanguage: commitLanguage,
		ReviewLanguage: reviewLanguage,
		CommitModel:    commitModel,
		ReviewModel:    reviewModel,
		PRLanguage:     prLanguage,
		PRModel:        prModel,
		Color:          color,
	}, nil
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
			continue // Try next path
		}

		if err := yaml.Unmarshal(data, &config); err != nil {
			return nil, err
		}
		return &config, nil
	}

	return nil, os.ErrNotExist
}

func (c *Config) UseColor() bool {
	switch c.Color {
	case "never":
		return false
	case "always":
		return true
	default:
		return true
	}
}

func (c *Config) ResolveModel(name string) string {
	switch name {
	case "", "flash":
		if c.BaseFlashModel != "" {
			return c.BaseFlashModel
		}
		return c.FlashModel
	case "pro":
		if c.BaseProModel != "" {
			return c.BaseProModel
		}
		return c.ProModel
	default:
		return name
	}
}
