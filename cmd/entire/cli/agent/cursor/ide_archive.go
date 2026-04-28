package cursor

// Cursor IDE chat archive (export/restore). Stub for the follow-up PR that
// extends PR #1007 (cursor-agent CLI) to also cover the desktop IDE.
//
// See docs/architecture/cursor-ide-archive.md for the full design.
//
// The CLI path remains untouched — that already ships in PR #1007. This file
// will hold the IDE-specific export/restore that reads from
// ~/Library/Application Support/Cursor/User/{global,workspace}Storage/state.vscdb.

import (
	"context"
	"errors"
)

// IDEChatArchiveFilename is the filename used inside a session's metadata
// directory to carry the IDE-side JSONL dump. Sibling of ChatArchiveFilename
// (cursor-agent CLI's archive). Kept distinct because the row schema differs
// (ws-item / bubble / agentblob vs CLI's meta / blob).
const IDEChatArchiveFilename = "cursor-ide-chat.jsonl"

// errIDEArchiveNotImplemented is the placeholder error returned by the IDE
// archive functions until the follow-up PR lands. Callers in the lifecycle
// path treat this like any other contributor failure: log a warn, continue.
var errIDEArchiveNotImplemented = errors.New("cursor IDE archive not yet implemented")

// ExportIDEChatArchive will read the active workspace's chat state out of
// Cursor IDE's two state.vscdb files (global + workspace) and emit a JSONL
// dump suitable for inclusion in a checkpoint.
//
// Not implemented yet — the follow-up PR fills this in. Returning the
// sentinel error keeps the contributor path well-behaved in the meantime.
func ExportIDEChatArchive(_ context.Context, _ string) ([]byte, error) {
	return nil, errIDEArchiveNotImplemented
}

// RestoreIDEChatArchive will replay an IDE archive back into the
// workspaceStorage and globalStorage state.vscdb pair on the current machine.
// Not implemented yet — the follow-up PR fills this in.
func RestoreIDEChatArchive(_ context.Context, _ []byte) error {
	return errIDEArchiveNotImplemented
}
