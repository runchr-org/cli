package githubidentity

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strings"
)

var authStatusAccountPattern = regexp.MustCompile(`(?:Logged in to|Failed to log in to) github\.com account ([A-Za-z0-9-]+)`)

// ResolveUsername returns the active local GitHub username from gh auth state.
// It only uses local gh configuration and does not make API calls.
func ResolveUsername(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", "auth", "status", "--hostname", "github.com")
	output, err := cmd.CombinedOutput()
	if username := parseActiveUsername(output); username != "" {
		return username, nil
	}

	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return "", errors.New("GitHub username is unavailable; install GitHub CLI and run `gh auth login -h github.com`")
	}

	return "", errors.New("GitHub username is unavailable; could not determine the active github.com account from local `gh auth status` output")
}

func parseActiveUsername(output []byte) string {
	lines := strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n")
	current := ""
	fallback := ""

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if matches := authStatusAccountPattern.FindStringSubmatch(trimmed); len(matches) == 2 {
			current = matches[1]
			if fallback == "" {
				fallback = current
			}
			continue
		}
		if current != "" && strings.EqualFold(trimmed, "- Active account: true") {
			return current
		}
	}

	return fallback
}
