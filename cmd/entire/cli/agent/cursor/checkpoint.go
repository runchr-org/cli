package cursor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ContributeCheckpointFiles exports the Cursor chat store.db as a
// cursor-chat.jsonl archive into the checkpoint metadata directory.
// When cursor has no DB for the session yet (fresh launch before the
// first turn), the archive is skipped silently so we don't block the
// checkpoint save.
func (c *CursorAgent) ContributeCheckpointFiles(ctx context.Context, sessionID string, metadataDir string) error {
	data, err := ExportChatArchive(ctx, sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.WriteFile(filepath.Join(metadataDir, ChatArchiveFilename), data, 0o600); err != nil {
		return fmt.Errorf("writing cursor chat archive: %w", err)
	}
	return nil
}
