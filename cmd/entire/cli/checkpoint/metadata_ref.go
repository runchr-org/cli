package checkpoint

import (
	"context"

	"github.com/go-git/go-git/v6/plumbing"

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
