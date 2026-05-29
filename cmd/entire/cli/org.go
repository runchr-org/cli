package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/entireio/cli/internal/coreapi"
)

// newOrgCmd is the hidden `entire org` command group: create and list
// organizations on the Entire control plane. Surfaced via `entire labs`
// while the control-plane surface matures.
func newOrgCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "org",
		Short:  "Manage Entire organizations",
		Hidden: true,
	}
	cmd.AddCommand(newOrgCreateCmd())
	cmd.AddCommand(newOrgListCmd())
	return cmd
}

func newOrgCreateCmd() *cobra.Command {
	var region string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an organization",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCoreJSON(cmd, func(ctx context.Context, c *coreapi.Client) (any, error) {
				body := &coreapi.CreateOrgInputBody{Name: args[0]}
				if region != "" {
					body.Region = coreapi.NewOptString(region)
				}
				return c.CreateOrg(ctx, body)
			})
		},
	}
	cmd.Flags().StringVar(&region, "region", "", "jurisdiction slug (defaults to the server's home jurisdiction)")
	return cmd
}

func newOrgListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List organizations you can see",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCoreJSON(cmd, func(ctx context.Context, c *coreapi.Client) (any, error) {
				out, err := c.ListOrgs(ctx)
				if err != nil {
					return nil, err
				}
				return out.Orgs, nil
			})
		},
	}
}
