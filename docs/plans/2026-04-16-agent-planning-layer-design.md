# Agent Planning Layer: Planner + Executor Feedback on Top of Memory

## Summary

This document specifies a planning layer that sits on top of the agent memory system ([2026-04-16-agent-memory-lessons-experiences-design.md](./2026-04-16-agent-memory-lessons-experiences-design.md)). It introduces:

- A **plan artifact** — a stored, revisable, step-structured document tied to one goal.
- A **Planner role** — a CLI command (`entire plan <goal>`) and a thin agent skill that drafts plans by consuming retrieved memory.
- An **Executor feedback path** — step advancement from agent turns, commits, and explicit user action, with outcome evaluation flowing back into memory.

The target architecture is a three-role model aligned with MIA's Manager / Planner / Executor decomposition, but without adopting MIA's runtime separation or reinforcement-learning machinery:

- **Manager** = the memory layer from the prior doc (lessons + experiences, retrieval, injection).
- **Planner** = `entire plan` and its stored artifacts. A plan is a compact, memory-grounded strategy document that the agent consumes and revises.
- **Executor** = the existing agent (Claude Code, Gemini, Cursor, Droid, Copilot, …) running inside its own runtime. Entire does not execute code itself.

Plans are **local-primary**: the plan artifact, its steps, and its revisions live in the local CLI database as source of truth. This is an intentional asymmetry with memory (which is server-primary with a local cache): plans are per-device workflow state, not cross-device knowledge. The only server interaction for a plan is a **completion signal** — when a plan terminates with `outcome=success`, the CLI posts a compact summary to the server so repeated completed plans can be considered for experience derivation alongside checkpoint-based evidence. Memory outcome feedback is not plan-driven — the server already computes memory outcomes from session signal recurrence (see memory doc §8), which reflects whatever the plan caused the agent to do.

The feedback loop is therefore closed by two independent paths: (1) session signals already flowing server-side update memory outcomes naturally, and (2) completed-plan summaries contribute to experience derivation as an additional evidence source.

Target: external beta, opt-in, shipped on top of the memory layer. Defaults once enabled: `mode=auto`, `policy=review`, plan activation is `review`.

---

## 1. Context

### Why a planning layer

Memory retrieval answers "what should the agent remember?" and "how was this solved before?" It does not answer "what is the current strategy for this goal, and which step are we on?" Today that context lives only in the ephemeral turn prompt; it cannot be revised deliberately, cannot be linked to outcomes, and cannot accumulate across sessions.

A plan artifact makes strategy a first-class object:

- **Queryable** — `entire plan list`, `entire plan show <id>`.
- **Revisable** — revisions are logged with reason and actor, not silently overwritten.
- **Linkable** — plans reference the memories that informed them and the sessions/checkpoints that executed them.
- **Outcome-bearing** — a plan is the right granularity for asking "did this strategy work?", which is closer to the question users actually care about than "did this individual memory reduce friction?"

### Why now

The memory layer is the prerequisite. Without it, a plan would be drafted from thin air or from the current session transcript alone — exactly the state we have today and exactly what MIA argues is too shallow. Once lessons and experiences exist, plans become *grounded* artifacts: each step can cite the memory it came from, and the same retrieval pipeline that feeds TurnStart feeds the Planner.

### What this doc does not do

- It does not replace the Executor with a runtime owned by Entire. Agents remain external.
- It does not introduce an RL training loop.
- It does not promote plans above lessons or experiences as the canonical memory unit. Plans are a new artifact type, not a replacement.
- It does not redesign anything in the memory doc. All schemas, lifecycle states, and hardening rules from that doc hold.

---

## 2. Goals and Non-Goals

### Goals

- **Plans as artifacts, not a runtime.** A plan is a row in the existing `memory-loop.db` with an ordered list of steps, revisions, and an outcome. No new service, no new process.
- **Grounded drafting.** Plan drafting uses the same retrieval path as TurnStart injection, so plans inherit the hardening and explainability of the memory layer.
- **First-class revision.** Plans are expected to change mid-flight. Revisions are logged with reason and actor; no silent overwrites.
- **Multi-signal step advancement.** Steps advance via explicit CLI action, commit-message trailers, or agent-claim through the Stop hook. No single source of truth.
- **Feedback closes the loop.** Plan outcomes update linked memories' outcomes, and repeated completed plans are eligible to derive playbook-shaped experiences.
- **Kill-switch parity.** The entire planning layer can be toggled off at runtime, reverting to today's behavior.

### Non-Goals

- No separate Planner runtime or planner service.
- No RL-based plan optimization.
- No agent-agnostic "execution DSL" — steps remain natural-language descriptions with optional structured criteria. Agents read them; they are not a programming language.
- No auto-drafting on every user prompt. Plans are drafted on explicit request (`entire plan <goal>`) or when a configured skill invokes the command. TurnStart never drafts.
- No plan sync across contributors in beta. Plans may contain sensitive goal context and arbitrary step text. Sharing requires a redaction design separate from this doc.

---

## 3. Three-Role Model

```
┌─────────────────────────────────────────────────────────────────────┐
│                          Entire (local)                             │
│                                                                     │
│   ┌──────────────┐     retrieves      ┌──────────────────────────┐  │
│   │   Manager    │ ─────────────────► │        Planner           │  │
│   │ (memory DB)  │                    │ (plan artifact + drafts) │  │
│   └──────┬───────┘                    └──────────┬───────────────┘  │
│          │ TurnStart injection                   │ writes plan      │
│          │ <active-plan> + lessons +             │                  │
│          │ <prior-solve-path>                    ▼                  │
│          │                               ┌───────────────────────┐  │
│          └──────────────────────────────►│    Executor (agent)   │  │
│                                          │   Claude / Gemini /   │  │
│                                          │   Cursor / Droid /    │  │
│                                          │   Copilot / Vogon     │  │
│                                          └──────────┬────────────┘  │
│                                                     │ hooks         │
│                                                     │ (PostCommit,  │
│                                                     │  Stop, etc.)  │
│                                                     ▼               │
│   ┌──────────────┐       feedback      ┌──────────────────────────┐ │
│   │   Manager    │ ◄───────────────────│   Step advancement +     │ │
│   │  (outcomes)  │                     │   outcome evaluation     │ │
│   └──────────────┘                     └──────────────────────────┘ │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Role boundaries

| Role | Owns | Does not own |
|---|---|---|
| Manager (server + local cache) | Memory extraction, source-of-truth memory storage, outcome computation from session signals | Plans, plan outcomes |
| Planner (local only) | Plan artifact, drafting, storage, revision, step advancement logic, local plan outcome | Memory retrieval (reads the cache), actually performing the work |
| Executor (agent) | Running tools, editing files, producing commits | Deciding strategy; owning memory; writing to plan tables |

The agent is the Executor and only the Executor. Its outputs are observed by Entire through hooks (as today). It never writes directly to memory or to the plan table; it can *propose* advancement or revision via the CLI, which is mediated by Entire.

### Where things live

| Artifact | Location | Notes |
|---|---|---|
| Lessons, experiences (source of truth) | Server | Synced into local cache via memory-loop refresh |
| Local memory cache | `.entire/memory-loop.db` — `memories_cache`, `experiences_cache` | Read path for plan drafting and injection |
| Plan, plan_steps, plan_revisions | `.entire/memory-loop.db` — primary, not a cache | Per-device; no cross-device sync |
| Plan completion signal | Posted to server via `POST /plans/completions` | Only server-side trace of a plan; used for experience derivation |
| Memory outcomes | Computed server-side from session signals | Flow back into local cache via refresh; not driven by plans |

---

## 4. Plan Artifact

### Schema

Three new **local-primary** tables in `.entire/memory-loop.db`, alongside the memory cache (`memories_cache`, `experiences_cache`). These tables are not cached copies of server state — they are the authoritative record for the plan. No outbox is needed for plan lifecycle writes because nothing server-side is expecting them. The only outbox entry a plan ever generates is the plan-completion signal (Section 10).

```sql
plans (
  id                            TEXT PRIMARY KEY,
  goal                          TEXT NOT NULL,
  scope_kind                    TEXT NOT NULL,        -- 'me', 'repo', 'branch'
  scope_value                   TEXT,
  status                        TEXT NOT NULL,        -- draft, active, paused, completed, abandoned
  origin                        TEXT NOT NULL,        -- 'manual', 'generated'
  owner_id                      TEXT,
  agent                         TEXT,                 -- agent name at activation, if any
  source_signal                 TEXT,                 -- friction pattern this plan addresses
  retrieved_lesson_ids_json     TEXT NOT NULL,        -- snapshot at draft time
  retrieved_experience_ids_json TEXT NOT NULL,        -- snapshot at draft time
  session_ids_json              TEXT NOT NULL,        -- linked sessions
  checkpoint_ids_json           TEXT NOT NULL,        -- linked checkpoints
  outcome                       TEXT DEFAULT 'pending', -- pending, success, partial, failed, abandoned
  created_at                    TEXT NOT NULL,
  activated_at                  TEXT,
  completed_at                  TEXT,
  updated_at                    TEXT NOT NULL,
  last_injected_at              TEXT
)

plan_steps (
  id                     TEXT PRIMARY KEY,
  plan_id                TEXT NOT NULL REFERENCES plans(id),
  ordinal                INTEGER NOT NULL,
  description            TEXT NOT NULL,
  success_criteria       TEXT,
  avoid_notes            TEXT,
  source_memory_ids_json TEXT NOT NULL,               -- {lesson|experience}: id
  status                 TEXT NOT NULL,               -- pending, active, done, skipped, failed
  advancement_signal     TEXT,                        -- 'explicit', 'commit', 'agent-claim', 'user-claim'
  advancement_detail     TEXT,                        -- commit SHA, session id, etc.
  started_at             TEXT,
  completed_at           TEXT,
  UNIQUE (plan_id, ordinal)
)

plan_revisions (
  id       INTEGER PRIMARY KEY AUTOINCREMENT,
  plan_id  TEXT NOT NULL REFERENCES plans(id),
  at       TEXT NOT NULL,
  type     TEXT NOT NULL,        -- 'add_step', 'remove_step', 'reorder', 'rewrite_goal',
                                 -- 'edit_step', 'abandon', 'resume'
  detail   TEXT NOT NULL,        -- JSON with before/after
  reason   TEXT,
  actor    TEXT NOT NULL         -- 'user', 'agent', 'auto'
)
```

### Indexes

- `plans(status)`, `plans(scope_kind, scope_value)`, `plans(owner_id)`, `plans(last_injected_at)`
- `plan_steps(plan_id, ordinal)`, `plan_steps(status)`
- `plan_revisions(plan_id, at)`

### Storage rules

- Plans live in the same SQLite database as memory for transactional linkage. Retrieval snapshot and session/checkpoint links resolve against the same DB.
- `retrieved_lesson_ids_json` and `retrieved_experience_ids_json` capture the exact IDs used at draft time. This is the audit trail for "why did the plan say this?" and it is preserved even if the underlying memories are later archived.
- Plans are **local-only in beta** regardless of `scope_kind`. Sync requires a separate redaction design (see open questions §14).
- Revisions are append-only. Plan state is reconstructed by layering revisions over the current row; the row holds the latest view, revisions hold the history.

### Lifecycle

```
    ┌───────┐   activate   ┌────────┐   complete   ┌───────────┐
    │ draft │ ───────────► │ active │ ───────────► │ completed │
    └───┬───┘              └───┬────┘              └───────────┘
        │                      │
        │ abandon              │ pause / abandon
        ▼                      ▼
    ┌──────────┐           ┌────────┐
    │ abandoned│           │ paused │ ──resume──┐
    └──────────┘           └────────┘           │
                                │                │
                                │ abandon        │
                                ▼                ▼
                           ┌──────────┐      ┌────────┐
                           │ abandoned│      │ active │
                           └──────────┘      └────────┘
```

| State | Meaning |
|---|---|
| `draft` | Drafted, not yet influencing any agent turn |
| `active` | Eligible for TurnStart injection; steps can advance |
| `paused` | Temporarily not injected; can resume |
| `completed` | All non-skipped steps `done`; outcome evaluated |
| `abandoned` | Explicitly stopped; outcome recorded as `abandoned` |

Draft plans enter `active` through either:

- Explicit user acceptance (`entire plan accept <id>` or interactive confirm in the draft flow), under `activation=review` (beta default).
- Automatic activation on draft creation, under `activation=auto` (opt-in).

At most one plan is `active` per `(scope_kind, scope_value, owner_id)` at a time to keep TurnStart injection predictable. Activating a second plan pauses the first.

---

## 5. CLI Surface

### Commands

| Command | Purpose |
|---|---|
| `entire plan <goal>` | Draft a new plan for the given goal; open review (review mode) or auto-activate |
| `entire plan list` | List plans in scope, filterable by status |
| `entire plan show <id>` | Show plan detail including retrieved memory, steps, revisions |
| `entire plan status [<id>]` | Compact progress view; default to current active plan |
| `entire plan accept <id>` | Move from `draft` → `active` |
| `entire plan advance [<id>] [--step <n>]` | Mark the current (or named) step `done`; start next step |
| `entire plan skip [<id>] [--step <n>] --reason "..."` | Mark step `skipped` with reason |
| `entire plan revise <id>` | Open TUI to edit goal, steps, or add/remove steps; writes a revision row |
| `entire plan pause <id>` | `active` → `paused` |
| `entire plan resume <id>` | `paused` → `active` |
| `entire plan abandon <id> --reason "..."` | `active`/`paused` → `abandoned`; outcome set |
| `entire plan outcome <id>` | Show outcome evaluation; re-run if not yet finalized |

### Agent skills (thin wrappers)

Two skills are added to the `entire:` plugin namespace so agents can cooperate with planning without special-casing:

- **`entire:draft-plan`** — wraps `entire plan <goal>`, used when an agent thinks a plan would help. Returns the plan ID and rendered steps.
- **`entire:revise-plan`** — wraps `entire plan revise` for structured revisions (add step, rewrite step, mark step skipped). The agent never edits the DB directly; it issues CLI calls that Entire validates and records.

Both skills are no-ops when `settings.PlanningLayerEnabled=off`.

### Global settings

```yaml
PlanningLayerEnabled: false         # default off in beta
PlanActivationPolicy: review        # review | auto
PlanInjectionBudgetBytes: 800       # soft budget for <active-plan> block
PlanMaxActiveBySessionScope: 1      # hard invariant
PlanStepAdvancementSources:
  - agent-claim                     # Stop hook parse
  - commit                          # Entire-Checkpoint trailer
  - explicit                        # CLI / TUI
```

---

## 6. Drafting a Plan

Drafting is **one local LLM call** that consumes memory from the local cache. The retrieval pipeline is the same one feeding TurnStart injection; only the consumer differs. Drafting does not round-trip to the server — if the cache is stale, the draft is drawn from whatever the cache currently holds. A user who wants the freshest memory before drafting runs `entire memory-loop refresh` first.

Retrieved memory IDs are **server-assigned IDs**, which are stable across cache invalidation. The snapshot stored on the plan references these IDs, so even if a cached row is archived or replaced server-side on the next refresh, the snapshot remains valid for the audit view. The TUI resolves snapshot IDs against the current cache and displays the state of each cited memory (active, archived, replaced) without breaking the plan.

```
entire plan <goal>
   │
   ▼
1. Resolve scope (default: session-active scope)
   │
   ▼
2. Retrieve memory:
     score active lessons
     run procedural-need classifier
     if threshold met, score active experiences
   │
   ▼
3. Compose Planner prompt:
     goal, retrieved memories (with IDs), recent session context,
     repo metadata, active plan (if revising, not drafting fresh)
   │
   ▼
4. LLM draft → structured steps
     (each step carries source_memory_ids citing the retrieved set)
   │
   ▼
5. Validate:
     - at least 1 step
     - each step has description
     - each cited source_memory_id exists in the retrieval snapshot
     - success_criteria present on at least one step (warn otherwise)
   │
   ▼
6. Write plan row + plan_steps rows; status=draft
   │
   ▼
7. If activation=review: open review (TUI or accessible prompt)
   If activation=auto:    transition draft → active immediately
```

The draft is **reproducible** in the sense that the retrieval snapshot is stored. The LLM output itself is not deterministic and is not fingerprinted — plans are not deduplicated. Two drafts of the same goal produce two plans; the user chooses one or abandons both.

### Sanitization and hardening

The draft comes from an LLM consuming user-supplied goal text and memory content that has already been sanitized at ingress. Before the draft is stored:

- Every LLM-generated string field (`goal` rewrite, step `description`, `success_criteria`, `avoid_notes`) passes through the same sanitizer as memory ingress (role markers, fenced blocks, tag openers, imperative-plus-verb patterns).
- Rewrites recorded on the plan row in `sanitizer_warnings_json` (new column added in PR 1 of the rollout).
- The goal as stored is the **user-supplied goal** verbatim; any LLM rewriting of the goal is rejected.

---

## 7. Injection (TurnStart block)

### Combined TurnStart bundle

With planning enabled, TurnStart injection gains a third block type:

```
<active-plan id="plan_k3x8p2" step="2" ordinal="2/4">
goal: Fix flaky tests in manual_commit_condensation_test.go
current: Audit test setup for cwd mutation and missing testutil.InitRepo usage
success-criteria: identify every site that bypasses testutil.InitRepo
source: lesson:L4k9 experience:E7m2
remaining: 2 steps
</active-plan>

[lesson bullets ...]

<prior-solve-path task-class="integration_test_failure">
step: ...
avoid: ...
</prior-solve-path>
```

### Budget split

The overall TurnStart byte budget (user setting) is now split across three consumers with default weights:

| Block | Default share |
|---|---|
| `<active-plan>` | 15 % |
| Lesson bullets | 60 % |
| `<prior-solve-path>` (when present) | 25 % |

If there is no active plan, its share redistributes to lessons and experiences pro rata. If procedural-need is low, experience share redistributes to lessons. Shares are caps, not floors; if a consumer has nothing to say, its share is zero.

The active-plan block is always a single block (there is at most one active plan per scope per owner). The block's content is **the current step's context**, not the entire plan — past steps are summarized as a count, upcoming steps as a count. The full plan lives in `entire plan show <id>`.

### Rendering and hardening

- `<active-plan>` uses the same delimited template approach as `<prior-solve-path>`. Downstream token scanning can detect tampering.
- The TUI shows the exact rendered string before a plan is activated.
- The kill switch `settings.PlanningLayerEnabled=off` suppresses the block entirely with no other side effects.

---

## 8. Step Advancement

Step advancement is multi-signal. No single source is authoritative; signals are composed with explicit precedence.

### Sources

1. **`explicit`** — user runs `entire plan advance` or advances via the TUI. Always wins.
2. **`commit`** — a commit whose message contains `Entire-Plan-Step: <plan_id>/<ordinal>` trailer. Added automatically by the PrepareCommitMsg hook when an active plan exists and the agent has claimed step completion.
3. **`agent-claim`** — the Stop hook parses the agent's final turn output for a structured signal (`[plan-step-done]` line or tool use). Recorded but does not immediately advance; surfaces as a pending claim in the next `entire plan status`.

### Precedence and conflict resolution

```
explicit > commit > agent-claim
```

- If `explicit` and `commit` both arrive, `explicit` wins but the commit trailer is recorded on `plan_steps.advancement_detail` for traceability.
- `agent-claim` never advances on its own without user or commit confirmation. This keeps the Executor from silently declaring completion on steps it did not actually finish, which MIA's paper describes as a common failure mode.
- When a step advances, its `success_criteria` are evaluated best-effort:
  - If the criteria string references a CLI-runnable check (e.g., `mise run test:integration`), the hook can optionally run it (opt-in).
  - Otherwise the criteria are recorded for human review on `entire plan outcome`.

### Skipping and failing

- `entire plan skip --step <n> --reason "..."` marks a step `skipped` and advances to the next. Skipped steps do not count toward the outcome's success signal.
- A step can be marked `failed` by explicit CLI or by three consecutive agent-claims followed by agent abandonment detected via session end. Failed steps do not advance the plan automatically; the user must either revise the step or abandon the plan.

---

## 9. Revision

Revisions are first-class. Every change to a plan after activation goes through `entire plan revise` (interactive) or its programmatic equivalent (called by the `entire:revise-plan` agent skill). Each revision:

- Appends a row to `plan_revisions` with `type`, `detail` (JSON before/after), `reason`, and `actor`.
- Updates the affected `plan_steps` rows or the `plans` row.
- Does **not** change step ordinals of already-`done` steps.

### Allowed revision types

| Type | Effect |
|---|---|
| `add_step` | Insert a new step at a given ordinal; shifts pending steps |
| `remove_step` | Remove a pending step; disallowed on `done`/`active` steps |
| `reorder` | Reorder pending steps; disallowed on `done`/`active` steps |
| `rewrite_goal` | Edit plan `goal`; sanitizer applies |
| `edit_step` | Edit description, success_criteria, avoid_notes on a pending step |
| `abandon` | Terminal; sets plan.status=`abandoned`, outcome=`abandoned` |
| `resume` | Move paused → active |

The TUI revision view always shows a diff against the prior version and requires a reason on destructive revisions (remove_step, abandon). The `entire:revise-plan` agent skill enforces the same rules.

---

## 10. Outcome Evaluation

Plan outcome generalizes memory outcome: **did executing this strategy cause the problem the plan was meant to solve to stop recurring?**

### Evaluation rules

Plan outcome is computed **locally** from the plan's own state plus session signals observable on this device.

- `outcome='pending'` while `status` is anything except `completed` or `abandoned`.
- On `completed`:
  - All non-skipped steps are `done`.
  - The CLI evaluates each step's `success_criteria` where a CLI-runnable check is present (opt-in).
  - If all evaluated criteria pass and no step is `failed`, the plan's outcome is `success`.
  - If at least one evaluated criterion failed or at least one step was `failed`, outcome is `partial` or `failed` based on user/agent choice.
  - The plan records its outcome locally; it does not compute memory outcomes.
- On `abandoned`:
  - `outcome='abandoned'`. This is a terminal outcome carrying information (the plan was wrong, the goal changed, or the agent could not execute).

### No direct memory-feedback writes

Plans do **not** push feedback to memory rows. Memory outcome is computed server-side (memory doc §8) from session-signal recurrence, which already reflects whatever the plan caused the agent to do. A plan whose execution actually reduced `source_signal` friction will be reinforced through the normal memory-outcome path; a plan whose execution didn't will not.

This avoids three problems:
- The CLI can no longer write directly to `memories` — those live on the server, and plans have no privileged channel to them.
- Double-counting: plan-driven feedback + session-signal feedback would reinforce the same evidence twice.
- Plan-author bias: a plan completing `success` locally does not necessarily mean the underlying friction went away; the server's signal-recurrence check is the ground truth.

### Plan-completion signal (only server-side coupling)

When a plan reaches `outcome=success`, the CLI posts a compact summary to the server via `POST /plans/completions`:

```json
{
  "plan_id": "plan_k3x8p2",
  "goal": "Fix flaky tests in manual_commit_condensation_test.go",
  "source_signal": "integration_test_cwd_leak",
  "task_class": "integration_test_failure",
  "file_dependencies": ["cmd/entire/cli/strategy/manual_commit_condensation_test.go"],
  "successful_steps": [...],
  "owner_id": "alishakawaguchi",
  "repo_id": "entirehq/cli",
  "completed_at": "2026-04-16T14:22:00Z"
}
```

Payload rules:
- Only sent for `outcome=success`. `partial`, `failed`, and `abandoned` outcomes do not produce a signal.
- No `attempted_steps`, no `failed_steps`, no raw transcript excerpts.
- LLM-generated fields already passed through the sanitizer at draft time.
- Outbox-eligible: if offline, the signal is queued and flushed on the next `entire memory-loop refresh`.
- Server authorization enforces `owner_id` matches the authenticated user and `repo_id` is accessible.

The server stores plan-completion signals in a minimal table (no plan contents, just the fields above) and uses them as input to the experience-derivation job described in the memory doc §9. Plan completions are **additional evidence**, not a replacement for checkpoint-based derivation.

### Completed plans as experience candidates

Plan completions contribute to the server's experience-derivation gate using the same evidence-independence rules as checkpoint-based experiences:

- ≥ 3 distinct plan-completion signals or checkpoint-based traces (combined) with overlapping `source_signal` and `file_dependencies`
- ≥ 2 distinct checkpoint bases (plan completions inherit the checkpoint base from their linked checkpoints)
- independence — ≥ 2 owners, **or** ≥ 2 branches with ≥ 24 h wall-clock span
- thresholds code-pinned server-side; settings may raise only

When the gate is met, the server generates an experience candidate with `source_plan_ids[]` populated alongside `source_experience_ids[]`. The candidate syncs into the CLI cache like any other experience and is reviewable in the existing experience TUI.

---

## 11. Full Example

Scenario: fixing flaky integration tests.

```bash
$ entire plan "Fix flaky tests in manual_commit_condensation_test.go"

Retrieving relevant memory...
  lessons:     3 matched
    [L] repo_rule:      Tests touching git state must use testutil.InitRepo (score 0.84)
    [L] anti_pattern:   Do not use os.Chdir after t.Parallel (score 0.71)
    [L] workflow_rule:  Seed at least one commit before invoking handlers (score 0.62)
  experiences: 1 matched  (procedural need: high)
    [E] integration_test_failure: "Recover from cwd leakage in lifecycle handlers" (score 0.79)

Drafting plan using retrieved memory (model: haiku, ~1.8s)...

Plan draft  (id: plan_k3x8p2)
Goal:  Fix flaky tests in manual_commit_condensation_test.go
Risk:  low-medium — may reveal deeper cwd leakage across sibling tests

Steps:
  1. Reproduce locally with `mise run test:integration -run TestCondensation`
     success: failure reliably reproduces ≥ 2/3 runs
  2. Audit test setup for cwd mutation and missing testutil.InitRepo usage
     source: lesson L:repo_rule + experience E:integration_test_failure
  3. Migrate to isolated temp repo pattern (InitRepo + seed commit + t.Chdir)
     avoid: running lifecycle handlers from real repo cwd  (from L:anti_pattern)
  4. Verify no sibling test regressed: `mise run test:integration`
     success: 0 failures across 3 consecutive runs

Dependencies: none
Memory injected: L:1, L:2, L:3, E:1

? Accept plan, revise, or draft alternatives?  [accept]

Plan activated. Plan id stored in session context for next agent turn.

$ claude    # or any agent — the plan is now in the TurnStart injection
```

On the agent side, the TurnStart hook injects a compact block:

```
<active-plan id="plan_k3x8p2" step="1">
goal: Fix flaky tests in manual_commit_condensation_test.go
current: Reproduce locally with `mise run test:integration -run TestCondensation`
success-criteria: failure reliably reproduces >= 2/3 runs
remaining-steps: 3
</active-plan>
<prior-solve-path task-class="integration_test_failure">
step: Reproduce in isolated temp repo first
step: Trace cwd-based git resolution before changing strategy logic
step: Use testutil.InitRepo + seed commit + t.Chdir
avoid: Running lifecycle handlers from the real repo cwd
</prior-solve-path>
```

As the agent works, step advancement happens via the existing hook system:

```bash
$ entire plan status plan_k3x8p2
  [x] 1. Reproduce locally                                     (done  14:02)
  [>] 2. Audit test setup                                      (active)
  [ ] 3. Migrate to temp repo pattern
  [ ] 4. Verify no sibling regression

$ entire plan revise plan_k3x8p2 --add-step \
    "2b. Check for similar pattern in rewind_test.go"
# Opens TUI for user to review revision; logged with reason.
```

On session stop, the plan's local outcome is finalized and a completion signal is posted to the server:

```bash
$ entire plan outcome plan_k3x8p2
  status:    completed
  outcome:   success
  steps:     4/4 done, 1 added mid-flight
  cited memory (not modified by plan; server will update from session signals):
    L:repo_rule      (server will reinforce if signal resolved)
    E:integ_failure  (server will reinforce if signal resolved)
  server completion signal: queued in outbox
  → on next memory-loop refresh: server evaluates for experience derivation
```

### Design implications this example forces

- Plans are **local-primary** artifacts stored in `.entire/memory-loop.db` (new `plans`, `plan_steps`, `plan_revisions` tables). They do not sync cross-device.
- Plan drafting is one local LLM call using cached memory — same retrieval path as injection, just a different consumer. No server round-trip to draft.
- TurnStart injection gets a third block type (`<active-plan>`) alongside lessons and experiences; byte budget splits three ways.
- Step advancement can be explicit (`entire plan advance`), inferred from commits (Entire-Plan-Step trailer), or agent-claimed via the Stop hook. All advancement stays local.
- Revision is first-class — the plan is expected to change; revisions are logged locally, not silent overwrites.
- Plan outcome is computed locally from step state + optional CLI-runnable success-criteria checks.
- Plans **do not** directly modify memory rows. Memory outcome is server-computed from session signals (memory doc §8), which already captures the effect of plan execution.
- Successful plans post a compact completion signal to the server as additional evidence for experience derivation. Non-success outcomes produce no server-side trace.

---

## 12. Governance and Review

TUI is the only review surface in beta. Two new lenses joining `Lessons` and `Experiences`:

- **Plans** — list + detail. Shows goal, scope, status, linked memories, retrieval snapshot, step progress, revision log, outcome.
- **Pending claims** — step advancements proposed by agents that have not yet been confirmed by commit or explicit user action. User can accept or reject in bulk.

Actions:

- `accept` (draft → active)
- `activate` (paused → active; at most one active per scope)
- `pause`, `resume`
- `advance`, `skip`, `revise`, `abandon`
- `reevaluate outcome` for completed plans whose outcome is still pending after the settling window

### Suppression

Plans are not suppressible by fingerprint (they are not deduplicated). Suppression applies at the memory layer; if a plan cites a suppressed memory, that memory is flagged on the plan detail but does not invalidate the plan.

---

## 13. Rollout Plan — Vertical Slices

```
PR P1  Plan artifact + manual create
          │
          ▼
PR P2  Plan lifecycle + revisions
          │
          ▼
PR P3  Drafting pipeline (LLM, retrieval, validator)
          │
          ▼
PR P4  TurnStart injection (<active-plan> block)
          │
          ▼
PR P5  Step advancement (explicit + commit signals)
          │
          ▼
PR P6  Step advancement (agent-claim via Stop hook)
          │
          ▼
PR P7  TUI review (plans, pending claims)
          │
          ▼
PR P8  Outcome evaluation + memory feedback
          │
          ▼
PR P9  Agent skills (entire:draft-plan, entire:revise-plan)
          │
          ▼
PR P10 Plans → experience derivation (playbook path)
```

All plan-layer PRs depend on the memory layer's PR 5 (lesson injection) at minimum, and memory PRs 7–8 for the experience-retrieval pipeline used during drafting. Plan tables are local-primary — no memory-cache dependency for writes — so PR P1 only needs the memory cache schema to be in place (memory PR 1).

### PR P1 — Plan artifact + manual create

**Deliverables**

- `plans`, `plan_steps`, `plan_revisions` tables + migrations.
- Manual create flow: `entire plan create <goal>` prompts for steps interactively (no LLM).
- `entire plan list`, `entire plan show <id>`.
- Validator: at least one step, ordinal uniqueness, scope resolution, owner_id from `gh auth status`.

**Tests required**

- Validator rejects plans with zero steps.
- Ordinal uniqueness enforced by unique index.
- Scope isolation: `me`-scoped and `repo`-scoped plans with identical goals coexist without collision.

**Success criteria** — plans can be created, listed, and shown entirely without the LLM path.

**Dependencies** — Memory PR 1.

### PR P2 — Lifecycle + revisions

**Deliverables**

- State transitions per Section 4 lifecycle diagram.
- `entire plan accept`, `pause`, `resume`, `abandon`.
- `entire plan revise` for all revision types from Section 9, with reason required on destructive revisions.
- `plan_revisions` append-only log.
- Invariant: at most one active plan per `(scope_kind, scope_value, owner_id)`.

**Tests required**

- Activating a second plan in the same scope pauses the first.
- `remove_step` / `reorder` rejected on `done` or `active` steps.
- Revision log reconstructs prior state.

**Success criteria** — a plan can move through all lifecycle states under CLI control.

**Dependencies** — PR P1.

### PR P3 — Drafting pipeline

**Deliverables**

- `entire plan <goal>` — LLM draft using the memory retrieval pipeline.
- Retrieval snapshot written to `retrieved_lesson_ids_json` and `retrieved_experience_ids_json`.
- Ingress sanitization of LLM-generated fields, with `sanitizer_warnings_json` recording.
- Validation: cited `source_memory_ids` exist in the snapshot; goal stored verbatim from user input.
- Activation policy `review` (default) and `auto`.

**Tests required**

- Cited memory IDs that are not in the retrieval snapshot cause draft rejection.
- Goal as stored is byte-identical to user input.
- Sanitizer strips role markers / fenced blocks / tag openers / imperative-plus-verb from generated fields.
- Under `review`, draft status is `draft` and no TurnStart injection occurs.

**Success criteria** — a draft produced from a realistic goal cites real memory IDs and passes sanitization.

**Dependencies** — PR P1, Memory PR 4 (lesson retrieval), Memory PR 9 (procedural-need classifier) ideally; acceptable to ship with lesson-only retrieval if Memory PR 9 is not yet in.

### PR P4 — TurnStart injection

**Deliverables**

- `<active-plan>` block template per Section 7.
- Byte-budget split across plan / lessons / experiences with redistribution rules.
- Block emitted only when `settings.PlanningLayerEnabled=on` and an `active` plan exists in session scope.
- Injection log fields for plan ID and current step.
- Kill switch (`PlanningLayerEnabled=off`) suppresses the block entirely.

**Tests required**

- With `PlanningLayerEnabled=off`, no `<active-plan>` ever emits.
- Budget redistribution: no active plan → share goes to lessons/experiences.
- Delimited template is byte-stable across turns for the same plan+step.

**Success criteria** — a TurnStart in a session with an active plan injects the plan block alongside memory.

**Dependencies** — PR P2, Memory PR 4.

### PR P5 — Step advancement (explicit + commit)

**Deliverables**

- `entire plan advance`, `entire plan skip`.
- PrepareCommitMsg hook appends `Entire-Plan-Step: <plan_id>/<ordinal>` trailer when an active plan has a current step with a pending agent-claim or user-claim.
- PostCommit hook parses the trailer and advances the step.
- `plan_steps.advancement_signal` and `advancement_detail` populated.

**Tests required**

- `explicit` advancement beats a pending `commit` advancement on the same step.
- Commit trailer presence is required for `commit`-source advancement; absence does not advance.
- Skipped step's `success_criteria` are not evaluated.

**Success criteria** — a realistic session with commits walks through all steps to completion.

**Dependencies** — PR P4.

### PR P6 — Step advancement (agent-claim)

**Deliverables**

- Stop hook parses agent final turn output for `[plan-step-done]` structured lines.
- Claims are recorded as `advancement_signal='agent-claim'` but do **not** auto-advance.
- Claims surface in `entire plan status` and in the TUI pending-claims list.

**Tests required**

- An agent-claim alone never transitions step to `done`.
- A subsequent commit or explicit advance consumes the pending claim.
- Three consecutive agent-claims followed by session end without commits flag the step as at-risk (surfaced in TUI, not auto-failed).

**Success criteria** — agents can signal step completion without being trusted to act on it.

**Dependencies** — PR P5.

### PR P7 — TUI review

**Deliverables**

- Plans lens: list + detail.
- Pending-claims lens: bulk accept/reject.
- Revisions view per plan.
- Retrieval snapshot view (which memories fed this plan).

**Tests required**

- Bulk accept of pending claims advances all selected steps atomically.
- Retrieval snapshot survives archival of its underlying memories (shown as archived in the view, not broken).

**Success criteria** — a reviewer can move a plan from draft to completion entirely within the TUI.

**Dependencies** — PR P3, PR P6.

### PR P8 — Outcome evaluation + plan-completion signal

**Server dependencies** — `POST /plans/completions` endpoint.

**Deliverables**

- Local outcome computation per Section 10: step-state check, optional CLI-runnable success-criteria evaluation.
- `entire plan outcome <id>` command.
- Plan-completion signal written to the memory-loop outbox on `outcome=success`; flushed by `entire memory-loop refresh`.
- No direct writes to memory cache rows from plan code — the plan never modifies `memories_cache` or `experiences_cache`.
- Settling window (default 72 h) delays finalization when criteria cannot be evaluated immediately.

**Tests required**

- `success` outcome queues exactly one completion signal in the outbox; `partial`, `failed`, `abandoned` produce none.
- Outbox entry is rejected by the sanitizer if payload contains unsanitized LLM-generated content.
- Memory cache rows are not mutated by any plan-outcome code path.
- Settling window: outcome remains `pending` for the configured duration before finalizing.
- Offline completion: signal queued; next refresh flushes it without loss.

**Success criteria** — completed plans post outcome signals to the server; memory cache rows remain untouched by plan code.

**Dependencies** — PR P7, Memory PR 4 (outbox infrastructure).

### PR P9 — Agent skills

**Deliverables**

- `entire:draft-plan` skill — wraps `entire plan <goal>`; returns plan ID and rendered steps.
- `entire:revise-plan` skill — wraps `entire plan revise`; supports add_step, edit_step, skip_step, abandon.
- Both skills no-op when `PlanningLayerEnabled=off`.
- Documentation in `docs/architecture/agent-guide.md`.

**Tests required**

- Skill calls with `PlanningLayerEnabled=off` return a clear no-op response without side effects.
- `entire:revise-plan` cannot advance steps (advancement is gated; revisions are separate).

**Success criteria** — an agent can invoke planning through skills in a real session without additional plumbing.

**Dependencies** — PR P3, PR P5.

### PR P10 — Plan-derived experience read views

**Server dependencies** — server-side experience derivation accepts plan-completion signals as evidence (extension of the memory-doc derivation job). Derived experiences carry `source_plan_ids[]` in the payload.

**Deliverables**

- The CLI displays plan-derived experiences the same way it displays checkpoint-derived experiences — they arrive through the normal memory-cache sync.
- TUI cross-links from plan detail → derived experience and from experience detail → source plan(s).
- No local derivation job. Derivation runs server-side. Any attempt in CLI settings to lower a derivation threshold below the server-reported floor is rejected at load time (inherited from memory-doc §9 behavior).

**Tests required**

- Plan-derived experience visible in experience TUI after refresh; cross-links resolve correctly.
- Server-reported threshold floors are honored; CLI settings cannot lower them.
- A plan whose cited memories are all archived still surfaces correctly as the source of a derived experience.

**Success criteria** — repeated successful plans on a recurring `source_signal` yield server-derived experience candidates that the CLI can review and promote through the existing experience TUI.

**Dependencies** — PR P8, Memory PR 7, Memory PR 10.

---

## 14. Open Questions

1. **Plan sync across contributors.** Plans contain goal text and free-form step text that may include repo-sensitive intent. Sync requires a redaction design. Default until resolved: plans are local-only even at `scope_kind=repo`.
2. **Auto-run success criteria.** When success criteria are CLI-runnable (e.g., `mise run test:integration`), should the PostCommit hook optionally run them automatically, or keep evaluation human-only in beta? Safety implications for arbitrary commands.
3. **Multiple active plans per scope.** The beta invariant is one active plan per scope. Real workflows sometimes want two (e.g., a background refactor plan plus a foreground bug-fix plan). When does that earn its way in?
4. **Draft-time model.** Should drafting always use Haiku, or should model selection follow a user setting? Quality-vs-cost tradeoff; impacts draft latency budget.
5. **Playbook layer.** The memory doc anticipated a playbook layer. This doc's PR P10 is one path to playbooks (repeated plans become experiences, which derive lessons). Is that the full playbook story, or do playbooks deserve their own artifact type?
6. **Revisions by agent.** The `entire:revise-plan` skill lets an agent propose revisions. Should any revision require human confirmation under `activation=review`, or only destructive ones?

---

## 15. Verification

Per-PR tests are listed in Section 13. System-level verification before the planning-layer default flip:

- Concurrent access — drafting during an active session does not race with TurnStart injection.
- Injection latency — `<active-plan>` block emission does not breach the 50 ms TurnStart SLO. Measured at PR P4.
- Kill switch — `PlanningLayerEnabled=off` reverts to lesson+experience-only behavior within one turn.
- Advancement precedence — explicit > commit > agent-claim holds under adversarial input (agent claims every step done).
- **No-touch memory invariant** — plan code never mutates `memories_cache` or `experiences_cache` rows. Verified by a test that diffs the cache before and after a plan completion.
- **Completion-signal exactly-once** — `outcome=success` produces one outbox row; non-success outcomes produce none; the outbox row is idempotent on retry.
- Sanitization — LLM-generated plan fields strip role markers, fenced blocks, tag openers, imperative-plus-verb; goal stored verbatim from user input.
- Derivation floors — server-reported thresholds honored; CLI settings cannot lower them.
- Offline completion — plan completed while offline, refreshed later, signal reaches the server without loss.
- Canary E2E — full draft-activate-advance-complete-outcome cycle exercised through Vogon.

---

## 16. Summary

- Three roles: **Manager** (memory, server source of truth + local cache), **Planner** (plans, local-primary), **Executor** (agent, external).
- Plans are **local-primary** artifacts — per-device workflow state, not cross-device knowledge. No sync.
- Plans reference memory via **server-assigned IDs** stored in the retrieval snapshot, which remain valid across cache invalidation.
- Drafting uses the local memory cache; no server round-trip for a draft.
- Step advancement is multi-signal with explicit precedence; agents cannot silently declare completion.
- Revisions are first-class and logged locally, including when an agent proposes them via skill.
- Plans **do not** directly write memory rows. Memory outcomes flow from session signals server-side (memory doc §8), which already captures the effect of plan execution.
- The only server-side coupling from a plan is a **completion signal** on `outcome=success`, sent via the memory-loop outbox. It contributes to server-side experience derivation as additional evidence.
- The full planning layer sits behind a runtime kill switch; default off in beta, flipped on only after cohort telemetry from the memory layer is clean.
