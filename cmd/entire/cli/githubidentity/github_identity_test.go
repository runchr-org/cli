package githubidentity

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveUsername_UsesActiveAccountEvenWhenTokenIsInvalid(t *testing.T) {
	installFakeGitHubCLI(t, "#!/bin/sh\ncat <<'EOF'\ngithub.com\n  X Failed to log in to github.com account alishakawaguchi (default)\n  - Active account: true\nEOF\nexit 1\n")

	username, err := ResolveUsername(context.Background())
	require.NoError(t, err)
	require.Equal(t, "alishakawaguchi", username)
}

func TestResolveUsername_ErrorsWhenNoAccountCanBeDetermined(t *testing.T) {
	installFakeGitHubCLI(t, "#!/bin/sh\nprintf 'github.com\\n' >&2\nexit 1\n")

	_, err := ResolveUsername(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "GitHub username")
}

func installFakeGitHubCLI(t *testing.T, script string) {
	t.Helper()

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "gh")
	require.NoError(t, os.WriteFile(binPath, []byte(script), 0o755))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
