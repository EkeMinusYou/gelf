package git

import (
	"fmt"
	"os/exec"
	"strings"
)

func GetRemoteURL(remoteName string) (string, error) {
	remoteName = strings.TrimSpace(remoteName)
	if remoteName == "" {
		return "", fmt.Errorf("remote name is empty")
	}

	cmd := exec.Command("git", "remote", "get-url", remoteName)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get remote URL for %s: %w", remoteName, err)
	}

	remoteURL := strings.TrimSpace(string(output))
	if remoteURL == "" {
		return "", fmt.Errorf("remote URL for %s is empty", remoteName)
	}

	return remoteURL, nil
}
