package trail

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/paths"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/object"
)

const (
	migrationMarkerFile = "DEPRECATED_MOVED_TO_DATABASE_STORAGE"
)

// migrationMarker is the content of the marker file placed on the git branch after migration.
type migrationMarker struct {
	MigratedAt time.Time `json:"migrated_at"`
	TrailCount int       `json:"trail_count"`
	Message    string    `json:"message"`
}

// IsMigrated checks if the entire/trails/v1 branch has been migrated to the API.
func IsMigrated(repo *git.Repository) bool {
	refName := plumbing.NewBranchReferenceName(paths.TrailsBranchName)
	ref, err := repo.Reference(refName, true)
	if err != nil {
		// Try remote tracking branch
		remoteRefName := plumbing.NewRemoteReferenceName("origin", paths.TrailsBranchName)
		ref, err = repo.Reference(remoteRefName, true)
		if err != nil {
			return false // No branch = not migrated (and nothing to migrate)
		}
	}

	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return false
	}

	tree, err := repo.TreeObject(commit.TreeHash)
	if err != nil {
		return false
	}

	_, err = tree.FindEntry(migrationMarkerFile)
	return err == nil
}

// MigrateIfNeeded migrates trails from the git branch to the API if not already done.
// Returns the number of trails migrated, or 0 if already migrated or nothing to migrate.
func MigrateIfNeeded(_ context.Context, apiStore *APIStore, repo *git.Repository) (int, error) { //nolint:unparam // error return reserved for future migration failures
	if IsMigrated(repo) {
		return 0, nil
	}

	gitStore := NewGitStore(repo)
	trails, err := gitStore.List()
	if err != nil {
		// No branch or empty — nothing to migrate, mark as done
		if markErr := placeMigrationMarker(repo, 0); markErr != nil {
			slog.Warn("failed to place migration marker", slog.String("error", markErr.Error()))
		}
		return 0, nil
	}

	if len(trails) == 0 {
		if markErr := placeMigrationMarker(repo, 0); markErr != nil {
			slog.Warn("failed to place migration marker", slog.String("error", markErr.Error()))
		}
		return 0, nil
	}

	// Migrate each trail to the API
	migrated := 0
	for _, m := range trails {
		// Read full trail data (discussion, checkpoints) for future use
		_, discussion, checkpoints, readErr := gitStore.Read(m.TrailID)
		if readErr != nil {
			slog.Warn("failed to read trail for migration, skipping",
				slog.String("trail_id", string(m.TrailID)),
				slog.String("error", readErr.Error()),
			)
			continue
		}

		if writeErr := apiStore.Write(m, discussion, checkpoints); writeErr != nil {
			slog.Warn("failed to migrate trail to API, skipping",
				slog.String("trail_id", string(m.TrailID)),
				slog.String("error", writeErr.Error()),
			)
			continue
		}
		migrated++
	}

	// Place marker on git branch
	if markErr := placeMigrationMarker(repo, migrated); markErr != nil {
		slog.Warn("failed to place migration marker", slog.String("error", markErr.Error()))
	}

	return migrated, nil
}

// placeMigrationMarker writes the MIGRATED_TO_API marker file to the entire/trails/v1 branch.
func placeMigrationMarker(repo *git.Repository, trailCount int) error {
	refName := plumbing.NewBranchReferenceName(paths.TrailsBranchName)
	ref, err := repo.Reference(refName, true)
	if err != nil {
		// Branch doesn't exist — create orphan with just the marker
		return createOrphanWithMarker(repo, trailCount)
	}

	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return fmt.Errorf("read trails branch commit: %w", err)
	}

	marker := migrationMarker{
		MigratedAt: time.Now().UTC(),
		TrailCount: trailCount,
		Message:    "Trails have been migrated to API-based storage. This branch is kept for historical reference.",
	}
	markerJSON, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal migration marker: %w", err)
	}

	blobHash, err := checkpoint.CreateBlobFromContent(repo, markerJSON)
	if err != nil {
		return fmt.Errorf("create marker blob: %w", err)
	}

	// Add marker file to existing tree
	newTreeHash, err := checkpoint.UpdateSubtree(
		repo, commit.TreeHash,
		nil, // root level
		[]object.TreeEntry{{Name: migrationMarkerFile, Mode: filemode.Regular, Hash: blobHash}},
		checkpoint.UpdateSubtreeOptions{MergeMode: checkpoint.MergeKeepExisting},
	)
	if err != nil {
		return fmt.Errorf("update tree with marker: %w", err)
	}

	authorName, authorEmail := checkpoint.GetGitAuthorFromRepo(repo)
	commitHash, err := checkpoint.CreateCommit(repo, newTreeHash, ref.Hash(), "Mark trails as migrated to API", authorName, authorEmail)
	if err != nil {
		return fmt.Errorf("create marker commit: %w", err)
	}

	newRef := plumbing.NewHashReference(refName, commitHash)
	if err := repo.Storer.SetReference(newRef); err != nil {
		return fmt.Errorf("set branch reference: %w", err)
	}
	return nil
}

// createOrphanWithMarker creates the entire/trails/v1 orphan branch with just the marker file.
func createOrphanWithMarker(repo *git.Repository, trailCount int) error {
	marker := migrationMarker{
		MigratedAt: time.Now().UTC(),
		TrailCount: trailCount,
		Message:    "Trails have been migrated to API-based storage.",
	}
	markerJSON, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal migration marker: %w", err)
	}

	blobHash, err := checkpoint.CreateBlobFromContent(repo, markerJSON)
	if err != nil {
		return fmt.Errorf("create marker blob: %w", err)
	}

	entries := map[string]object.TreeEntry{
		migrationMarkerFile: {Name: migrationMarkerFile, Mode: filemode.Regular, Hash: blobHash},
	}
	treeHash, err := checkpoint.BuildTreeFromEntries(repo, entries)
	if err != nil {
		return fmt.Errorf("build marker tree: %w", err)
	}

	authorName, authorEmail := checkpoint.GetGitAuthorFromRepo(repo)
	commitHash, err := checkpoint.CreateCommit(repo, treeHash, plumbing.ZeroHash, "Mark trails as migrated to API", authorName, authorEmail)
	if err != nil {
		return fmt.Errorf("create marker commit: %w", err)
	}

	refName := plumbing.NewBranchReferenceName(paths.TrailsBranchName)
	newRef := plumbing.NewHashReference(refName, commitHash)
	if err := repo.Storer.SetReference(newRef); err != nil {
		return fmt.Errorf("set branch reference: %w", err)
	}
	return nil
}
