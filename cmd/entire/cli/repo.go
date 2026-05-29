package cli

import (
	"github.com/spf13/cobra"

	"github.com/entireio/cli/internal/coreapi"
)

// newRepoCmd is the hidden `entire repo` command group: control-plane
// repository lifecycle (create, list within a project, get, delete) on the
// Entire control plane. Git content operations (clone, log, diff, …) are
// intentionally out of scope here. Surfaced via `entire labs`.
func newRepoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "repo",
		Short:  "Manage Entire repositories",
		Hidden: true,
	}
	cmd.AddCommand(newRepoCreateCmd())
	cmd.AddCommand(newRepoListCmd())
	cmd.AddCommand(newRepoGetCmd())
	cmd.AddCommand(newRepoDeleteCmd())
	return cmd
}

func newRepoCreateCmd() *cobra.Command {
	var (
		projectID   string
		clusterHost string
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a repository in a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			client, err := newCoreClient()
			if err != nil {
				return err
			}
			body := &coreapi.CreateRepoInputBody{
				Name:      args[0],
				ProjectId: projectID,
			}
			if clusterHost != "" {
				body.ClusterHost = coreapi.NewOptString(clusterHost)
			}
			repo, err := client.CreateRepo(cmd.Context(), body)
			if err != nil {
				return renderCoreError(err)
			}
			return printJSON(cmd.OutOrStdout(), repo)
		},
	}
	cmd.Flags().StringVar(&projectID, "project", "", "owning project ULID (required)")
	cmd.Flags().StringVar(&clusterHost, "cluster-host", "", "public host of the cluster to pin the repo to (defaults to the jurisdiction default)")
	markRequired(cmd, "project")
	return cmd
}

func newRepoListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <project>",
		Short: "List repositories in a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			client, err := newCoreClient()
			if err != nil {
				return err
			}
			out, err := client.ListProjectRepos(cmd.Context(), coreapi.ListProjectReposParams{ProjectId: args[0]})
			if err != nil {
				return renderCoreError(err)
			}
			return printJSON(cmd.OutOrStdout(), out.Repos)
		},
	}
}

func newRepoGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <repo>",
		Short: "Show a repository by ULID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			client, err := newCoreClient()
			if err != nil {
				return err
			}
			repo, err := client.GetRepo(cmd.Context(), coreapi.GetRepoParams{RepoId: args[0]})
			if err != nil {
				return renderCoreError(err)
			}
			return printJSON(cmd.OutOrStdout(), repo)
		},
	}
}

func newRepoDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <repo>",
		Short: "Delete a repository by ULID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			client, err := newCoreClient()
			if err != nil {
				return err
			}
			if err := client.DeleteRepo(cmd.Context(), coreapi.DeleteRepoParams{RepoId: args[0]}); err != nil {
				return renderCoreError(err)
			}
			cmd.Printf("Deleted repo %s\n", args[0])
			return nil
		},
	}
}
