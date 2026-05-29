package cli

import (
	"github.com/spf13/cobra"

	"github.com/entireio/cli/internal/coreapi"
)

// newGrantCmd is the hidden `entire grant` command group: manage access
// grants and org membership on the Entire control plane. Mirrors the three
// grantable resources — org, project, repo — each with add / list /
// remove. Surfaced via `entire labs`.
//
// Grantees are addressed by their identity provider + provider user id
// (e.g. --provider github --provider-user-id 12345), matching the control
// plane's grant model. Handle-based addressing is a follow-up.
func newGrantCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "grant",
		Short:  "Manage Entire access grants and org membership",
		Hidden: true,
	}
	cmd.AddCommand(newGrantOrgCmd())
	cmd.AddCommand(newGrantProjectCmd())
	cmd.AddCommand(newGrantRepoCmd())
	return cmd
}

// --- org membership -------------------------------------------------------

func newGrantOrgCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "org",
		Short: "Manage org membership",
	}
	cmd.AddCommand(newGrantOrgAddCmd())
	cmd.AddCommand(newGrantOrgListCmd())
	cmd.AddCommand(newGrantOrgRemoveCmd())
	return cmd
}

func newGrantOrgAddCmd() *cobra.Command {
	var provider, providerUserID, role string
	cmd := &cobra.Command{
		Use:   "add <org>",
		Short: "Add a member to an org",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			client, err := newCoreClient()
			if err != nil {
				return err
			}
			body := &coreapi.AddOrgMemberInputBody{
				Provider:       provider,
				ProviderUserId: providerUserID,
			}
			if role != "" {
				body.Role = coreapi.NewOptAddOrgMemberInputBodyRole(coreapi.AddOrgMemberInputBodyRole(role))
			}
			m, err := client.AddOrgMember(cmd.Context(), body, coreapi.AddOrgMemberParams{OrgId: args[0]})
			if err != nil {
				return renderCoreError(err)
			}
			return printJSON(cmd.OutOrStdout(), m)
		},
	}
	bindGranteeFlags(cmd, &provider, &providerUserID)
	cmd.Flags().StringVar(&role, "role", "", "org role: owner, admin, or member (default member)")
	return cmd
}

func newGrantOrgListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <org>",
		Short: "List org members",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			client, err := newCoreClient()
			if err != nil {
				return err
			}
			out, err := client.ListOrgMembers(cmd.Context(), coreapi.ListOrgMembersParams{OrgId: args[0]})
			if err != nil {
				return renderCoreError(err)
			}
			return printJSON(cmd.OutOrStdout(), out.Members)
		},
	}
}

func newGrantOrgRemoveCmd() *cobra.Command {
	var provider, providerUserID string
	cmd := &cobra.Command{
		Use:   "remove <org>",
		Short: "Remove a member from an org",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			client, err := newCoreClient()
			if err != nil {
				return err
			}
			if err := client.RemoveOrgMember(cmd.Context(), coreapi.RemoveOrgMemberParams{
				OrgId:          args[0],
				Provider:       provider,
				ProviderUserId: providerUserID,
			}); err != nil {
				return renderCoreError(err)
			}
			cmd.Printf("Removed %s/%s from org %s\n", provider, providerUserID, args[0])
			return nil
		},
	}
	bindGranteeFlags(cmd, &provider, &providerUserID)
	return cmd
}

// --- project grants -------------------------------------------------------

func newGrantProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage project access",
	}
	cmd.AddCommand(newGrantProjectAddCmd())
	cmd.AddCommand(newGrantProjectListCmd())
	cmd.AddCommand(newGrantProjectRemoveCmd())
	return cmd
}

func newGrantProjectAddCmd() *cobra.Command {
	var provider, providerUserID, role, granteeType string
	cmd := &cobra.Command{
		Use:   "add <project>",
		Short: "Grant access to a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			client, err := newCoreClient()
			if err != nil {
				return err
			}
			body := &coreapi.GrantProjectAccessInputBody{
				Provider:       provider,
				ProviderUserId: providerUserID,
				Role:           role,
			}
			if granteeType != "" {
				body.GranteeType = coreapi.NewOptGrantProjectAccessInputBodyGranteeType(coreapi.GrantProjectAccessInputBodyGranteeType(granteeType))
			}
			out, err := client.GrantProjectAccess(cmd.Context(), body, coreapi.GrantProjectAccessParams{ProjectId: args[0]})
			if err != nil {
				return renderCoreError(err)
			}
			return printJSON(cmd.OutOrStdout(), out)
		},
	}
	bindGranteeFlags(cmd, &provider, &providerUserID)
	cmd.Flags().StringVar(&role, "role", "", "project role (required)")
	cmd.Flags().StringVar(&granteeType, "grantee-type", "", "grantee kind: account, org, or team (default account)")
	markRequired(cmd, "role")
	return cmd
}

func newGrantProjectListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <project>",
		Short: "List project members",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			client, err := newCoreClient()
			if err != nil {
				return err
			}
			out, err := client.ListProjectMembers(cmd.Context(), coreapi.ListProjectMembersParams{ProjectId: args[0]})
			if err != nil {
				return renderCoreError(err)
			}
			return printJSON(cmd.OutOrStdout(), out.Members)
		},
	}
}

func newGrantProjectRemoveCmd() *cobra.Command {
	var granteeType, granteeID string
	cmd := &cobra.Command{
		Use:   "remove <project>",
		Short: "Revoke project access from a grantee",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			client, err := newCoreClient()
			if err != nil {
				return err
			}
			if err := client.RevokeProjectAccess(cmd.Context(), coreapi.RevokeProjectAccessParams{
				ProjectId:   args[0],
				GranteeType: granteeType,
				GranteeId:   granteeID,
			}); err != nil {
				return renderCoreError(err)
			}
			cmd.Printf("Revoked %s %s from project %s\n", granteeType, granteeID, args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&granteeType, "grantee-type", "", "grantee kind: account, org, or team (required)")
	cmd.Flags().StringVar(&granteeID, "grantee-id", "", "grantee ULID (required)")
	markRequired(cmd, "grantee-type", "grantee-id")
	return cmd
}

// --- repo grants ----------------------------------------------------------

func newGrantRepoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "Manage repo access",
	}
	cmd.AddCommand(newGrantRepoAddCmd())
	return cmd
}

func newGrantRepoAddCmd() *cobra.Command {
	var provider, providerUserID, role, granteeType string
	cmd := &cobra.Command{
		Use:   "add <repo>",
		Short: "Grant access to a repo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			client, err := newCoreClient()
			if err != nil {
				return err
			}
			body := &coreapi.GrantRepoAccessInputBody{
				Provider:       provider,
				ProviderUserId: providerUserID,
				Role:           role,
			}
			if granteeType != "" {
				body.GranteeType = coreapi.NewOptGrantRepoAccessInputBodyGranteeType(coreapi.GrantRepoAccessInputBodyGranteeType(granteeType))
			}
			out, err := client.GrantRepoAccess(cmd.Context(), body, coreapi.GrantRepoAccessParams{RepoId: args[0]})
			if err != nil {
				return renderCoreError(err)
			}
			return printJSON(cmd.OutOrStdout(), out)
		},
	}
	bindGranteeFlags(cmd, &provider, &providerUserID)
	cmd.Flags().StringVar(&role, "role", "", "repo role (required)")
	cmd.Flags().StringVar(&granteeType, "grantee-type", "", "grantee kind: account, org, or team (default account)")
	markRequired(cmd, "role")
	return cmd
}

// bindGranteeFlags wires the shared --provider / --provider-user-id pair
// that identifies a grantee across the org/project/repo add+remove verbs,
// marking both required.
func bindGranteeFlags(cmd *cobra.Command, provider, providerUserID *string) {
	cmd.Flags().StringVar(provider, "provider", "", "identity provider (e.g. github) (required)")
	cmd.Flags().StringVar(providerUserID, "provider-user-id", "", "provider-specific user id (required)")
	markRequired(cmd, "provider", "provider-user-id")
}
