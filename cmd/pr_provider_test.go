package cmd

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/EkeMinusYou/gelf/internal/gitea"
	"github.com/EkeMinusYou/gelf/internal/github"
	"github.com/EkeMinusYou/gelf/internal/process"
	"github.com/EkeMinusYou/gelf/internal/testutil"
)

func TestPRProviderDetection(t *testing.T) {
	const teaLogins = `[{"name":"work","url":"https://git.example:3000/gitea","ssh_host":"git-ssh.example"}]`
	const duplicateLogins = `[{"name":"work","url":"https://git.example:3000/gitea"},{"name":"personal","url":"https://git.example:3000/gitea"}]`
	missing := &exec.Error{Name: "missing-cli", Err: exec.ErrNotFound}
	for _, tc := range []struct {
		name, remote, provider, login, logins, ghHosts string
		want, headHost, repoName, wantErr              string
		teaErr, ghErr                                  error
		teaCalls, ghCalls                              int
	}{
		{name: "public GitHub needs no login probes", remote: "git@github.com:owner/repo.git", want: "github", headHost: "github.com"},
		{name: "GitHub over SSH port 443", remote: "ssh://git@ssh.github.com:443/owner/repo.git", want: "github", headHost: "github.com"},
		{name: "Gitea HTTP port and subpath", remote: "https://git.example:3000/gitea/owner/repo.git", logins: teaLogins, want: "gitea", headHost: "git.example:3000", repoName: "owner/repo", teaCalls: 1, ghCalls: 1},
		{name: "Gitea SSH alias", remote: "git@git-ssh.example:owner/repo.git", provider: "auto", logins: teaLogins, want: "gitea", headHost: "git.example:3000", repoName: "owner/repo", teaCalls: 1, ghCalls: 1},
		{name: "Gitea SSH transport port", remote: "ssh://git@git.example:2222/owner/repo.git", logins: teaLogins, want: "gitea", headHost: "git.example:3000", repoName: "owner/repo", teaCalls: 1, ghCalls: 1},
		{name: "Gitea works without gh", remote: "https://git.example:3000/gitea/owner/repo.git", logins: teaLogins, ghErr: missing, want: "gitea", headHost: "git.example:3000", repoName: "owner/repo", teaCalls: 1, ghCalls: 1},
		{name: "Enterprise with no tea", remote: "git@enterprise.example:owner/repo.git", ghHosts: `{"enterprise.example":[{}]}`, teaErr: missing, want: "github", headHost: "enterprise.example", repoName: "enterprise.example/owner/repo", teaCalls: 1, ghCalls: 1},
		{name: "Enterprise HTTP port", remote: "https://enterprise.example:8443/owner/repo.git", ghHosts: `{"enterprise.example:8443":[{}]}`, want: "github", headHost: "enterprise.example:8443", repoName: "enterprise.example:8443/owner/repo", teaCalls: 1, ghCalls: 1},
		{name: "expired Enterprise credentials retain identity", remote: "git@enterprise.example:owner/repo.git", ghHosts: `{"enterprise.example":[{"state":"error"}]}`, want: "github", headHost: "enterprise.example", repoName: "enterprise.example/owner/repo", teaCalls: 1, ghCalls: 1},
		{name: "unrelated gh login is not evidence", remote: "git@unknown.example:owner/repo.git", ghHosts: `{"enterprise.example":[{}]}`, wantErr: "cannot detect", teaCalls: 1, ghCalls: 1},
		{name: "GitHub suffix is not public GitHub", remote: "git@github.com.example:owner/repo.git", wantErr: "cannot detect", teaCalls: 1, ghCalls: 1},
		{name: "unknown host does not fall back to GitHub", remote: "git@unknown.example:owner/repo.git", wantErr: "--provider", teaCalls: 1, ghCalls: 1},
		{name: "overlapping providers require override", remote: "https://git.example:3000/gitea/owner/repo.git", logins: teaLogins, ghHosts: `{"git.example:3000":[{}]}`, wantErr: "both gh and tea", teaCalls: 1, ghCalls: 1},
		{name: "multiple Gitea accounts require login", remote: "https://git.example:3000/gitea/owner/repo.git", logins: duplicateLogins, wantErr: "gitea.login", teaCalls: 1, ghCalls: 1},
		{name: "multiple Gitea accounts are still Gitea evidence", remote: "https://git.example:3000/gitea/owner/repo.git", logins: duplicateLogins, ghHosts: `{"git.example:3000":[{}]}`, wantErr: "both gh and tea", teaCalls: 1, ghCalls: 1},
		{name: "named login selects Gitea account", remote: "https://git.example:3000/gitea/owner/repo.git", logins: duplicateLogins, login: "personal", want: "gitea", headHost: "git.example:3000", repoName: "owner/repo", teaCalls: 1, ghCalls: 1},
		{name: "invalid named login retains Gitea identity", remote: "https://git.example:3000/gitea/owner/repo.git", logins: teaLogins, login: "missing", wantErr: "gitea.login", teaCalls: 1, ghCalls: 1},
		{name: "invalid named login does not hide provider overlap", remote: "https://git.example:3000/gitea/owner/repo.git", logins: teaLogins, login: "missing", ghHosts: `{"git.example:3000":[{}]}`, wantErr: "both gh and tea", teaCalls: 1, ghCalls: 1},
		{name: "explicit GitHub skips detection", remote: "https://git.example:3000/owner/repo.git", provider: "github", logins: teaLogins, want: "github", headHost: "git.example:3000", repoName: "git.example:3000/owner/repo"},
		{name: "explicit Gitea skips GitHub probe", remote: "https://git.example:3000/gitea/owner/repo.git", provider: "gitea", logins: teaLogins, want: "gitea", headHost: "git.example:3000", repoName: "owner/repo", teaCalls: 1},
		{name: "local remote is not a hosted repository", remote: "/tmp/repo.git", wantErr: "unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			teaCalls, ghCalls := 0, 0
			runner := testutil.RunnerFunc(func(ctx context.Context, name string, args []string, body io.Reader) (process.Result, error) {
				if body != nil {
					t.Fatal("provider detection sent a request body")
				}
				switch name {
				case "tea":
					teaCalls++
					if strings.Join(args, " ") != "logins list --output json" {
						t.Fatalf("detection made a tea API call: %v", args)
					}
					data := tc.logins
					if data == "" {
						data = "[]"
					}
					return process.Result{Stdout: data}, tc.teaErr
				case "gh":
					ghCalls++
					if len(args) != 7 || args[0] != "auth" || args[1] != "status" || args[2] != "--hostname" || strings.Join(args[4:], " ") != "--active --json hosts" {
						t.Fatalf("detection made an unexpected gh call: %v", args)
					}
					data := tc.ghHosts
					if data == "" {
						data = "{}"
					}
					return process.Result{Stdout: fmt.Sprintf(`{"hosts":%s}`, data)}, tc.ghErr
				}
				t.Fatalf("unexpected command: %s %v", name, args)
				return process.Result{}, nil
			})
			deps := dependencies{Gitea: gitea.NewClient(runner), GitHub: github.NewClient(runner)}
			cfg := testConfig()
			cfg.PRProvider, cfg.GiteaLogin = tc.provider, tc.login
			host, err := resolvePRHost(context.Background(), deps, cfg, tc.remote)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("host=%+v err=%v, want %q", host, err, tc.wantErr)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				_, isTea := host.Client.(*gitea.Client)
				if isTea != (tc.want == "gitea") || host.Head.Host != tc.headHost || host.RepoName != tc.repoName {
					t.Fatalf("wrong host: %+v head=%+v", host, host.Head)
				}
			}
			if teaCalls != tc.teaCalls || ghCalls != tc.ghCalls {
				t.Fatalf("tea calls=%d gh calls=%d, want %d/%d", teaCalls, ghCalls, tc.teaCalls, tc.ghCalls)
			}
			if deps.Gitea.Login != "" || deps.GitHub.Host != "github.com" || cfg.PRProvider != tc.provider {
				t.Fatal("provider resolution mutated shared dependencies or configuration")
			}
		})
	}
}
