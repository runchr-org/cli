# Animation: How Checkpoint Links Survive Rebase

This document shows step-by-step how checkpoint links remain intact when a user rebases their branch, demonstrating the shadow branch migration and the independence of the metadata branch.

---

## Starting State: Two Commits With Checkpoints

The user has been working with an agent across two commits, both linked to checkpoints.

```
  User's branch (feature):

    aaa1111 ──► bbb2222 ──► ccc3333  ← HEAD
       │           │
       │           │ Entire-Checkpoint: ff00112233aa
       │           │
       │ Entire-Checkpoint: a3b2c4d5e6f7
       │

  entire/checkpoints/v1 (orphan branch):
  ┌──────────────────────────────────────────┐
  │  a3/b2c4d5e6f7/                          │  ← checkpoint for aaa1111
  │  └── 0/full.jsonl, prompt.txt, ...       │
  │                                          │
  │  ff/00112233aa/                           │  ← checkpoint for bbb2222
  │  └── 0/full.jsonl, prompt.txt, ...       │
  └──────────────────────────────────────────┘

  Shadow branch (active session):
    entire/ccc3333-f7a8b9  ← current agent work

  Session state:
    { baseCommit: "ccc3333", phase: "ACTIVE", stepCount: 2 }
```

---

## The Rebase Begins

The user (or agent) runs `git rebase origin/main` to incorporate upstream changes.

```
  origin/main:  xxx0001 ──► xxx0002 ──► xxx0003

  User's branch BEFORE rebase:

    aaa1111 ──► bbb2222 ──► ccc3333  ← HEAD

  $ git rebase origin/main
```

---

## During Rebase: Commits Are Replayed

Git replays each commit on top of the new base. **Trailers are preserved** because they're part of the commit message.

```
  Replaying commits...

  Step 1: Replay aaa1111
  ┌──────────────────────────────────────────────────────────┐
  │  xxx0003 ──► aaa1111'                                    │
  │                │                                         │
  │                │ Entire-Checkpoint: a3b2c4d5e6f7  ✓ KEPT │
  └──────────────────────────────────────────────────────────┘

  Step 2: Replay bbb2222
  ┌──────────────────────────────────────────────────────────┐
  │  xxx0003 ──► aaa1111' ──► bbb2222'                       │
  │                              │                           │
  │                              │ Entire-Checkpoint:        │
  │                              │   ff00112233aa  ✓ KEPT    │
  └──────────────────────────────────────────────────────────┘

  Step 3: Replay ccc3333
  ┌──────────────────────────────────────────────────────────┐
  │  xxx0003 ──► aaa1111' ──► bbb2222' ──► ccc3333'  ← HEAD │
  │                                                          │
  │  (no trailer — this was an agent-only commit)            │
  └──────────────────────────────────────────────────────────┘

  Note: post-commit hook fires for each replayed commit,
        but detects rebase via .git/rebase-merge/ directory
        and SKIPS condensation. No duplicate checkpoints!
```

---

## After Rebase: New Commit Hashes, Same Trailers

```
  BEFORE rebase:                        AFTER rebase:

  aaa1111 ──► bbb2222 ──► ccc3333      xxx0003 ──► aaa1111' ──► bbb2222' ──► ccc3333'
     │           │                                     │            │
     │           │ ff00112233aa                         │            │ ff00112233aa
     │                                                 │
     │ a3b2c4d5e6f7                                    │ a3b2c4d5e6f7
                                                       │
                                                ← same checkpoint IDs! →

  Old hashes gone, but checkpoint trailers are IDENTICAL.
```

---

## Key Insight: Metadata Branch Is Untouched

The `entire/checkpoints/v1` branch is an **orphan branch** — completely independent of the user's working branch. Rebase doesn't touch it at all.

```
  entire/checkpoints/v1:
  ┌──────────────────────────────────────────┐
  │                                          │
  │  a3/b2c4d5e6f7/                          │  ← STILL HERE, unchanged
  │  └── 0/full.jsonl, prompt.txt, ...       │
  │                                          │
  │  ff/00112233aa/                           │  ← STILL HERE, unchanged
  │  └── 0/full.jsonl, prompt.txt, ...       │
  │                                          │
  └──────────────────────────────────────────┘

  The link works:
    aaa1111' commit → trailer "a3b2c4d5e6f7" → a3/b2c4d5e6f7/ on metadata branch ✓
    bbb2222' commit → trailer "ff00112233aa"  → ff/00112233aa/ on metadata branch ✓
```

---

## Shadow Branch Migration

The agent was mid-session when rebase happened. HEAD changed from `ccc3333` to `ccc3333'`. The shadow branch still points to the old hash.

```
  PROBLEM:
  ┌──────────────────────────────────────────────────────────┐
  │                                                          │
  │  Shadow branch: entire/ccc3333-f7a8b9                    │
  │                        ───┬───                           │
  │                           │                              │
  │                    old commit hash!                       │
  │                                                          │
  │  HEAD is now: ccc3333'  (different hash)                 │
  │                                                          │
  │  Session state still says: baseCommit: "ccc3333"         │
  │                                                          │
  └──────────────────────────────────────────────────────────┘
```

---

## Step 5: Agent's Next SaveStep Triggers Migration

When the agent makes its next change, `SaveStep()` detects the HEAD mismatch and migrates.

```
  SaveStep() called:
  ┌──────────────────────────────────────────────────────────┐
  │                                                          │
  │  1. Load state.BaseCommit = "ccc3333"                    │
  │  2. Read current HEAD    = "ccc3333'"                    │
  │  3. Compare: ccc3333 ≠ ccc3333'                          │
  │     → Migration needed!                                  │
  │                                                          │
  └──────────────────────────────────────────────────────────┘
                           │
                           ▼
  migrateShadowBranchIfNeeded():
  ┌──────────────────────────────────────────────────────────┐
  │                                                          │
  │  Old branch name: entire/ccc3333-f7a8b9                  │
  │  New branch name: entire/ccc3333'-f7a8b9                 │
  │                         ────┬───                         │
  │                             │                            │
  │                      new commit hash                     │
  │                                                          │
  │  Actions:                                                │
  │  ┌────────────────────────────────────────┐              │
  │  │ 1. Create ref entire/ccc3333'-f7a8b9   │              │
  │  │    pointing to same commit as old ref  │              │
  │  │                                        │              │
  │  │ 2. Delete ref entire/ccc3333-f7a8b9    │              │
  │  │                                        │              │
  │  │ 3. Update state.BaseCommit = ccc3333'  │              │
  │  └────────────────────────────────────────┘              │
  │                                                          │
  └──────────────────────────────────────────────────────────┘
```

---

## After Migration: Everything Reconnected

```
  User's branch (feature):

    xxx0003 ──► aaa1111' ──► bbb2222' ──► ccc3333'  ← HEAD
                   │            │
                   │            │ Entire-Checkpoint: ff00112233aa
                   │
                   │ Entire-Checkpoint: a3b2c4d5e6f7


  Shadow branch (migrated):
    entire/ccc3333'-f7a8b9  ← same commits, new name
    ┌────────────────────────────────────────┐
    │  step 1 → step 2  (agent's snapshots)  │
    │  All checkpoint data preserved!        │
    └────────────────────────────────────────┘


  entire/checkpoints/v1 (untouched):
  ┌──────────────────────────────────────────┐
  │  a3/b2c4d5e6f7/  ✓                       │
  │  ff/00112233aa/   ✓                       │
  └──────────────────────────────────────────┘


  Session state (updated):
    { baseCommit: "ccc3333'", phase: "ACTIVE", stepCount: 2 }
```

---

## Next Commit After Rebase: Normal Flow Resumes

When the user commits after rebase, the normal checkpoint flow runs — no special handling needed.

```
  Time ───────────────────────────────────────────────────────────────────►

  Before rebase        Rebase             Migration          Next commit
  ┌─────────────┐    ┌──────────┐    ┌──────────────┐    ┌──────────────┐
  │ ccc3333     │    │ git      │    │ SaveStep     │    │ git commit   │
  │ shadow:     │───►│ rebase   │───►│ detects HEAD │───►│ new ID:      │
  │ entire/     │    │ origin/  │    │ mismatch     │    │ bb9988776655 │
  │ ccc3333-    │    │ main     │    │ renames      │    │ condensed to │
  │ f7a8b9      │    │          │    │ shadow       │    │ metadata     │
  │             │    │ HEAD →   │    │ branch       │    │ branch       │
  │             │    │ ccc3333' │    │              │    │              │
  └─────────────┘    └──────────┘    └──────────────┘    └──────────────┘
```

---

## Why It All Works: Three Independence Properties

```
  ┌─────────────────────────────────────────────────────────────────────┐
  │                                                                     │
  │  Property 1: CHECKPOINT IDs ARE CONTENT-ADDRESSED                   │
  │  ─────────────────────────────────────────────────                  │
  │  IDs are random, not derived from commit hashes.                    │
  │  Rebase changes hashes but IDs stay the same.                       │
  │                                                                     │
  │    aaa1111  has trailer  a3b2c4d5e6f7                               │
  │    aaa1111' has trailer  a3b2c4d5e6f7  ← same!                     │
  │                                                                     │
  ├─────────────────────────────────────────────────────────────────────┤
  │                                                                     │
  │  Property 2: METADATA BRANCH IS INDEPENDENT                         │
  │  ──────────────────────────────────────────                         │
  │  entire/checkpoints/v1 is an orphan branch.                         │
  │  It has no parent relationship with user branches.                  │
  │  Rebase, reset, force-push — none affect it.                        │
  │                                                                     │
  │    User branch: rebased, hashes changed                             │
  │    Metadata branch: untouched, data intact                          │
  │                                                                     │
  ├─────────────────────────────────────────────────────────────────────┤
  │                                                                     │
  │  Property 3: SHADOW BRANCHES MIGRATE AUTOMATICALLY                  │
  │  ────────────────────────────────────────────────                   │
  │  When HEAD changes without a commit (rebase, pull),                 │
  │  the shadow branch is renamed to match the new HEAD.                │
  │  All checkpoint data on the branch is preserved.                    │
  │                                                                     │
  │    entire/ccc3333-f7a8b9 → entire/ccc3333'-f7a8b9                   │
  │    (same git objects, just a ref rename)                             │
  │                                                                     │
  └─────────────────────────────────────────────────────────────────────┘
```

---

## Edge Cases Handled

### Interactive Rebase (Squash/Edit)

```
  Before:  aaa1111 ──► bbb2222 ──► ccc3333

  User squashes bbb2222 into aaa1111:

  After:   aaa1111' ──► ccc3333'
              │
              │ Entire-Checkpoint: a3b2c4d5e6f7
              │ (from aaa1111, trailer preserved in squash)
              │
              │ Note: bbb2222's trailer (ff00112233aa) is LOST
              │ if user doesn't include it in squash message.
              │ But metadata on entire/checkpoints/v1 is still there —
              │ it's just no longer linked from a user commit.
              │ The data is preserved, only the link is broken.
```

### Rebase with Conflicts

```
  During conflict resolution:
    - .git/rebase-merge/ exists
    - post-commit hook detects this → skips condensation
    - No duplicate/spurious checkpoints created

  After conflict resolution:
    - Rebase completes, .git/rebase-merge/ removed
    - Next SaveStep detects HEAD change → migrates shadow branch
    - Normal flow resumes
```

### Force Push After Rebase

```
  $ git push --force

  pre-push hook fires:
    - Pushes entire/checkpoints/v1 branch alongside user's branch
    - Remote now has all checkpoint metadata
    - Links work on remote too (trailers in commits → metadata on metadata branch)
```
