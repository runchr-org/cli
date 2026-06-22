package checkpoint

import (
	"context"
	"fmt"
)

// EphemeralWriteRequest is a single shadow-branch (ephemeral) write command.
// The set is closed via the unexported marker; the store dispatches on the
// concrete type, mirroring the persistent WriteRequest union.
type EphemeralWriteRequest interface {
	isEphemeralWriteRequest()
}

// WriteCheckpoint captures working-tree changes as a shadow-branch checkpoint.
// (Former WriteTemporary.)
type WriteCheckpoint WriteEphemeralOptions

// WriteTask records a completed subagent task as a shadow-branch checkpoint.
// (Former WriteTemporaryTask.)
type WriteTask WriteEphemeralTaskOptions

func (WriteCheckpoint) isEphemeralWriteRequest() {}
func (WriteTask) isEphemeralWriteRequest()       {}

// Write dispatches an ephemeral write request to the matching shadow-branch
// operation. The result carries the created (or existing) commit hash.
func (s *ephemeralStore) Write(ctx context.Context, req EphemeralWriteRequest) (WriteEphemeralResult, error) {
	switch r := req.(type) {
	case WriteCheckpoint:
		return s.writeCheckpoint(ctx, WriteEphemeralOptions(r))
	case WriteTask:
		hash, err := s.writeTask(ctx, WriteEphemeralTaskOptions(r))
		return WriteEphemeralResult{CommitHash: hash}, err
	default:
		return WriteEphemeralResult{}, fmt.Errorf("checkpoint: unsupported ephemeral write request %T", req)
	}
}
