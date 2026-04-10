package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	checkpointid "github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/session"
	"github.com/entireio/cli/cmd/entire/cli/strategy"
	"github.com/entireio/cli/cmd/entire/cli/trailers"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/spf13/cobra"
)

func newCommitCmd() *cobra.Command {
	var message string

	cmd := &cobra.Command{
		Use:   "commit",
		Short: "Create a git commit with Entire checkpoint headers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if message == "" {
				return errors.New("commit message is required; use -m")
			}

			hash, err := runCommit(cmd.Context(), message)
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s\n", shortCommitHash(hash), firstLine(message))
			return nil
		},
	}

	cmd.Flags().StringVarP(&message, "message", "m", "", "Commit message")
	return cmd
}

func runCommit(ctx context.Context, message string) (plumbing.Hash, error) {
	repo, err := openRepository(ctx)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	finalMessage, checkpointID, err := prepareEntireCommitMessage(ctx, message)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	checkpointID = resolveCommitCheckpointID(finalMessage, checkpointID)

	treeHash, err := buildCommitTreeFromIndex(ctx, repo)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	parents, previousTree, err := getCommitParentsAndTree(repo)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	if treeHash == previousTree {
		return plumbing.ZeroHash, git.ErrEmptyCommit
	}

	author, err := GetGitAuthor(ctx)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	now := time.Now()
	sig := object.Signature{
		Name:  author.Name,
		Email: author.Email,
		When:  now,
	}

	commit := &object.Commit{
		TreeHash:     treeHash,
		ParentHashes: parents,
		Author:       sig,
		Committer:    sig,
		Message:      finalMessage,
	}

	if checkpointID != "" {
		commit.ExtraHeaders = []object.ExtraHeader{
			{Key: trailers.CheckpointHeaderKey, Value: checkpointID},
		}
	}

	obj := repo.Storer.NewEncodedObject()
	if err := commit.Encode(obj); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to encode commit: %w", err)
	}

	hash, err := repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to store commit: %w", err)
	}

	if err := updateCommitHEAD(repo, hash); err != nil {
		return plumbing.ZeroHash, err
	}

	if err := GetStrategy(ctx).PostCommit(ctx); err != nil {
		return plumbing.ZeroHash, err
	}

	return hash, nil
}

func prepareEntireCommitMessage(ctx context.Context, message string) (string, string, error) {
	tmp, err := os.CreateTemp("", "entire-commit-msg-*")
	if err != nil {
		return "", "", fmt.Errorf("failed to create temporary commit message: %w", err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		return "", "", fmt.Errorf("failed to close temporary commit message: %w", err)
	}
	defer os.Remove(tmpPath)

	if err := os.WriteFile(tmpPath, []byte(message), 0o600); err != nil {
		return "", "", fmt.Errorf("failed to write temporary commit message: %w", err)
	}

	strat := GetStrategy(ctx)
	if err := strat.PrepareCommitMsg(ctx, tmpPath, "message"); err != nil {
		return "", "", err
	}
	if err := strat.CommitMsg(ctx, tmpPath); err != nil {
		return "", "", err
	}

	content, err := os.ReadFile(tmpPath) //nolint:gosec // temp file created by this process
	if err != nil {
		return "", "", fmt.Errorf("failed to read temporary commit message: %w", err)
	}

	finalMessage := string(content)
	cpID, found := trailers.ParseCheckpoint(finalMessage)
	if found {
		return finalMessage, cpID.String(), nil
	}

	forcedCheckpointID, err := forceCheckpointIDForActiveSession(ctx)
	if err != nil {
		return "", "", err
	}
	if forcedCheckpointID != "" {
		finalMessage = trailers.FormatCheckpoint(finalMessage, forcedCheckpointID)
		return finalMessage, forcedCheckpointID.String(), nil
	}

	return finalMessage, "", nil
}

func buildCommitTreeFromIndex(ctx context.Context, repo *git.Repository) (plumbing.Hash, error) {
	idx, err := repo.Storer.Index()
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to read git index: %w", err)
	}

	if len(idx.Entries) == 0 {
		return plumbing.ZeroHash, git.ErrEmptyCommit
	}

	entries := make(map[string]object.TreeEntry, len(idx.Entries))
	for _, entry := range idx.Entries {
		entries[entry.Name] = object.TreeEntry{
			Name: entry.Name,
			Mode: entry.Mode,
			Hash: entry.Hash,
		}
	}

	treeHash, err := checkpoint.BuildTreeFromEntries(ctx, repo, entries)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("failed to build commit tree: %w", err)
	}

	return treeHash, nil
}

func getCommitParentsAndTree(repo *git.Repository) ([]plumbing.Hash, plumbing.Hash, error) {
	if strategy.IsEmptyRepository(repo) {
		return nil, plumbing.ZeroHash, nil
	}

	head, err := repo.Head()
	if err != nil {
		return nil, plumbing.ZeroHash, fmt.Errorf("failed to resolve HEAD: %w", err)
	}

	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return nil, plumbing.ZeroHash, fmt.Errorf("failed to resolve HEAD commit: %w", err)
	}

	return []plumbing.Hash{head.Hash()}, commit.TreeHash, nil
}

func updateCommitHEAD(repo *git.Repository, commitHash plumbing.Hash) error {
	head, err := repo.Storer.Reference(plumbing.HEAD)
	if err != nil {
		return fmt.Errorf("failed to read HEAD reference: %w", err)
	}

	name := plumbing.HEAD
	if head.Type() != plumbing.HashReference {
		name = head.Target()
	}

	ref := plumbing.NewHashReference(name, commitHash)
	if err := repo.Storer.SetReference(ref); err != nil {
		return fmt.Errorf("failed to update HEAD: %w", err)
	}

	return nil
}

func shortCommitHash(hash plumbing.Hash) string {
	s := hash.String()
	if len(s) <= 7 {
		return s
	}
	return s[:7]
}

func forceCheckpointIDForActiveSession(ctx context.Context) (checkpointid.CheckpointID, error) {
	worktreeRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		return checkpointid.EmptyCheckpointID, nil
	}

	store, err := session.NewStateStore(ctx)
	if err != nil {
		return checkpointid.EmptyCheckpointID, fmt.Errorf("failed to open session state store: %w", err)
	}

	states, err := store.List(ctx)
	if err != nil {
		return checkpointid.EmptyCheckpointID, fmt.Errorf("failed to list session states: %w", err)
	}

	var eligible []*session.State
	for _, state := range states {
		if state.BaseCommit == "" {
			continue
		}
		if !shouldLinkSessionState(state) {
			continue
		}
		eligible = append(eligible, state)
		if state.WorktreePath != "" && state.WorktreePath == worktreeRoot {
			cpID, genErr := checkpointid.Generate()
			if genErr != nil {
				return checkpointid.EmptyCheckpointID, fmt.Errorf("failed to generate checkpoint ID: %w", genErr)
			}
			return cpID, nil
		}
	}

	if len(eligible) == 1 {
		cpID, genErr := checkpointid.Generate()
		if genErr != nil {
			return checkpointid.EmptyCheckpointID, fmt.Errorf("failed to generate checkpoint ID: %w", genErr)
		}
		return cpID, nil
	}

	return checkpointid.EmptyCheckpointID, nil
}

func shouldLinkSessionState(state *session.State) bool {
	if state == nil {
		return false
	}
	if state.Phase.IsActive() {
		return true
	}
	if !state.FullyCondensed && (state.StepCount > 0 || len(state.FilesTouched) > 0) {
		return true
	}
	return false
}

func resolveCommitCheckpointID(message, checkpointID string) string {
	if checkpointID != "" {
		return checkpointID
	}
	if cpID, found := trailers.ParseCheckpoint(message); found {
		return cpID.String()
	}
	return ""
}
