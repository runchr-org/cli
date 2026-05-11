## Set up & connect

Turn on Entire and log in so your agent work gets tracked. Install hooks for an agent to start capturing checkpoints alongside your commits.

- `entire enable` — Enable Entire in current repository
- `entire agent add` — Install hooks for an agent
- `entire auth login` — Log in to Entire

## Observe your work

Check what you're working on right now — your activity summary, the active session, and a recap of recent checkpoint milestones.

- `entire activity` — Show your activity overview
- `entire session current` — Show the active session for the current worktree
- `entire recap` — Summarize recent checkpoint activity

## Find & explore checkpoints

Search or list checkpoints by keyword or semantic match. Explain the intent behind any session or commit by pulling up the original prompt, agent response, and files touched.

- `entire checkpoint search` — Search checkpoints using semantic and keyword matching
- `entire checkpoint list` — List checkpoints on the current branch
- `entire checkpoint explain` — Explain a session, commit, or checkpoint

## Switch & resume work

Jump between branches without losing context by resuming a session from its last commit. Attach work that wasn't auto-captured, or rewind interactively to an earlier checkpoint and resume from there.

- `entire session resume` — Switch to a branch and resume its session
- `entire session attach` — Attach an existing agent session
- `entire checkpoint rewind` — Browse checkpoints and rewind your session

## Manage & troubleshoot

Detect and fix stuck sessions, broken metadata branches, or hook misconfiguration with the doctor. Clean up session data and check whether Entire is enabled.

- `entire doctor` — Diagnose and fix session issues
- `entire clean` — Clean up Entire session data
- `entire status` — Show Entire status

## Summarize & dispatch

Generate a dispatch that summarizes your recent agent work — useful for standup, handoff, or your own weekly review.

- `entire dispatch` — Generate a dispatch summarizing recent agent work

## Labs

Entire Labs is where experimental workflows live — try new features before they graduate to the main CLI. Run `entire labs` to see what's available.

- `entire review` — Run configured review skills against the current branch
- `entire learn` — Learn the Entire CLI

## External agents

Entire ships with built-in support for several agents (run `entire agent list` to see them). For anything else, drop an `entire-agent-<name>` binary on your PATH and it shows up alongside the built-ins, ready for `entire agent add`.

https://github.com/entireio/external-agents

## Skills

Entire publishes a curated library of agent skills — slash commands and integrations that drop into Claude Code, Codex, Cursor, OpenCode, and other supported agents.

https://github.com/entireio/skills

## Other commands

- `entire agent list` — List installed and available agents
- `entire agent remove` — Uninstall hooks for an agent
- `entire auth logout` — Log out of Entire
- `entire auth list` — List active API tokens for the authenticated user
- `entire auth revoke` — Revoke an API token by id
- `entire auth status` — Show authentication status
- `entire configure` — Update Entire settings in the current repository
- `entire disable` — Disable Entire in current repository
- `entire doctor bundle` — Produce a diagnostic bundle (zip) for bug reports — secrets are redacted by default
- `entire doctor logs` — Show recent operational logs
- `entire doctor trace` — Show hook performance traces
- `entire plugin install` — Link or copy a plugin executable into the managed directory
- `entire plugin list` — List plugins installed in the managed directory
- `entire plugin remove` — Remove a plugin from the managed directory
- `entire session info` — Show detailed session information
- `entire session list` — List all sessions
- `entire session stop` — Stop one or more active sessions
- `entire version` — Show build information

https://docs.entire.io/cli
