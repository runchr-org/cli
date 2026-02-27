package cursor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// ContributeCheckpointFiles exports the Cursor chat store.db as a
// .cursor-chat.json archive into the checkpoint metadata directory.
func (c *CursorAgent) ContributeCheckpointFiles(ctx context.Context, sessionID string, metadataDir string) error {
	data, err := ExportChatArchive(ctx, sessionID)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(metadataDir, sessionID+".cursor-chat.json"), data, 0o600); err != nil {
		return fmt.Errorf("writing cursor chat archive: %w", err)
	}
	return nil
}
