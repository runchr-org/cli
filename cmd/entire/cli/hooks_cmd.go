package cli

import (
	"context"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	_ "github.com/entireio/cli/cmd/entire/cli/agent/claudecode" // register agent
	_ "github.com/entireio/cli/cmd/entire/cli/agent/cursor"     // register agent
	"github.com/entireio/cli/cmd/entire/cli/agent/external"
	_ "github.com/entireio/cli/cmd/entire/cli/agent/factoryaidroid" // register agent
	_ "github.com/entireio/cli/cmd/entire/cli/agent/geminicli"      // register agent
	_ "github.com/entireio/cli/cmd/entire/cli/agent/opencode"       // register agent

	"github.com/spf13/cobra"
)

func newHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "hooks",
		Short:  "Hook handlers",
		Long:   "Commands called by hooks. These are internal and not for direct user use.",
		Hidden: true, // Internal command, not for direct user use
	}

	// Git hooks are strategy-level (not agent-specific)
	cmd.AddCommand(newHooksGitCmd())

	// Discover external agent plugins from PATH before iterating
	external.DiscoverAndRegister(context.Background())

	// Dynamically add agent hook subcommands
	// Each agent that implements HookSupport gets its own subcommand tree
	for _, agentName := range agent.List() {
		ag, err := agent.Get(agentName)
		if err != nil {
			continue
		}
		if handler, ok := ag.(agent.HookSupport); ok {
			cmd.AddCommand(newAgentHooksCmd(agentName, handler))
		}
	}

	return cmd
}
