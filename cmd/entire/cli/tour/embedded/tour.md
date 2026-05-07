# Tour content not regenerated

This file is a build-time stub. The release pipeline overwrites it with the
real tour markdown by running:

    mise run tour:regenerate

before invoking GoReleaser. If you're seeing this in a shipped binary it
means the regeneration step was skipped — file an issue.

For the live (slow) tour, run `entire tour --regenerate` in a checkout that
has a TextGenerator-capable agent on PATH (claude, codex, gemini, cursor,
copilot, or an external entire-agent-* plugin). For the live "what's new"
digest, run `entire tour --latest`.

Docs: https://docs.entire.io/cli
