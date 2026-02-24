package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/logging"
)

// corruptedUserName is the literal string that #456 sets user.name to.
const corruptedUserName = "user.email"

// validateConfigNotCorrupted checks whether local git config has the #456
// corruption pattern (user.name = literal "user.email").
// This catches corruption from external actors (e.g., an AI agent running
// `git config user.name user.email` as a standalone command).
func validateConfigNotCorrupted(ctx context.Context) {
	name := getLocalGitConfigValue("user.name")
	if name == corruptedUserName {
		fmt.Fprintf(os.Stderr,
			"WARNING: .git/config user.name is the literal string \"user.email\" — this is a known "+
				"corruption pattern (see https://github.com/entireio/cli/issues/456). "+
				"Fix with: git config --local user.name \"<your actual name>\"\n",
		)
		logging.Warn(ctx, "detected #456 git config corruption: user.name = literal \"user.email\"")
	}
}

// getLocalGitConfigValue retrieves a git config value from local scope only.
// Returns empty string if the value is not set locally or on error.
func getLocalGitConfigValue(key string) string {
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "git", "config", "--local", "--get", key)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
