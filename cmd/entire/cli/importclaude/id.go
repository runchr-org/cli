// Package importclaude imports pre-existing Claude Code transcripts into Entire
// as read-only, commit-less ("orphaned") checkpoints on the local-only
// entire/imports/v1 ref.
package importclaude

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
)

// DeriveCheckpointID produces a stable 12-hex checkpoint ID for an imported
// turn. Re-importing the same (sessionID, turnUUID) yields the same ID, which
// is how import stays idempotent.
func DeriveCheckpointID(sessionID, turnUUID string) id.CheckpointID {
	sum := sha256.Sum256([]byte(sessionID + "/" + turnUUID))
	hexID := hex.EncodeToString(sum[:6]) // 6 bytes = 12 lowercase hex chars
	return id.MustCheckpointID(hexID)
}
