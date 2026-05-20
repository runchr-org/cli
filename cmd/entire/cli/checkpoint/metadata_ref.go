package checkpoint

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"

	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/settings"
)

// MetadataRef returns the plumbing.ReferenceName for v1 metadata storage,
// resolved from settings.
//
// Legacy v1 repos return the branch ref (refs/heads/entire/checkpoints/v1).
// checkpoints_version 1.1 repos return the custom ref
// (refs/entire/checkpoints/v1). Falls back to the legacy branch ref when
// settings cannot be loaded.
func MetadataRef(ctx context.Context) plumbing.ReferenceName {
	if settings.UsesCustomMetadataRef(ctx) {
		return plumbing.ReferenceName(paths.MetadataRefName)
	}
	return plumbing.NewBranchReferenceName(paths.MetadataBranchName)
}

// MetadataTrackingRef returns the plumbing.ReferenceName for the
// remote-tracking counterpart of the v1 metadata ref. Used by the push
// hook's divergence-detection logic and by `entire doctor`.
//
// For legacy v1: refs/remotes/origin/entire/checkpoints/v1.
// For 1.1: refs/entire/remotes/origin/checkpoints/v1.
//
// The 1.1 tracking ref is intentionally NOT the same as the local ref —
// mapping a fetched ref to itself would clobber local writes on every
// fetch. The separate namespace preserves local commits the way the
// standard refs/heads/* ↔ refs/remotes/origin/* mapping does.
func MetadataTrackingRef(ctx context.Context) plumbing.ReferenceName {
	if settings.UsesCustomMetadataRef(ctx) {
		return plumbing.ReferenceName(paths.MetadataTrackingRefName)
	}
	return plumbing.NewRemoteReferenceName("origin", paths.MetadataBranchName)
}

// PreserveV1History initializes the custom ref at the legacy v1 branch's tip
// so prior checkpoint history remains reachable under 1.1. Idempotent; runs
// at most once per repo. Safe to call repeatedly.
//
// Returns nil and is a no-op when:
//   - The repo is not configured for v1.1.
//   - The custom ref already exists (regardless of legacy branch state).
//   - Neither ref exists (no history to preserve; fresh-orphan creation is
//     the caller's responsibility via strategy.EnsureMetadataBranch).
//
// Does NOT create an orphan ref when neither exists — that responsibility
// lives with strategy.EnsureMetadataBranch, which is only called from write
// paths.
func PreserveV1History(ctx context.Context, repo *git.Repository) error {
	if !settings.UsesCustomMetadataRef(ctx) {
		return nil
	}
	target := plumbing.ReferenceName(paths.MetadataRefName)
	if _, err := repo.Reference(target, false); err == nil {
		return nil // already preserved (or freshly created)
	}
	legacy := plumbing.NewBranchReferenceName(paths.MetadataBranchName)
	legacyRef, err := repo.Reference(legacy, false)
	if err != nil {
		// No legacy branch — nothing to preserve. Caller handles missing
		// ref normally (fresh-orphan on write, empty result on read).
		return nil //nolint:nilerr // expected when the repo started fresh on 1.1
	}
	if err := repo.Storer.SetReference(plumbing.NewHashReference(target, legacyRef.Hash())); err != nil {
		return fmt.Errorf("preserve v1 history at custom ref: %w", err)
	}
	logging.Info(ctx, "preserved v1 history at custom ref",
		slog.String("legacy_hash", legacyRef.Hash().String()),
		slog.String("target_ref", string(target)),
	)
	return nil
}
