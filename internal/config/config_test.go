package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EkeMinusYou/gelf/internal/testutil"
)

func configEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	for _, name := range []string{"VERTEXAI_PROJECT", "GOOGLE_CLOUD_PROJECT", "VERTEXAI_LOCATION"} {
		t.Setenv(name, "")
	}
	return dir
}

func TestConfigurationDefaultsAndPrecedence(t *testing.T) {
	dir := configEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CommitModel != cfg.FlashModel || cfg.PRModel != cfg.ProModel || cfg.CommitMaxDiffBytes != 100000 || cfg.PRMaxDiffBytes != 100000 || cfg.Color != "always" || cfg.PRSessionLogs || cfg.PRMaxSessionLogBytes != 20000 || cfg.PRSessionLogCount != 3 {
		t.Fatalf("defaults: %+v", cfg)
	}
	testutil.Write(t, dir, "xdg/gelf/gelf.yaml", "language: japanese\nmodel:\n  flash: custom-flash\n  pro: custom-pro\ncommit:\n  model: pro\npr:\n  model: flash\n  body_language: french\n  max_diff_bytes: 256\nvertex_ai:\n  project_id: file-project\n  location: file-location\n")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "fallback-project")
	t.Setenv("VERTEXAI_PROJECT", "preferred-project")
	t.Setenv("VERTEXAI_LOCATION", "env-location")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FlashModel != "custom-flash" || cfg.ProModel != "custom-pro" || cfg.CommitModel != "custom-pro" || cfg.PRModel != "custom-flash" || cfg.ProjectID != "preferred-project" || cfg.Location != "env-location" || cfg.PRTitleLanguage != "japanese" || cfg.PRBodyLanguage != "french" || cfg.PRMaxDiffBytes != 256 {
		t.Fatalf("precedence: %+v", cfg)
	}
	testutil.Write(t, dir, "gelf.yml", "commit:\n  model: direct-model\n")
	cfg, err = Load()
	if err != nil || cfg.CommitModel != "direct-model" || cfg.CommitLanguage != "english" {
		t.Fatalf("project config should replace XDG config: %+v %v", cfg, err)
	}
}

func TestInvalidConfigurationIsAnError(t *testing.T) {
	for _, content := range []string{"commit: [invalid", "color: invalid", "commit:\n  max_diff_bytes: -1", "pr:\n  max_diff_bytes: -1", "pr:\n  max_session_log_bytes: -1", "pr:\n  session_log_count: -1", "pr:\n  session_log_count: 0"} {
		t.Run(content, func(t *testing.T) {
			dir := configEnv(t)
			testutil.Write(t, dir, "gelf.yml", content)
			if _, err := Load(); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	dir := configEnv(t)
	if err := os.Mkdir(filepath.Join(dir, "gelf.yml"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "gelf.yml") {
		t.Fatalf("unreadable config silently ignored: %v", err)
	}
}

func TestSessionLogConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		enabled       bool
		count         int
	}{
		{name: "enabled", content: "pr:\n  session_logs: true\n  session_log_count: 5\n  max_session_log_bytes: 512\n", enabled: true, count: 5},
		{name: "disabled", content: "pr:\n  session_logs: false\n  max_session_log_bytes: 512\n", count: 3},
		{name: "count alone", content: "pr:\n  session_log_count: 2\n  max_session_log_bytes: 512\n", count: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := configEnv(t)
			testutil.Write(t, dir, "gelf.yml", tc.content)
			cfg, err := Load()
			if err != nil || cfg.PRSessionLogs != tc.enabled || cfg.PRSessionLogCount != tc.count || cfg.PRMaxSessionLogBytes != 512 {
				t.Fatalf("session config: %+v %v", cfg, err)
			}
		})
	}
}

func TestColorModes(t *testing.T) {
	out := &bytes.Buffer{}
	for _, mode := range []string{"always", "never", "auto"} {
		cfg := &Config{Color: mode}
		want := mode == "always"
		if got := cfg.UseColor(out); got != want {
			t.Fatalf("mode %s: %v", mode, got)
		}
	}
	t.Setenv("NO_COLOR", "1")
	if (&Config{Color: "auto"}).UseColor(os.Stdout) {
		t.Fatal("auto ignored NO_COLOR")
	}
}
