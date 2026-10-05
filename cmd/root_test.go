package cmd

import (
	"bytes"
	"runtime/debug"
	"strings"
	"testing"
)

func TestBuildVersion(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abcdef0123456789"}, {Key: "vcs.modified", Value: "true"}}}
	if got := buildVersion(info, "v2.0.0"); got != "v2.0.0" {
		t.Fatal(got)
	}
	if got := buildVersion(info, "dev"); got != "v1.2.3" {
		t.Fatal(got)
	}
	info.Main.Version = "(devel)"
	if got := buildVersion(info, "dev"); got != "abcdef012345-dirty" {
		t.Fatal(got)
	}
	if got := buildVersion(nil, "dev"); got != "dev" {
		t.Fatal(got)
	}
}

func TestVersionAndCompletionsUseConfiguredOutput(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"completion", "bash"}, {"completion", "zsh"}, {"completion", "fish"}, {"completion", "powershell"}} {
		root := NewRootCommand()
		var out bytes.Buffer
		root.SetArgs(args)
		root.SetOut(&out)
		if err := root.Execute(); err != nil || strings.TrimSpace(out.String()) == "" {
			t.Fatalf("%v: %v output=%q", args, err, out.String())
		}
	}
}
