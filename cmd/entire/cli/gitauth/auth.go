// Package gitauth resolves transport.AuthMethod for go-git operations.
//
// It checks for git credential helpers (for HTTPS) and SSH agent (for SSH),
// so that go-git fetch/push operations can authenticate without shelling out
// to the git CLI.
package gitauth

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing/transport"
	githttp "github.com/go-git/go-git/v6/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v6/plumbing/transport/ssh"
)

// credentialHelperTimeout is the max time to wait for a credential helper.
const credentialHelperTimeout = 5 * time.Second

// ResolveAuth returns a transport.AuthMethod suitable for the given remote URL.
//
// For HTTPS URLs it attempts to use the git credential helper. For SSH URLs
// (including SCP format) it uses the SSH agent. Returns nil if no auth can be
// resolved, which allows unauthenticated access (e.g. public repos).
func ResolveAuth(ctx context.Context, remoteURL string) transport.AuthMethod { //nolint:ireturn // must return interface for go-git FetchOptions.Auth
	if remoteURL == "" {
		return nil
	}

	if IsSSHURL(remoteURL) {
		return resolveSSHAuth()
	}

	// HTTPS (or other non-SSH)
	return resolveHTTPAuth(ctx, remoteURL)
}

// IsSSHURL returns true if the URL uses SSH protocol.
// Supports SCP format (git@host:path) and ssh:// URLs.
func IsSSHURL(rawURL string) bool {
	// SCP format: git@github.com:org/repo.git
	// Has ":" but no "://" scheme separator.
	if strings.Contains(rawURL, ":") && !strings.Contains(rawURL, "://") {
		return true
	}
	// ssh:// protocol
	return strings.HasPrefix(rawURL, "ssh://")
}

// resolveHTTPAuth runs `git credential fill` to obtain HTTPS credentials.
func resolveHTTPAuth(ctx context.Context, remoteURL string) transport.AuthMethod { //nolint:ireturn // returns *githttp.BasicAuth or nil
	u, err := url.Parse(remoteURL)
	if err != nil {
		return nil
	}

	protocol := u.Scheme
	host := u.Host // includes port if present
	if protocol == "" || host == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, credentialHelperTimeout)
	defer cancel()

	input := fmt.Sprintf("protocol=%s\nhost=%s\n\n", protocol, host)

	cmd := exec.CommandContext(ctx, "git", "credential", "fill")
	cmd.Stdin = strings.NewReader(input)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil
	}

	username, password := parseCredentialOutput(stdout.String())
	if username == "" && password == "" {
		return nil
	}

	return &githttp.BasicAuth{
		Username: username,
		Password: password,
	}
}

// parseCredentialOutput extracts username and password from `git credential fill` output.
func parseCredentialOutput(output string) (string, string) {
	var username, password string
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if k, v, ok := strings.Cut(line, "="); ok {
			switch k {
			case "username":
				username = v
			case "password":
				password = v
			}
		}
	}
	return username, password
}

// RemoteURL returns the first configured URL for the named remote.
// Returns empty string if the remote doesn't exist or has no URLs.
func RemoteURL(repo *git.Repository, remoteName string) string {
	remote, err := repo.Remote(remoteName)
	if err != nil {
		return ""
	}
	cfg := remote.Config()
	if len(cfg.URLs) == 0 {
		return ""
	}
	return cfg.URLs[0]
}

// resolveSSHAuth returns an SSH agent auth method if an agent is available.
func resolveSSHAuth() transport.AuthMethod { //nolint:ireturn // returns *gitssh.PublicKeysCallback or nil
	if os.Getenv("SSH_AUTH_SOCK") == "" {
		return nil
	}

	auth, err := gitssh.NewSSHAgentAuth("git")
	if err != nil {
		return nil
	}
	return auth
}
