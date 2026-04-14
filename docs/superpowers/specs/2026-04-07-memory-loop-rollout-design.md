# Memory Loop: System Design & Rollout Plan

## Context

The memory loop is a closed-loop system that learns from agent session analytics and injects contextual guidance into future sessions. The proof of concept validated the core idea, but its code and storage model are throwaway. This document defines the production design from scratch and a rollout path composed of small, independently shippable PRs.

Target: external beta, opt-in. Once enabled, beta defaults to `mode=auto` and `policy=review`.

---

## 1. System Overview

```
Sessions -> Checkpoints -> [entire.io API: CheckpointAnalysis] -> Local Cache
                                                                      │
                                                                      ▼
                                              Memory Generation -> Storage -> Injection -> Sessions
                                                                                  ^
                                                                       Outcome Tracking
```

CheckpointAnalysis is generated and stored by the entire.io API (see [RFD-014](https://github.com/entirehq/company-knowledge/pull/76)). The memory loop fetches analysis from the API and caches it in the local insights DB. Memory generation runs locally against the cached analysis.

### Memory Record

A memory is a structured piece of guidance derived from session patterns or created manually by a user.

**Kinds:** `repo_rule`, `workflow_rule`, `agent_instruction`, `skill_patch`, `anti_pattern`

**Uniform beta schema:**

- `summary`
- `kind`
- `decisions[]`
- `failed_approaches[]`
- `warnings[]`
- `error_signatures[]`
- `cause_chain[]`
- `file_dependencies[]`
- `key_insights[]`

**Schema rules:**

- `summary` is required for every memory.
- `summary` is for compact display and prompt injection only. It is not used for scoring.
- All structured fields are lists.
- `cause_chain[]` is stored and rendered as an ordered sequence.
- `file_dependencies[]` contains repo-relative file paths only.
- `error_signatures[]` can include exact literals and LLM-minted normalized categories.
- No structured field other than `summary` is individually required.

**Signal types:**

- **High-signal fields:** `failed_approaches[]`, `error_signatures[]`, `cause_chain[]`, `file_dependencies[]`
- **Lower-signal fields:** `decisions[]`, `warnings[]`, `key_insights[]`

### Lifecycle States

```
                  activate / promote
            ┌──────────────────────────┐
            │                          ▼
      ┌───────────┐            ┌─────────────┐
      │ candidate │            │   active    │
      └───────────┘            └─────────────┘
            │                    │          │
            │ archive            │ suppress │ archive
            │                    ▼          │
            │              ┌────────────┐   │
            │              │ suppressed │   │
            │              └────────────┘   │
            │                    │          │
            │                    │ archive  │
            ▼                    ▼          ▼
      ┌──────────────────────────────────────┐
      │              archived                │
      └──────────────────────────────────────┘
```

- **candidate**: Generated but not yet approved for injection
- **active**: Eligible for injection into sessions
- **suppressed**: Temporarily disabled by user. Fingerprint still blocks regeneration.
- **archived**: Permanently disabled. Preserved for history only.

**Origin rules:**

- `manual` memories always enter as `active`, regardless of scope.
- `generated` memories follow activation policy across all scopes:
  - `policy=auto` -> enter as `active`
  - `policy=review` -> enter as `candidate`

### Scopes

**Personal (`me`):** Tied to GitHub username (`owner_id`). Repo-local only in beta.

**Repo:** Visible to all contributors through shared repo sync. Shared generated repo memories are governed through review.

**Branch:** Ephemeral. Tied to a git branch name. Eligible for auto-cleanup when the branch no longer exists locally across any worktree.

```
Scope Hierarchy & Promotion
============================

  ┌─────────┐  promote   ┌──────────┐  promote   ┌──────────┐
  │  Branch │ ─────────> │ Personal │ ─────────> │   Repo   │
  └─────────┘            └──────────┘            └──────────┘
       │                      │                       │
  Ephemeral              Repo-local              Shared in beta
  Auto-cleaned           Per-user                All contributors
  when branch gone       GitHub owner_id         Remote sync
```

### Modes and Policies

**Modes in beta:**

- `off`: Injection hook is a no-op
- `auto`: Matched memories are injected automatically

`manual` mode is removed from the beta design.

**Policies in beta:**

- `review`: All generated memories remain `candidate`
- `auto`: All generated memories become `active`

**Beta defaults once enabled:**

- `mode=auto`
- `policy=review`
- `threshold=balanced`

Generated candidate review is TUI-only in beta.

---

## 2. Storage Architecture

### Local Storage: SQLite

All reads and writes go through a local SQLite database at `.entire/memory-loop.db`. Injection always reads from local storage, even when repo memories are shared remotely. The local insights DB caches CheckpointAnalysis fetched from the entire.io API for use during memory generation.

```
Beta Storage Architecture
=========================

                                     .entire/
  ┌──────────────┐    read/write    ┌───────────────────┐
  │  CLI / Hook  │ <─────────────> │  memory-loop.db   │
  └──────────────┘                  │  (SQLite + WAL)   │
                                    └─────────┬─────────┘
                                              │ sync
                                              │ repo scope only
                                              ▼
                              ┌────────────────────────────┐
                              │  Code owner choice         │
                              │  - checkpoint branch       │
                              │  - EntireDB                │
                              └────────────────────────────┘

  Personal + Branch memories: local only
  Repo memories: local cache + shared remote sync
```

**Schema shape:**

```sql
schema_version (version INTEGER)

memories (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  summary TEXT NOT NULL,
  decisions_json TEXT NOT NULL,
  failed_approaches_json TEXT NOT NULL,
  warnings_json TEXT NOT NULL,
  error_signatures_json TEXT NOT NULL,
  cause_chain_json TEXT NOT NULL,
  file_dependencies_json TEXT NOT NULL,
  key_insights_json TEXT NOT NULL,
  scope_kind TEXT NOT NULL,   -- 'me', 'repo', 'branch'
  scope_value TEXT,           -- branch name, owner_id, or empty
  status TEXT NOT NULL,       -- 'candidate', 'active', 'suppressed', 'archived'
  origin TEXT NOT NULL,       -- 'generated', 'manual'
  fingerprint TEXT NOT NULL,
  confidence TEXT,
  strength INTEGER,
  source_signal TEXT,
  outcome TEXT DEFAULT 'neutral',
  inject_count INTEGER DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  last_injected_at TEXT
)

memory_history (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  memory_id TEXT NOT NULL REFERENCES memories(id),
  type TEXT NOT NULL,
  at TEXT NOT NULL,
  detail TEXT,
  session_id TEXT
)

injection_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL,
  injected_at TEXT NOT NULL,
  selected_memory_ids_json TEXT NOT NULL,
  used_embeddings INTEGER NOT NULL,
  used_embedding_fallback INTEGER NOT NULL
)

refresh_history (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  at TEXT NOT NULL,
  generated_count INTEGER NOT NULL,
  activated_count INTEGER NOT NULL,
  candidate_count INTEGER NOT NULL,
  filtered_count INTEGER NOT NULL,
  deduped_count INTEGER NOT NULL
)

analysis_cache (
  checkpoint_id TEXT NOT NULL,
  repo_id TEXT NOT NULL,
  analysis_json TEXT NOT NULL,
  schema_version INTEGER NOT NULL,
  content_hash TEXT NOT NULL,       -- SHA-256 of analysis_json
  fetched_at TEXT NOT NULL,
  source TEXT NOT NULL,             -- 'api' or 'on-demand'
  generator_version INTEGER,        -- local generation pipeline version at time of last processing
  processed_at TEXT,                 -- when this entry was last used for memory generation
  PRIMARY KEY (checkpoint_id, repo_id)
)
```

**Storage rules:**

- Versioned migrations, not `CREATE TABLE IF NOT EXISTS` only.
- WAL mode and busy timeout for concurrent reads and writes.
- Index `status`, `scope_kind + scope_value`, and `fingerprint`.
- Index `analysis_cache` on `(repo_id, fetched_at)` for recency queries.
- No hard cap in beta. Growth is monitored through local metrics instead.

**Analysis cache rules:**

- A cached entry is considered fresh if `fetched_at` is within the last 24 hours. Refresh re-fetches stale entries.
- If the API returns an analysis with a different `content_hash` than the cached version, the cache row is replaced and memory generation runs against the new content.
- If the API returns the same `content_hash` and the local `generator_version` matches the current pipeline version, `fetched_at` is updated but memory generation skips that checkpoint (already processed).
- If the `content_hash` is unchanged but `generator_version` is older than the current pipeline version, memory generation re-runs against the cached analysis. This forces regeneration when local pattern analysis, LLM prompts, admission gates, or fingerprinting logic changes.
- The `generator_version` is a monotonically increasing integer constant defined in the generation pipeline code. It must be bumped in any PR that changes generation semantics.
- If the API is unreachable, stale cache entries older than 48 hours are skipped for memory generation (see degraded-mode rules below). Entries within 48 hours may still be used. Refresh output reports how many checkpoints were skipped due to staleness.
- Cache rows for checkpoints older than 90 days are eligible for eviction during refresh.

### Remote Storage Questions

Repo memories are shared in beta. Two code-owner decisions remain open.

**Question 1: Shared repo backend from day one**

**Option A: checkpoint branch**
- Reuses existing git distribution and checkpoint infrastructure
- Works with normal push/fetch flows
- No new external service dependency
- Harder conflict resolution and metadata evolution
- Weaker long-term query and audit model

**Option B: EntireDB**
- Cleaner sync semantics and conflict handling
- Better long-term foundation for shared state
- Stronger audit and query model
- Adds service and auth dependency
- Larger operational surface for beta

**Question 2: What remote state should sync**

**Option A: sync only active repo memories**
- Smallest payload and simplest governance story
- Fewer sync conflicts
- Easier beta behavior to explain
- Does not preserve shared suppression/archive intent

**Option B: sync active + suppressed/archived/history**
- Preserves governance decisions and richer review history
- Better basis for multi-user review later
- More state, more conflicts, more complexity

### Concurrent Access

Multiple write paths will exist:

- lifecycle hook injection logs
- manual add/edit/archive operations
- refresh reconciliation
- TUI review actions

Rules:

- Injection log writes stay short and transactional.
- Reconciliation runs in a single transaction.
- Busy timeouts surface explicit errors.
- Retry once on busy timeout for the common contention case.

---

## 3. Injection and Scoring

### Injection Path

```
Injection Hot Path
==================

  User submits prompt
         │
         ▼
  ┌─────────────────┐
  │   TurnStart     │
  │   hook          │
  └────────┬────────┘
           │
           ▼
  ┌─────────────────┐
  │ Check mode      │──── off ────> return
  └────────┬────────┘
           │ auto
           ▼
  ┌─────────────────┐
  │ Load active     │
  │ memories        │
  └────────┬────────┘
           │
           ▼
  ┌─────────────────┐
  │ Score structured│
  │ fields only     │
  └────────┬────────┘
           │
           ▼
  ┌─────────────────┐
  │ Select top N    │
  │ under byte      │
  │ budget          │
  └────────┬────────┘
           │
           ▼
  ┌─────────────────┐
  │ Inject compact  │
  │ summary + typed │
  │ guidance        │
  └────────┬────────┘
           │
           ▼
  ┌─────────────────┐
  │ Log injection   │
  │ and fallback    │
  └─────────────────┘
```

Target: total injection path under 50ms. Log a warning if exceeded.

### Scoring Model

Scoring uses only structured fields. `summary` is excluded.

**Primary signals:**

- Exact and normalized `error_signatures[]`
- `file_dependencies[]` path overlap
- `failed_approaches[]`
- `cause_chain[]`

**Secondary signals:**

- `decisions[]`
- `key_insights[]`
- `warnings[]` in `flexible` and `balanced` only

**Additional factors:**

- Embedding similarity in `balanced`
- Outcome bonus and penalty
- Cooldown penalty for recently injected memories
- Diversity across same-fingerprint memories

**Embedding behavior:**

- `balanced` includes embeddings by default in beta.
- If embeddings are unavailable, scoring falls back silently.
- The fallback is recorded in metrics and status only, not shown in-turn.

**Injection count:**

- Maximum memories injected per turn remains a user setting managed in the TUI.

### Threshold Presets

**flexible**
- Admission: `summary` plus lower-signal fields is enough
- Scoring: high-signal + lower-signal + `warnings`
- Best recall, highest noise risk

**balanced** (beta default)
- Admission: `summary` plus at least one high-signal field
- Scoring: high-signal fields lead, `warnings` contribute, embeddings enabled
- Best default tradeoff between precision and recall

**strict**
- Admission: `summary` plus at least two high-signal fields
- Scoring: concrete structured signals only, `warnings` ignored
- Highest trust, lowest recall

---

## 4. Generation, Reconciliation, and Outcomes

### Generation Pipeline

```
Generation Pipeline
===================

  Identify recent checkpoints (synced + local)
       │
       ├── synced: fetch analysis by checkpoint_id + repo_id
       │
       └── unsynced: upload scoped transcript for on-demand analysis
       │
       ▼
  Cache in analysis_cache (checkpoint_id, repo_id)
       -> Skip unchanged (same content_hash)
       -> Pattern analysis -> LLM structured output
       -> preset admission gate -> dedup fingerprint
       -> reconcile -> apply policy -> store
```

Steps:

1. Identify recent checkpoints (both synced and local-only).
2. For synced checkpoints: fetch CheckpointAnalysis from the entire.io API by checkpoint ID + repo identity.
3. For local unsynced checkpoints: upload scoped transcript material to the entire.io API for on-demand analysis (see [RFD-014 CLI Flow for Local Unsynced Checkpoints](https://github.com/entirehq/company-knowledge/pull/76)). The uploaded payload is minimal: checkpoint-scoped transcript, prompt, files touched, checkpoint ID, repo identity, and agent metadata.
4. Cache all results in the local `analysis_cache` table keyed by `(checkpoint_id, repo_id)`.
5. Skip checkpoints whose cached `content_hash` has not changed and whose `generator_version` matches the current pipeline version. Re-run generation if `generator_version` is stale.
6. Analyze friction patterns, missing context, and failure loops from cached analysis.
7. Ask the LLM for structured memories using the production schema.
8. Apply preset admission gates.
9. Reject generic records that lack concrete technical specificity.
10. Compute primary fingerprint from normalized high-signal fields plus `kind`.
11. Reconcile with existing generated memories.
12. Apply activation policy and write results.

**Future possibility:** Memory generation itself could move server-side to the entire.io API, eliminating the local LLM call. This is deferred until the local generation quality and the CheckpointAnalysis API are both stable.

### Admission Rules

Generated memories must always have `summary`.

Additional preset gates:

- `flexible`: lower-signal fields are sufficient
- `balanced`: at least one populated high-signal field
- `strict`: at least two populated high-signal fields

### Reconciliation Rules

**Primary fingerprint identity:**

- `kind`
- normalized `failed_approaches[]`
- normalized `error_signatures[]`
- normalized `cause_chain[]`
- normalized `file_dependencies[]`

**Merge behavior for generated memories with the same fingerprint:**

- Missing lower-signal fields are merged in.
- Conflicting lower-signal values prefer the newer generated version.
- Generated memories are system-owned and uneditable in beta.

**Manual memory behavior:**

- Manual memories are user-owned and editable.
- Manual memories are not overwritten by generated reconciliation.

### Outcome Tracking

Each generated memory stores `source_signal`, the friction pattern that caused generation.

Outcome rules:

1. Manual memories are always `neutral`.
2. Memories with `inject_count < 3` remain `neutral`.
3. After enough injections, compare post-injection sessions against `source_signal`.
4. If the source friction signal disappears, mark `reinforced`.
5. If it persists, mark `ineffective`.
6. If data is insufficient, stay `neutral`.

This measures whether the original problem stopped recurring, not whether words from the memory text appeared again.

### Refresh

Beta refresh is manual only:

```bash
entire memory-loop refresh
```

Refresh requires Entire auth and API availability to fetch CheckpointAnalysis.

**Degraded-mode behavior (API unreachable):**

- Cached entries within 48 hours of `fetched_at` may still be used for memory generation. These are recent enough that the analysis is unlikely to have been corrected server-side.
- Cached entries older than 48 hours are skipped entirely. No memories are created, updated, or reactivated from stale analysis beyond this window.
- Refresh output clearly reports degraded status: how many checkpoints were fetched fresh, how many used recent cache, and how many were skipped due to staleness.
- If all checkpoints are skipped (full outage with only stale data), refresh completes with zero generation and a prominent warning rather than silently succeeding.

Auto-refresh is intentionally deferred until generation quality and shared sync behavior are trusted.

### API Authorization and Cache Binding

**Server-side authorization:**

- The entire.io API enforces repo membership on every request. The authenticated user must have access to the repo identified in the request. The CLI does not rely on client-side repo identity alone.
- `GET /analysis` verifies the authenticated user has read access to the repo owning the checkpoint.
- `POST /analysis` (unsynced upload) verifies the authenticated user has write access to the repo. The API resolves repo identity from its own records or the GitHub App installation, not from the client-supplied `owner/repo` string alone.
- Requests for repos the user does not have access to return `403`. The CLI treats this as no analysis available for that checkpoint.

**Fetch contract:**

- The CLI authenticates with Entire auth (existing `entire auth` token).
- Synced checkpoint fetches use `GET /analysis?checkpoint_id=<id>&repo_id=<id>`.
- Unsynced checkpoint uploads use `POST /analysis` with the scoped payload.
- The API response must include `checkpoint_id`, `repo_id`, and `schema_version` fields that match the request. The CLI rejects responses where any of these do not match. Echoed IDs are a client-side safety check, not a substitute for server-side authorization.

**Cache binding:**

- The local `analysis_cache` is keyed by `(checkpoint_id, repo_id)`. This prevents cross-repo cache collisions.
- `repo_id` is the same identifier used for checkpoint sync (GitHub `owner/repo`).
- Before using a cached entry for memory generation, the CLI verifies `repo_id` matches the current repository. Mismatched entries are skipped and logged as warnings.

**Rejection behavior:**

- If the API returns an analysis for a different `checkpoint_id` or `repo_id` than requested, the response is discarded and logged as a warning. The checkpoint is treated as having no analysis for this refresh cycle.
- If the API returns `403`, the CLI logs the denied repo and continues to the next checkpoint without caching.

---

## 5. Beta Decisions and Open Questions

### Decided Beta Behavior

- Shared repo memories are in scope for beta.
- Beta defaults to `mode=auto`.
- Beta defaults to `policy=review`.
- Under `review`, all generated memories are `candidate`, regardless of scope.
- Under `auto`, all generated memories are `active`, regardless of scope.
- Generated candidate review is TUI-only in beta.
- Manual memories are editable and can be created in any scope.
- Generated memories are uneditable in beta.
- Personal memories are repo-local only.
- No hard cap in beta.
- Branch cleanup remains worktree-safe.
- Suppressed memories are kept indefinitely for now.
- Manual memory lifecycle is warn-only, not auto-pruned.
- Local-only health metrics are preferred in beta.
- Memory generation consumes CheckpointAnalysis from the entire.io API ([RFD-014](https://github.com/entirehq/company-knowledge/pull/76)), cached locally in the insights DB. Refresh requires Entire auth and API availability; fallback to stale cache with a warning.
- The CLI no longer generates CheckpointAnalysis locally. Analysis is generated and stored by the entire.io API using Entire's Claude key.

### Open Code-Owner Questions

**Q1: Which shared repo backend should beta use?**

**checkpoint branch**
- Lowest rollout risk
- No new service dependency
- More awkward sync and conflict handling

**EntireDB**
- Better long-term shared-state design
- Cleaner sync semantics
- Higher beta complexity and service dependency

**Q2: What remote state should sync?**

**active repo memories only**
- Simplest beta behavior
- Lowest sync complexity
- No shared suppression/archive history

**active + suppressed/archived/history**
- Richer shared governance model
- More sync and conflict complexity

---

## 6. PR Rollout Path

Each PR is a vertical slice delivering one working feature end-to-end. Target: manageable review size, not maximal batching.

```
PR Dependency Graph
===================

  PR 1: Structured Store + Manual Add
         │
         ▼
  PR 2: Lifecycle + Manual Editing
         │
         ▼
  PR 3: Generation + Preset Gates
         │
         ▼
  PR 4: Injection + Scoring
         │
         ▼
  PR 5: Shared Repo Sync
         │
         ├──────────────┐
         ▼              │
  PR 6: TUI Review      │
         │              │
         ▼              ▼
  PR 7: Outcome Tracking
         │
         ▼
  PR 8: Refresh + Tuning Polish
```

### PR 1: Structured Store + Manual Add

- SQLite database setup, schema, migrations, WAL mode
- Uniform structured memory model
- Manual create flow with inputs for all fields
- Manual memories enter `active` immediately in any scope
- List and detail views for stored memories

### PR 2: Lifecycle + Manual Editing

- `activate`, `suppress`, `unsuppress`, `archive`, `promote`
- History tracking for lifecycle events
- Manual edit flow for manually created memories only
- Status views by scope and lifecycle state

### PR 3: Generation + Preset Gates

- Fetch CheckpointAnalysis from entire.io API for synced checkpoints
- Upload scoped transcript for on-demand analysis of local unsynced checkpoints
- `analysis_cache` table with freshness, content hash, and `(checkpoint_id, repo_id)` binding
- Validate API responses match requested checkpoint and repo identity
- Pattern analysis from cached CheckpointAnalysis
- LLM structured generation (local)
- `flexible`, `balanced`, `strict` admission behavior
- Generic-memory rejection
- Primary fingerprint generation
- Generated memories obey activation policy
- Graceful fallback to stale cache when API is unreachable

### PR 4: Injection + Scoring

- Structured-field scoring engine
- Embeddings in `balanced`
- Silent embedding fallback metrics
- Outcome bonus and cooldown logic
- Lifecycle hook injection in `off` and `auto` modes only

### PR 5: Shared Repo Sync

- Implement the code-owner-selected repo backend
- Sync repo-scope memories from day one
- Preserve local-first reads for injection
- Handle sync conflicts explicitly

### PR 6: TUI Review

- Use the existing TUI pattern
- Review generated candidates in the TUI only
- Show structured fields before promotion
- Keep non-TUI review out of beta scope

### PR 7: Outcome Tracking

- Store and evaluate `source_signal`
- Derive `neutral`, `reinforced`, `ineffective`
- Feed outcomes back into scoring

### PR 8: Refresh + Tuning Polish

- Manual refresh UX polish
- Threshold tuning and metrics surfacing
- Branch cleanup polish
- Performance tuning for injection and reconciliation

---

## 7. Verification

Each PR includes its own targeted tests. Cross-cutting verification for the full system:

- Concurrent access: refresh during active session injection causes no data loss or silent failure
- Injection latency: measured under realistic memory volume, not only empty-store cases
- Embedding fallback: correctly recorded in metrics without in-turn warnings
- Branch cleanup safety: branch memories for branches active in another worktree are preserved
- Shared repo sync: conflict behavior is explicit and deterministic
- Outcome derivation: based on source friction signal, not text overlap
- API availability: refresh degrades gracefully to stale cache when entire.io API is unreachable
- Canary E2E: full flow exercised through Vogon where practical
