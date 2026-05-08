## Track & Resume Sessions

See all your active and ended sessions, resume from your last commit on any branch, or attach a session that wasn't captured by hooks. Useful when switching between parallel agent efforts or resuming interrupted work.

- `entire activity`
- `entire session list`
- `entire session current`
- `entire session resume`
- `entire session attach`
- `entire session info`

## Find & Understand Prior Work

Search checkpoints using keywords or concepts, then pull up human-readable context explaining what prompted the change and which files it touched. Useful for standup, handoff, or understanding a teammate's work.

- `entire checkpoint search`
- `entire checkpoint list`
- `entire checkpoint explain`

## Rewind & Recover

Interactively rewind your session to an earlier checkpoint, clean up session data for the current commit, or stop an active session. Useful when an agent change went sideways or you want a fresh start.

- `entire checkpoint rewind`
- `entire clean`
- `entire session stop`

## Summarize & Share

Generate a summary of recent checkpoint activity and agent work ready to share for standup, handoff, or your own review.

- `entire recap`
- `entire dispatch`

## Diagnose Issues

Detect session issues and offer to fix them.

- `entire doctor`

## Labs

Experimental workflows live under `entire labs` — try them out to explore capabilities and give feedback before they stabilize.

- `entire labs review` — Run configured review skills against the current branch
- `entire labs tour` — Tour the Entire CLI

## External agents

Entire ships with built-in support for several agents (run 'entire agent list' to see them). For anything else, drop an 'entire-agent-<name>' binary on your PATH and it shows up alongside the built-ins, ready for 'entire agent add'.

https://github.com/entireio/external-agents

## Skills

Entire publishes a curated library of agent skills — slash commands and integrations that drop into Claude Code, Codex, Cursor, OpenCode, and other supported agents.

https://github.com/entireio/skills

## Other commands

- `entire auth list` — List active API tokens for the authenticated user
- `entire auth login` — Log in to Entire
- `entire auth logout` — Log out of Entire
- `entire auth revoke` — Revoke an API token by id
- `entire auth status` — Show authentication status
- `entire agent add` — Install hooks for the specified agent in this repository
- `entire agent list` — List installed and available agents
- `entire agent remove` — Uninstall hooks for the specified agent in this repository
- `entire configure` — Update Entire settings in the current repository
- `entire disable` — Disable Entire in current repository
- `entire enable` — Enable Entire in current repository
- `entire doctor bundle` — Produce a diagnostic bundle (zip) for bug reports
- `entire doctor logs` — Show recent operational logs
- `entire doctor trace` — Show hook performance traces
- `entire plugin install` — Link or copy a plugin executable into the managed directory
- `entire plugin list` — List plugins installed in the managed directory
- `entire plugin remove` — Remove a plugin from the managed directory
- `entire status` — Show whether Entire is currently enabled or disabled
- `entire version` — Show build information

https://docs.entire.io/cli
