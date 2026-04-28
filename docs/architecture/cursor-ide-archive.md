# Cursor IDE Chat Archive

## Status

Draft — design only. PR #1007 ships `cursor-chat.jsonl` export/restore for the
**cursor-agent CLI**. This document scopes the parallel implementation for the
**Cursor IDE** (desktop app).

## Why a separate code path

Cursor-agent CLI and Cursor IDE both register as the same Entire agent
("cursor", `AgentTypeCursor`) and share `.cursor/hooks.json`, but they store
chat data in completely different places.

| | cursor-agent CLI | Cursor IDE |
|---|---|---|
| Chat DB | `~/.cursor/chats/<md5(cwd)>/<agent-id>/store.db` | `~/Library/Application Support/Cursor/User/globalStorage/state.vscdb` (shared across all workspaces) |
| Workspace state | none (DB carries everything) | `~/Library/Application Support/Cursor/User/workspaceStorage/<wsHash>/state.vscdb` (per workspace) |
| Schema | `meta(key,value) + blobs(id,data)` | `ItemTable(key,value) + cursorDiskKV(key,value)`; cursorDiskKV uses `bubbleId:<composerId>:<bubbleId>` and `agentKv:blob:<sha256>` namespaces |
| Workspace identity | `md5(EvalSymlinks(cwd))` | opaque hex hash assigned by Cursor; workspace.json maps it back to `file://<cwd>` |

So while the "v3 JSONL row dump" idea carries over, the source/target paths
and row schema are different enough that the export/restore code lives in a
new file rather than branching on agent kind inside `chatexport.go`.

## Scope

Required functionality on top of PR #1007:

1. Detect whether the active session is a Cursor IDE session vs a cursor-agent
   CLI session (cheap heuristic: presence of a workspace.json mapping to the
   current cwd under Cursor's workspaceStorage).
2. Export Cursor IDE chat state at checkpoint time to a sibling file
   alongside `cursor-chat.jsonl` (e.g. `cursor-ide-chat.jsonl`) — separate
   filename because the schema differs.
3. Restore Cursor IDE chat state on rewind/resume so the user can reopen the
   IDE and pick up the conversation from the composer pane.
4. Preserve cross-folder resume: workspace hash on the destination machine
   will differ; we resolve it dynamically from the current cwd.

Non-goals:

- Migrating Cursor IDE history into cursor-agent CLI format (or vice versa).
- Round-tripping any UI-only state outside the chat composer (extension
  state, recently opened files, etc.).
- Sharing a single archive file between IDE and CLI sessions in the same
  repo. They get separate archives.

## Cursor IDE storage layout

### Global DB (`globalStorage/state.vscdb`)

Contains every chat across every workspace on this machine. Two key
namespaces matter:

- `bubbleId:<composerId>:<bubbleId>` — one row per chat message. Value is
  JSON; for assistant messages it can ride inside a binary envelope around
  inner JSON (same shape pattern we already handle in `redactBlobData`).
- `agentKv:blob:<sha256>` — content-addressed blob store backing the
  composer's Merkle DAG. Same `id == sha256(data)` invariant as the CLI.

### Workspace DB (`workspaceStorage/<wsHash>/state.vscdb`)

Per-workspace UI + agent-service state. Relevant `ItemTable` keys:

- `composer.composerData` — `{"selectedComposerIds":[...], ...}` enumerates
  the composers that belong to this workspace.
- `aiService.prompts` — flat history of user prompts.
- `aiService.generations` — generation-level metadata.
- `workbench.panel.composerChatViewPane.<id>` — pane visibility/size state.

Mapping cwd → wsHash is via `workspace.json` in each workspaceStorage
subfolder; its `folder` key is `file://<absolute path>`. Use
`EvalSymlinks(cwd)` first so symlinked paths (e.g. `/tmp` vs `/private/tmp`
on macOS) match.

## Export

```
ws_path        = $HOME/Library/Application Support/Cursor/User
wsHash         = scan ws_path/workspaceStorage/*/workspace.json for one whose
                 .folder equals "file://" + EvalSymlinks(cwd)
composerIds    = read workspace ItemTable, parse composer.composerData
bubbles        = SELECT * FROM cursorDiskKV
                   WHERE key LIKE 'bubbleId:<composerId>:%'   FOR EACH composerId
referenced_blobs = walk bubble values; collect every "agentKv:blob:<id>" reference
blobs          = SELECT * FROM cursorDiskKV WHERE key IN (referenced_blobs)
ws_state       = SELECT key,value FROM ItemTable
                   WHERE key IN ('composer.composerData', 'aiService.prompts',
                                 'aiService.generations')
                      OR key LIKE 'workbench.panel.composerChatViewPane.<id>'
emit JSONL     →
                 {"t":"ws-item","k":"composer.composerData","v":...}
                 {"t":"ws-item","k":"aiService.prompts","v":[...]}
                 {"t":"bubble","k":"bubbleId:<cid>:<bid>","v":...}
                 {"t":"agentblob","id":"<sha>","data":"<base64>"}
                 ...
```

Each row goes through the same `redactBlobData` we ship for cursor-agent CLI
(which already handles both JSON shapes and cursor-wrapped binary frames
inside bubble values).

## Restore

```
target wsHash  = lookup workspaceStorage on the destination machine for the
                 current cwd (workspace.json scan, same heuristic as export)
                 — falls back to creating a new wsHash dir if no match yet
                 (cursor will pick it up on next launch)
open both DBs with sql.Open("sqlite", ...) and PRAGMA journal_mode=DELETE
                 (same cursor-readability fix we made for cursor-agent CLI)
INSERT OR REPLACE rows into the right table (ItemTable for ws-item lines,
                 cursorDiskKV for bubble + agentblob lines)
do NOT write store.db-wal or -shm — Cursor recreates them on next open
```

Restore is purely additive at the bubble / blob level (rows are scoped by
composerId and content-hash respectively), so we never clobber other
workspaces' chats sharing the same global DB.

## Reused from PR #1007

- `redactBlobData` (JSON walk + binary fallback for cursor-wrapped frames)
- `journal_mode=DELETE` quirk so cursor reads the main DB file
- JSONL row format and `writeExtraFilesToEntries` helper in checkpoint pkg
- `validateExtraFileRelPath` for filename safety

## Hooks parity

`.cursor/hooks.json` wires both binaries today; lifecycle events
(`session-start`, `before-submit-prompt`, `stop`, `session-end`) fire from
both. Open question: payload identifiers. The CLI passes its agent-id in
the hook stdin; the IDE may pass a different ID (composerId? chat view
pane GUID?). Confirm during implementation and add a translation step if
needed.

## Unknowns to resolve before merging

- Does cursor IDE actually fire our hooks for every chat turn, or only for
  cursor-agent invocations launched from inside the IDE? Verify by enabling
  Entire on a fresh repo and using IDE composer.
- Are bubble values ever encrypted (vs the cleartext-with-binary-prefix
  envelope we already handle)?
- Is `agentKv:blob:*` deduplicated globally? If yes, our archive will ship
  the same blob bytes across many checkpoints; git pack handles this for us
  but worth checking the per-checkpoint cost.

## Test plan

1. Unit: seeded vscdb fixtures roundtrip the JSONL.
2. Manual E2E: drive Cursor IDE composer in a test repo, commit, clone to a
   second folder, `entire resume`, reopen IDE → composer pane shows the
   prior conversation.
3. Secret-leak test (mirror PR #1007's): paste a fake API key into a chat
   bubble, archive, search v1 + restored DB for the raw secret → must be
   zero hits, REDACTED placeholder must appear.

## Estimated size

~250 LOC for export/restore split between a new `ide_archive.go` and
`ide_restore.go` in `cmd/entire/cli/agent/cursor/`, plus ~50 LOC for
workspace-hash lookup, plus ~100 LOC of tests. Reuses existing helpers.
