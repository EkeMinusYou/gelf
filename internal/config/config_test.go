package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if cfg.APIVersion != "v1" || cfg.CommitModel != cfg.FlashModel || cfg.PRModel != cfg.ProModel || cfg.CommitMaxDiffBytes != 100000 || cfg.PRMaxDiffBytes != 100000 || cfg.CommitThinking != "minimal" || cfg.CommitTimeout != time.Minute || cfg.Color != "always" || cfg.PRSessionLogs || cfg.PRMaxSessionLogBytes != 20000 || cfg.PRSessionLogCount != 3 || cfg.PRProvider != "auto" {
		t.Fatalf("defaults: %+v", cfg)
	}
	if cfg.FlashModel.Name != "gemini-3.8-flash" || cfg.ProModel.Name != "gemini-4-argon" || cfg.FlashModel.Location != "global" || cfg.ProModel.Location != "global" {
		t.Fatalf("default model profiles: %+v %+v", cfg.FlashModel, cfg.ProModel)
	}
	testutil.Write(t, dir, "xdg/gelf/gelf.yaml", "language: japanese\nmodel:\n  flash: custom-flash\n  pro: custom-pro\ncommit:\n  model: pro\n  thinking: high\npr:\n  model: flash\n  body_language: french\n  max_diff_bytes: 256\nvertex_ai:\n  project_id: file-project\n  location: file-location\n")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "fallback-project")
	t.Setenv("VERTEXAI_PROJECT", "preferred-project")
	t.Setenv("VERTEXAI_LOCATION", "env-location")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FlashModel.Name != "custom-flash" || cfg.ProModel.Name != "custom-pro" || cfg.CommitModel.Name != "custom-pro" || cfg.PRModel.Name != "custom-flash" || cfg.ProjectID != "preferred-project" || cfg.Location != "env-location" || cfg.PRTitleLanguage != "japanese" || cfg.PRBodyLanguage != "french" || cfg.PRMaxDiffBytes != 256 || cfg.CommitThinking != "high" {
		t.Fatalf("precedence: %+v", cfg)
	}
	if cfg.FlashModel.Location != "env-location" || cfg.ProModel.Location != "env-location" {
		t.Fatal("legacy model settings did not inherit the location")
	}
	testutil.Write(t, dir, "gelf.yml", "commit:\n  model: direct-model\n")
	cfg, err = Load()
	if err != nil || cfg.CommitModel.Name != "direct-model" || cfg.CommitLanguage != "english" {
		t.Fatalf("project config should replace XDG config: %+v %v", cfg, err)
	}
}

func TestInvalidConfigurationIsAnError(t *testing.T) {
	for _, content := range []string{"commit: [invalid", "color: invalid", "commit:\n  max_diff_bytes: -1", "pr:\n  max_diff_bytes: -1", "pr:\n  max_session_log_bytes: -1", "pr:\n  session_log_count: -1", "pr:\n  session_log_count: 0", "commit:\n  thinking: none", "pr:\n  provider: invalid", "commit:\n  timeout: invalid", "commit:\n  timeout: 0s", "commit:\n  timeout: -1s", "model:\n  flash: [invalid]", "model:\n  pro: 123", "model:\n  flash:\n    location: [invalid]"} {
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

func TestGiteaConfiguration(t *testing.T) {
	dir := configEnv(t)
	testutil.Write(t, dir, "gelf.yml", "pr:\n  provider: gitea\ngitea:\n  login: work\n")
	cfg, err := Load()
	if err != nil || cfg.PRProvider != "gitea" || cfg.GiteaLogin != "work" {
		t.Fatalf("Gitea config: %+v %v", cfg, err)
	}
}

func TestModelProfilesKeepLocationsAcrossCommandsAndOverrides(t *testing.T) {
	dir := configEnv(t)
	testutil.Write(t, dir, "gelf.yml", "model:\n  flash:\n    name: shared-model\n    location: us\n  pro:\n    name: shared-model\n    location: global\ncommit:\n  model: pro\npr:\n  model: flash\n")
	t.Setenv("VERTEXAI_LOCATION", "eu")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CommitModel != (Model{Name: "shared-model", Location: "global"}) || cfg.PRModel != (Model{Name: "shared-model", Location: "us"}) {
		t.Fatalf("model selection lost the location: commit=%+v pr=%+v", cfg.CommitModel, cfg.PRModel)
	}
	if cfg.ResolveModel("flash") != cfg.FlashModel || cfg.ResolveModel("pro") != cfg.ProModel || cfg.ResolveModel("custom") != (Model{Name: "custom", Location: "eu"}) {
		t.Fatal("model override did not retain its endpoint")
	}

	testutil.Write(t, dir, "gelf.yml", "model:\n  flash:\n    location: us\n  pro: legacy-pro\n")
	cfg, err = Load()
	if err != nil || cfg.FlashModel != (Model{Name: DefaultFlashModel, Location: "us"}) || cfg.ProModel != (Model{Name: "legacy-pro", Location: "eu"}) {
		t.Fatalf("mixed scalar/object profiles: cfg=%+v err=%v", cfg, err)
	}
	if cfg.ResolveModel(DefaultFlashModel) != cfg.FlashModel || cfg.ResolveModel("legacy-pro") != cfg.ProModel {
		t.Fatal("explicit model IDs did not retain their configured endpoints")
	}
}

func TestCommitTimeoutConfiguration(t *testing.T) {
	dir := configEnv(t)
	testutil.Write(t, dir, "gelf.yml", "commit:\n  timeout: 2m\nvertex_ai:\n  api_version: v1beta1\n")
	cfg, err := Load()
	if err != nil || cfg.CommitTimeout != 2*time.Minute || cfg.APIVersion != "v1beta1" || cfg.Location != "global" {
		t.Fatalf("timeout=%+v err=%v", cfg, err)
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
