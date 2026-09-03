package git

import (
	"fmt"
	"os/exec"
	"strings"
)

type PushStatus struct {
	HasUpstream   bool
	UpstreamRef   string
	RemoteName    string
	RemoteRef     string
	HeadPushed    bool
	NeedForcePush bool
}

func GetPushStatus(branch string) (PushStatus, error) {
	status := PushStatus{}

	upstreamRef, hasUpstream, err := getUpstreamRef()
	if err != nil {
		return status, err
	}

	if hasUpstream {
		status.HasUpstream = true
		status.UpstreamRef = upstreamRef
		status.RemoteRef = upstreamRef
		status.RemoteName = remoteNameFromRef(upstreamRef)
		if err := compareRefStatus(upstreamRef, &status); err != nil {
			return status, err
		}
		return status, nil
	}

	status.RemoteName = "origin"
	status.RemoteRef = fmt.Sprintf("origin/%s", branch)

	exists, err := remoteBranchExists(status.RemoteRef)
	if err != nil {
		return status, err
	}
	if !exists {
		return status, nil
	}

	if err := compareRefStatus(status.RemoteRef, &status); err != nil {
		return status, err
	}
	return status, nil
}

func compareRefStatus(remoteRef string, status *PushStatus) error {
	headSHA, err := getRefSHA("HEAD")
	if err != nil {
		return err
	}
	remoteSHA, err := getRefSHA(remoteRef)
	if err != nil {
		return err
	}

	if headSHA == remoteSHA {
		status.HeadPushed = true
		return nil
	}

	status.HeadPushed = false
	canFF, err := isAncestor(remoteRef, "HEAD")
	if err == nil && !canFF {
		status.NeedForcePush = true
	}
	return nil
}

func getRefSHA(ref string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--verify", ref)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get sha for ref %s: %w", ref, err)
	}
	return strings.TrimSpace(string(output)), nil
}

func getUpstreamRef() (string, bool, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 128 {
				return "", false, nil
			}
		}
		return "", false, fmt.Errorf("failed to determine upstream branch: %w", err)
	}

	ref := strings.TrimSpace(string(output))
	if ref == "" {
		return "", false, nil
	}

	return ref, true, nil
}

func isAncestor(ancestorRef, descendantRef string) (bool, error) {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", ancestorRef, descendantRef)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				return false, nil
			}
		}
		return false, fmt.Errorf("failed to compare git refs: %w", err)
	}

	return true, nil
}

func remoteBranchExists(remoteRef string) (bool, error) {
	ref := fmt.Sprintf("refs/remotes/%s", remoteRef)
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", ref)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				return false, nil
			}
		}
		return false, fmt.Errorf("failed to check remote branch: %w", err)
	}

	return true, nil
}

func remoteNameFromRef(ref string) string {
	parts := strings.SplitN(ref, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return "origin"
	}
	return parts[0]
}
