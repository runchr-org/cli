package session

// AttachReason describes why a session was (or was not) attached to a
// checkpoint during PostCommit condensation. It is logged at decision time
// and — for attach reasons only — persisted on CommittedMetadata so
// `entire checkpoint explain` and other tooling can answer "why is this
// session here?" without rerunning with debug logs.
//
// Reasons starting with "skip_" are log-only and never persisted; they
// describe the absence of an attachment.
type AttachReason string

const (
	// AttachReasonActiveRecentInteraction: session is ACTIVE and had a recent
	// agent interaction (within activeSessionInteractionThreshold). The
	// PrepareCommitMsg gate already established commit relatedness, so no
	// file overlap check is required.
	AttachReasonActiveRecentInteraction AttachReason = "active_recent_interaction"

	// AttachReasonFileOverlap: session is stale ACTIVE or IDLE/ENDED, but its
	// tracked files overlap with the committed file set AND the file contents
	// match the shadow branch (heuristic match).
	AttachReasonFileOverlap AttachReason = "file_overlap"

	// AttachReasonManual: session was imported via `entire attach` (or
	// `entire review attach`) and is therefore associated with the worktree
	// by explicit user intent. Bypasses the file-overlap and read-only-active
	// heuristics; only the "has new content" gate still applies.
	AttachReasonManual AttachReason = "manual_attach"

	// AttachSkipNoNewContent: no new transcript or files since the last
	// condensation. Log-only.
	AttachSkipNoNewContent AttachReason = "skip_no_new_content"

	// AttachSkipReadOnlyActive: ACTIVE session with no tracked files of its
	// own, and another session already claims the committed files. Avoids
	// attaching read-only agents (e.g. summarize) to unrelated commits.
	// Log-only.
	AttachSkipReadOnlyActive AttachReason = "skip_read_only_active"

	// AttachSkipNoTrackedFiles: stale ACTIVE or IDLE/ENDED session with no
	// tracked files — no overlap evidence available. Log-only.
	AttachSkipNoTrackedFiles AttachReason = "skip_no_tracked_files"

	// AttachSkipNoCommittedOverlap: tracked files exist but none were touched
	// by the commit. Log-only.
	AttachSkipNoCommittedOverlap AttachReason = "skip_no_committed_overlap"

	// AttachSkipContentMismatch: files overlap by path but shadow-branch
	// content does not match the committed content. Log-only.
	AttachSkipContentMismatch AttachReason = "skip_content_mismatch"
)

// IsAttach reports whether this reason resulted in the session being
// attached to the checkpoint. Skip reasons return false.
//
// New AttachReason values must be added to one of the cases below — the
// exhaustive linter will flag forgotten additions.
func (r AttachReason) IsAttach() bool {
	switch r {
	case AttachReasonActiveRecentInteraction, AttachReasonFileOverlap, AttachReasonManual:
		return true
	case AttachSkipNoNewContent,
		AttachSkipReadOnlyActive,
		AttachSkipNoTrackedFiles,
		AttachSkipNoCommittedOverlap,
		AttachSkipContentMismatch:
		return false
	}
	return false
}
