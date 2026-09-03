package cmd

import "testing"

func TestIsNonFastForwardError(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected bool
	}{
		{
			name: "rejected non-fast-forward from user report",
			output: `To github.com:groove-x/lovot-tools.git
 ! [rejected]        exclude_another-photo-key -> exclude_another-photo-key (non-fast-forward)
error: failed to push some refs to 'github.com:groove-x/lovot-tools.git'
hint: Updates were rejected because the tip of your current branch is behind
hint: its remote counterpart. If you want to integrate the remote changes,
hint: use 'git pull' before pushing again.
hint: See the 'Note about fast-forwards' in 'git push --help' for details.`,
			expected: true,
		},
		{
			name:     "fetch first hint",
			output:   "! [rejected]        main -> main (fetch first)",
			expected: true,
		},
		{
			name:     "branch tip is behind its remote",
			output:   "Updates were rejected because a pushed branch tip is behind its remote",
			expected: true,
		},
		{
			name:     "unrelated git error like auth failure",
			output:   "fatal: Authentication failed for 'https://github.com/...'",
			expected: false,
		},
		{
			name:     "unrelated network error",
			output:   "fatal: unable to access 'https://github.com/...': Could not resolve host",
			expected: false,
		},
		{
			name:     "empty output",
			output:   "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isNonFastForwardError(tt.output)
			if result != tt.expected {
				t.Errorf("isNonFastForwardError() = %v, want %v", result, tt.expected)
			}
		})
	}
}
