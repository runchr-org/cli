# Codex — Integration One-Pager

## Verdict: COMPATIBLE

Codex (OpenAI's CLI coding agent) supports lifecycle hooks via `hooks.json` config files with JSON stdin/stdout transport. The hook mechanism closely mirrors Claude Code's architecture (matcher-based hook groups, JSON on stdin, structured JSON output on stdout). Four hook events are available: SessionStart, UserPromptSubmit, Stop, and PreToolUse (shell/Bash only).

## Static Checks

| Check | Result | Notes |
|-------|--------|-------|
| Binary present | PASS | `codex` found on PATH |
| Help available | PASS | `codex --help` shows full subcommand list |
| Version info | PASS | `codex-cli 0.130.0` |
| Hook keywords | PASS | Hook system via `hooks.json` config files |
| Session keywords | PASS | `resume`, `fork` subcommands; session stored as threads in SQLite + JSONL rollout files |
| Config directory | PASS | `~/.codex/` (overridable via `CODEX_HOME`) |
| Documentation | PASS | JSON schemas at `codex-rs/hooks/schema/generated/` |

## Binary

- Name: `codex`
- Version: `codex-cli 0.130.0`
- Install: `npm install -g @openai/codex` or build from source

## Hook Mechanism

- Config file: `.codex/hooks.json` (project-level, in repo root) or `~/.codex/hooks.json` (user-level)
- Config format: JSON
- Config layer stack: System (`~/.codex/`) → Project (`.codex/`) — project takes precedence
- Hook registration: JSON file with `hooks` object containing event arrays of matcher groups

**hooks.json structure:**
```json
{
  "hooks": {
    "SessionStart": [
      {
        "matcher": null,
        "hooks": [
          {
            "type": "command",
            "command": "entire hooks codex session-start",
            "timeout": 30
          }
        ]
      }
    ],
    "UserPromptSubmit": [...],
    "Stop": [...],
    "PreToolUse": [...]
  }
}
```

**Hook handler fields:**
- `type`: `"command"` (shell execution)
- `command`: Shell command string
- `timeout` / `timeoutSec`: Timeout in seconds (default: 600)
- `async`: Boolean — if true, hook runs asynchronously (default: false)
- `statusMessage`: Optional display message during hook execution

**Matcher field:**
- `null` — matches all events
- `"*"` — matches all
- Regex pattern — matches tool names for PreToolUse (e.g., `"^Bash$"`)

### Hook Names and Event Mapping

| Native Hook Name | When It Fires | Entire EventType | Notes |
|-----------------|---------------|-----------------|-------|
| `SessionStart` | Session begins (startup, resume, or clear) | `SessionStart` | Includes `source` field |
| `UserPromptSubmit` | User submits a prompt | `TurnStart` | Includes `prompt` text |
| `Stop` | Agent finishes a turn | `TurnEnd` | Includes `last_assistant_message` |
| `PreToolUse` | Before tool execution | *(pass-through)* | Shell/Bash only for now; no lifecycle action needed |

### Hook Input (stdin JSON)

**All events share common fields:**
- `session_id` (string) — UUID thread ID
- `transcript_path` (string|null) — Path to JSONL rollout file, or null in ephemeral mode
- `cwd` (string) — Current working directory
- `hook_event_name` (string) — Event name constant
- `model` (string) — LLM model name
- `permission_mode` (string) — One of: `default`, `acceptEdits`, `plan`, `dontAsk`, `bypassPermissions`

**SessionStart-specific:**
```json
{
  "session_id": "550e8400-e29b-41d4-a716-446655440000",
  "transcript_path": "/Users/user/.codex/rollouts/01/01/rollout-20260324-550e8400.jsonl",
  "cwd": "/path/to/repo",
  "hook_event_name": "SessionStart",
  "model": "gpt-4.1",
  "permission_mode": "default",
  "source": "startup"
}
```
- `source` (string) — `"startup"`, `"resume"`, or `"clear"`

**UserPromptSubmit-specific:**
```json
{
  "session_id": "...",
  "turn_id": "turn-uuid",
  "transcript_path": "...",
  "cwd": "...",
  "hook_event_name": "UserPromptSubmit",
  "model": "gpt-4.1",
  "permission_mode": "default",
  "prompt": "Create a hello.txt file"
}
```
- `prompt` (string) — User's prompt text
- `turn_id` (string) — Turn-scoped identifier

**Stop-specific:**
```json
{
  "session_id": "...",
  "turn_id": "turn-uuid",
  "transcript_path": "...",
  "cwd": "...",
  "hook_event_name": "Stop",
  "model": "gpt-4.1",
  "permission_mode": "default",
  "stop_hook_active": true,
  "last_assistant_message": "I've created hello.txt."
}
```
- `stop_hook_active` (bool) — Whether Stop hook processing is active
- `last_assistant_message` (string|null) — Agent's final message
- `turn_id` (string) — Turn-scoped identifier

**PreToolUse-specific:**
```json
{
  "session_id": "...",
  "turn_id": "turn-uuid",
  "transcript_path": "...",
  "cwd": "...",
  "hook_event_name": "PreToolUse",
  "model": "gpt-4.1",
  "permission_mode": "default",
  "tool_name": "Bash",
  "tool_input": {"command": "ls -la"},
  "tool_use_id": "tool-call-uuid"
}
```
- Currently only fires for `Bash` tool (shell execution)

### Hook Output (stdout JSON)

All hooks accept optional JSON output on stdout. Empty output is valid.

**Universal fields (all events):**
```json
{
  "continue": true,
  "stopReason": null,
  "suppressOutput": false,
  "systemMessage": "Optional message to display"
}
```

The `systemMessage` field can be used to display messages to the user via the agent (similar to Claude Code's `systemMessage`).

## Transcript

- Location: JSONL "rollout" files in `~/.codex/` (sharded directory structure)
- Path pattern: `~/.codex/rollouts/<shard>/<shard>/rollout-<timestamp>-<thread-id>.jsonl`
- The `transcript_path` field in hook payloads provides the exact path
- Format: JSONL (line-delimited JSON)
- Session ID extraction: `session_id` field from hook payload (UUID format)
- Transcript may be null in `--ephemeral` mode

**Note:** Codex's primary storage is SQLite (`~/.codex/state`), but the JSONL rollout file is the file-based transcript we can read. The `transcript_path` in hook payloads points to this file.

## Config Preservation

- Use read-modify-write on entire `hooks.json` file
- Preserve unknown keys in the `hooks` object (future event types)
- The `hooks.json` is separate from `config.toml` — safe to create/modify independently

## CLI Flags

- Non-interactive prompt: `codex exec "<prompt>"` or `codex exec --dangerously-bypass-approvals-and-sandbox "<prompt>"`
- Interactive mode: `codex` or `codex "<prompt>"` (starts TUI)
- Resume session: `codex resume <session-id>` or `codex resume --last`
- Model override: `-m <model>` or `--model <model>`
- Full-auto mode: `codex exec --full-auto "<prompt>"` (workspace-write sandbox + auto-approve)
- JSONL output: `codex exec --json "<prompt>"` (events to stdout)
- Relevant env vars: `CODEX_HOME` (config dir override), `OPENAI_API_KEY` (API auth)

## Plugin / Skill Invocation

Codex's invocation syntax differs from Claude Code's `/<plugin>:<command>`
form. Three prefixes are used:

| Prefix | Meaning | Example |
|--------|---------|---------|
| `/`    | Codex built-in slash-command (reserved; not extensible by user plugins) | `/review`, `/plugins` (non-exhaustive — see `codex-rs/tui/src/slash_command.rs` for the full set) |
| `@`    | User-installed plugin | `@codex-review-pack` |
| `$`    | Bundled skill within a plugin | `$thorough-review` |

Plugin install surface: `codex plugin marketplace add <url>`, then `codex
plugin marketplace upgrade` / `remove`. (Earlier docs may reference `codex
plugins add` — that subcommand does not exist.)

Skill discovery for codex is currently stubbed in
`cmd/entire/cli/agent/codex/discovery.go`. When implemented, discovered
skills must be returned with their actual codex invocation prefix
(`@plugin-name` or `$skill-name`), never claude's `/<plugin>:<command>`
form. See the curated install hints in
`cmd/entire/cli/agent/skilldiscovery/registry.go` for the existing
per-agent registry pattern.

## Review Performance

Codex review wall-clock varies significantly on identical input (we
observed a 3x spread across sequential runs with the same prompt, scope,
and config). The dominant driver is **codex's reasoning model choosing
how broadly to explore** per turn — not network, caching, or entire's
wrapper.

The biggest controllable lever is `model_reasoning_effort`. Approximate
impact:

| `reasoning_effort` | Behavior |
|---|---|
| `xhigh` | Thorough; expect 4-6 min on a small diff |
| `medium` / `low` | Faster; 1-2 min typical, but variance remains |

### Tuning per-spawn (overrides `~/.codex/config.toml`)

`entire review --agent codex` honors per-agent overrides from the
`review.codex.*` section in `.entire/settings.json` (or clone-local
preferences at `.git/entire/preferences.json`). Both keys are optional:

```json
{
  "review": {
    "codex": {
      "skills": ["/review"],
      "reasoning_effort": "low",
      "model": "gpt-5-mini"
    }
  }
}
```

- `reasoning_effort` → `-c model_reasoning_effort=<level>` flag
- `model` → `-m <model>` flag

Empty values fall back to whatever `~/.codex/config.toml` configures, so
the global codex config remains the default. Users who want fast codex
reviews while keeping `xhigh` globally should set `reasoning_effort` to
`low` or `medium` here.

Codex session transcripts (JSONL rollouts at
`~/.codex/sessions/YYYY/MM/DD/`) record every tool call codex made and
the full token breakdown, which is the right place to start if a review
ran much longer than usual.

## Gaps & Limitations

- **Hooks require feature flag:** The `codex_hooks` feature is `default_enabled: false` (stage: UnderDevelopment). It must be enabled via `--enable codex_hooks` CLI flag, or `features.codex_hooks = true` in `config.toml`, or `-c features.codex_hooks=true`. Without this, hooks.json is ignored entirely.
- **No SessionEnd hook:** Codex does not fire a hook when a session is completely terminated. The `Stop` hook fires at end-of-turn, not end-of-session. This is similar to some other agents — the framework handles this gracefully.
- **PreToolUse is shell-only:** Currently only fires for `Bash` tool (direct shell execution). MCP tools, stdin streaming, and other tool types are not yet hooked. PostToolUse is in review.
- **Transcript may be null:** In `--ephemeral` mode, `transcript_path` is null. The integration should handle this gracefully.
- **No subagent hooks:** No PreTask/PostTask equivalent for subagent spawning.
- **Hook response protocol differs from Claude Code:** Codex uses `systemMessage` (same field name) but also supports `hookSpecificOutput` with `additionalContext` for injecting context into the model. For Entire's purposes, `systemMessage` is sufficient.

## Captured Payloads

- JSON schemas at `codex-rs/hooks/schema/generated/` in the Codex repository
- Hook config structure at `codex-rs/hooks/src/engine/config.rs` in the Codex repository
