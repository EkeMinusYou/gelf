package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type PushTarget struct {
	LocalBranch string
	PushURL     string
	RemoteName  string
	Branch      string
	RemoteRef   string
	HasUpstream bool
}

type PushStatus struct {
	Target    PushTarget
	HeadSHA   string
	RemoteSHA string
	Ahead     int
	Behind    int
}

func (s PushStatus) Diverged() bool   { return s.Ahead > 0 && s.Behind > 0 }
func (s PushStatus) BehindOnly() bool { return s.Ahead == 0 && s.Behind > 0 }
func (s PushStatus) UpToDate() bool   { return s.RemoteSHA != "" && s.Ahead == 0 && s.Behind == 0 }

func (r *Repository) PushTarget(ctx context.Context, branch string) (PushTarget, error) {
	target := PushTarget{Branch: branch, LocalBranch: branch}
	keys := []string{"branch." + branch + ".pushRemote", "remote.pushDefault", "branch." + branch + ".remote"}
	for _, key := range keys {
		value, err := r.config(ctx, key)
		if err != nil {
			return target, err
		}
		if value != "" {
			target.RemoteName = value
			break
		}
	}
	if target.RemoteName == "" {
		target.RemoteName = "origin"
	}
	if target.RemoteName == "." {
		return target, fmt.Errorf("pushing to the local repository is unsupported")
	}
	upstreamRemote, err := r.config(ctx, "branch."+branch+".remote")
	if err != nil {
		return target, err
	}
	mergeRef, err := r.config(ctx, "branch."+branch+".merge")
	if err != nil {
		return target, err
	}
	pushDefault, err := r.config(ctx, "push.default")
	if err != nil {
		return target, err
	}
	if upstreamRemote == target.RemoteName && strings.HasPrefix(mergeRef, "refs/heads/") && pushDefault != "current" {
		target.Branch = strings.TrimPrefix(mergeRef, "refs/heads/")
	}
	target.HasUpstream = upstreamRemote == target.RemoteName && mergeRef == "refs/heads/"+target.Branch
	target.PushURL, err = r.RemoteURL(ctx, target.RemoteName, true)
	if err != nil {
		return target, err
	}
	target.RemoteRef = "refs/remotes/" + target.RemoteName + "/" + target.Branch
	fetchURL, err := r.RemoteURL(ctx, target.RemoteName, false)
	if err != nil {
		return target, err
	}
	if fetchURL != target.PushURL {
		// Keep pull tracking refs intact when a remote has a distinct push URL.
		target.RemoteRef = cachedRef("push", target.PushURL, target.Branch)
	}
	if _, err := r.run(ctx, "check-ref-format", "refs/heads/"+target.Branch); err != nil {
		return target, err
	}
	return target, nil
}

// PushStatus refreshes only the selected remote branch, including deleted branches.
func (r *Repository) PushStatus(ctx context.Context, target PushTarget) (PushStatus, error) {
	status := PushStatus{Target: target}
	head, err := r.refSHA(ctx, "HEAD")
	if err != nil {
		return status, err
	}
	status.HeadSHA = head
	out, err := r.run(ctx, "ls-remote", "--heads", "--", target.PushURL, "refs/heads/"+target.Branch)
	if err != nil {
		return status, fmt.Errorf("failed to inspect push destination: %w", err)
	}
	if strings.TrimSpace(out) == "" {
		if _, err := r.run(ctx, "update-ref", "-d", target.RemoteRef); err != nil {
			return status, err
		}
		return status, nil
	}
	if _, err := r.run(ctx, "fetch", "--no-tags", "--no-write-fetch-head", "--", target.PushURL, "+refs/heads/"+target.Branch+":"+target.RemoteRef); err != nil {
		return status, err
	}
	status.RemoteSHA, err = r.refSHA(ctx, target.RemoteRef)
	if err != nil {
		return status, err
	}
	counts, err := r.run(ctx, "rev-list", "--left-right", "--count", status.HeadSHA+"..."+status.RemoteSHA)
	if err != nil {
		return status, err
	}
	fields := strings.Fields(counts)
	if len(fields) != 2 {
		return status, fmt.Errorf("unexpected ahead/behind counts: %q", counts)
	}
	status.Ahead, err = strconv.Atoi(fields[0])
	if err != nil {
		return status, err
	}
	status.Behind, err = strconv.Atoi(fields[1])
	return status, err
}

func (r *Repository) Push(ctx context.Context, status PushStatus, force bool) error {
	head, err := r.refSHA(ctx, "HEAD")
	if err != nil {
		return err
	}
	branch, err := r.CurrentBranch(ctx)
	if err != nil {
		return err
	}
	if head != status.HeadSHA || branch != status.Target.LocalBranch {
		return fmt.Errorf("current branch changed during PR preparation; run the command again")
	}
	args := []string{"push"}
	if force {
		if !status.Diverged() {
			return fmt.Errorf("force push requires divergent history")
		}
		args = append(args, "--force-with-lease=refs/heads/"+status.Target.Branch+":"+status.RemoteSHA)
	}
	args = append(args, "--", status.Target.RemoteName, status.HeadSHA+":refs/heads/"+status.Target.Branch)
	_, err = r.run(ctx, args...)
	if err != nil {
		return err
	}
	if !status.Target.HasUpstream {
		if _, err := r.run(ctx, "config", "branch."+status.Target.LocalBranch+".remote", status.Target.RemoteName); err != nil {
			return err
		}
		if _, err := r.run(ctx, "config", "branch."+status.Target.LocalBranch+".merge", "refs/heads/"+status.Target.Branch); err != nil {
			return err
		}
	}
	return nil
}
