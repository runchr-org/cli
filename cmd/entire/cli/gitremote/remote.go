// Package gitremote provides utilities for parsing git remote URLs.
package gitremote

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

// Protocol identifiers for git remotes.
const (
	ProtocolSSH   = "ssh"
	ProtocolHTTPS = "https"
)

// RemoteInfo holds parsed components of a git remote URL.
type RemoteInfo struct {
	Protocol string // "ssh" or "https"
	Host     string // e.g., "github.com"
	Owner    string // e.g., "org"
	Repo     string // e.g., "my-repo" (without .git)
}

// ParseRemoteURL parses a git remote URL into its components.
// Supports:
//   - SSH SCP format: git@github.com:org/repo.git
//   - HTTPS format: https://github.com/org/repo.git
//   - SSH protocol format: ssh://git@github.com/org/repo.git
func ParseRemoteURL(rawURL string) (*RemoteInfo, error) {
	rawURL = strings.TrimSpace(rawURL)

	// SSH SCP format: git@github.com:org/repo.git
	if strings.Contains(rawURL, ":") && !strings.Contains(rawURL, "://") {
		parts := strings.SplitN(rawURL, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid SSH URL: %s", redactURL(rawURL))
		}
		hostPart := parts[0]
		pathPart := parts[1]

		host := hostPart
		if idx := strings.Index(host, "@"); idx >= 0 {
			host = host[idx+1:]
		}

		owner, repo, err := SplitOwnerRepo(pathPart)
		if err != nil {
			return nil, err
		}

		return &RemoteInfo{Protocol: ProtocolSSH, Host: host, Owner: owner, Repo: repo}, nil
	}

	// URL format: https://github.com/org/repo.git or ssh://git@github.com/org/repo.git
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %s", redactURL(rawURL))
	}

	protocol := u.Scheme
	if protocol == "" {
		return nil, fmt.Errorf("no protocol in URL: %s", redactURL(rawURL))
	}
	host := u.Hostname()

	pathPart := strings.TrimPrefix(u.Path, "/")
	owner, repo, err := SplitOwnerRepo(pathPart)
	if err != nil {
		return nil, err
	}

	return &RemoteInfo{Protocol: protocol, Host: host, Owner: owner, Repo: repo}, nil
}

// SplitOwnerRepo splits "org/repo.git" into owner and repo (without .git suffix).
func SplitOwnerRepo(path string) (string, string, error) {
	path = strings.TrimSuffix(path, ".git")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("cannot parse owner/repo from path: %s", path)
	}
	return parts[0], parts[1], nil
}

// GetRemoteURL returns the URL configured for a git remote.
func GetRemoteURL(ctx context.Context, remoteName string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", remoteName)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("remote %q not found", remoteName)
	}
	return strings.TrimSpace(string(output)), nil
}

// GetOriginOwnerRepo extracts the owner and repo from the "origin" remote.
func GetOriginOwnerRepo(ctx context.Context) (owner, repo string, err error) {
	rawURL, err := GetRemoteURL(ctx, "origin")
	if err != nil {
		return "", "", err
	}
	info, err := ParseRemoteURL(rawURL)
	if err != nil {
		return "", "", err
	}
	return info.Owner, info.Repo, nil
}

// redactURL removes credentials from a URL for safe logging.
func redactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "<unparseable>"
	}
	if u.User != nil {
		u.User = url.User("REDACTED")
	}
	return u.String()
}
