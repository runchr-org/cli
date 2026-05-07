package tour

import _ "embed"

// embeddedTour is the pre-rendered workflow tour shipped with the
// binary. Generated at release time by running `entire tour
// --regenerate` (which exercises the agent-driven path) and
// committed alongside the source.
//
// Runtime cost of the regular tour drops from a multi-second agent
// call to a ~50ms file read + glamour render. The tradeoff is that
// the tour reflects the CLI surface as of the last release; adding a
// new top-level command means re-running --regenerate before tagging.
//
//go:embed embedded/tour.md
var embeddedTour string

// firstCaptureTail is appended to the embedded tour when the user is
// in the first-capture stage (Entire enabled, agent installed, no
// committed checkpoints yet). The main tour teaches capabilities
// against captured history; the tail explains that the history will
// appear after the user's next commit.
const firstCaptureTail = `

---

Checkpoints are created automatically once your agent runs and you commit. The search, resume, and rewind capabilities will gain real data after your next commit.`

// setupPromptText is rendered when Entire isn't enabled in the repo.
// Hand-written rather than agent-rendered because the content is
// short, stable, and references a fixed set of commands.
const setupPromptText = `## Get started with Entire

Entire isn't enabled in this repo yet. Run these to set it up:

- ` + "`entire enable`" + ` — Turn on session capture and commit-time checkpointing.
- ` + "`entire login`" + ` — (Optional) Sign in for cloud-side checkpoint search.
- ` + "`entire agent add <agent-name>`" + ` — Install hooks for your agent.

After enabling, re-run ` + "`entire tour`" + ` for the full workflow tour.

https://docs.entire.io/cli`

// agentInstallPromptText is rendered when Entire is enabled but no
// agent hooks are installed. Same rationale as setupPromptText —
// short, stable, hand-written.
const agentInstallPromptText = `## Install agent hooks

Entire is enabled here, but no agent hooks are installed yet.

- ` + "`entire agent list`" + ` — See built-in and external agents.
- ` + "`entire agent add <agent-name>`" + ` — Install hooks so your agent's sessions and commits become checkpoints.

External agents (anything not built in) ship as ` + "`entire-agent-<name>`" + ` binaries on your PATH. See https://github.com/entireio/external-agents.

After installing hooks, re-run ` + "`entire tour`" + ` for the full workflow tour.

https://docs.entire.io/cli`
