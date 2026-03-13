# Animation: How Checkpoints Are Created During Commits

This document walks through checkpoint creation step-by-step, showing the state of git refs, files, and metadata at each stage.

---

## Setup: Agent Session in Progress

The agent has been working — making file changes and saving checkpoints to a shadow branch.

```
                         ┌─────────────────────────────┐
  User's branch (main)   │  commit abc1234  ← HEAD     │
                         └─────────────────────────────┘

  Shadow branch           ┌─────────────────────────────┐
  entire/abc1234-f7a8b9   │  step 1 → step 2 → step 3  │
                          │  (agent's work snapshots)    │
                          └─────────────────────────────┘

  Working directory:
    ├── src/auth.go        ← modified by agent
    ├── src/auth_test.go   ← modified by agent
    └── README.md          ← untouched

  Session state (.git/entire-sessions/<session-id>.json):
    { phase: "IDLE", baseCommit: "abc1234", stepCount: 3 }
```

---

## Step 1: User Runs `git commit`

The user stages changes and runs `git commit`. Git fires the `prepare-commit-msg` hook **before** the editor opens.

```
  ┌──────────────────────────────────────────────────────────┐
  │  $ git add src/auth.go src/auth_test.go                  │
  │  $ git commit                                            │
  │                                                          │
  │  Git fires: prepare-commit-msg .git/COMMIT_EDITMSG       │
  └────────────────────────┬─────────────────────────────────┘
                           │
                           ▼
```

---

## Step 2: prepare-commit-msg Hook

The hook generates a checkpoint ID and adds it as a trailer to the commit message.

```
  prepare-commit-msg hook:
  ┌──────────────────────────────────────────────────────────┐
  │                                                          │
  │  1. Find sessions for this worktree                      │
  │     → Found: session "2026-03-13-uuid" (phase: IDLE)     │
  │                                                          │
  │  2. Check if session has new content                     │
  │     → Shadow branch has 3 checkpoints ✓                  │
  │                                                          │
  │  3. Generate checkpoint ID                               │
  │     → id.Generate() = "a3b2c4d5e6f7"                     │
  │       (6 random bytes → 12 hex chars)                    │
  │                                                          │
  │  4. Append trailer to commit message                     │
  │                                                          │
  └──────────────────────────────────────────────────────────┘

  .git/COMMIT_EDITMSG before:        .git/COMMIT_EDITMSG after:
  ┌────────────────────────┐         ┌──────────────────────────────────────┐
  │ Add authentication     │         │ Add authentication                   │
  │                        │   ───►  │                                      │
  │                        │         │ Entire-Checkpoint: a3b2c4d5e6f7      │
  └────────────────────────┘         └──────────────────────────────────────┘
```

---

## Step 3: User Confirms Commit

The editor opens with the trailer already in place. The user saves and closes. Git creates the commit.

```
  User's branch (main):

    abc1234 ──► def5678  ← HEAD (new commit)
                  │
                  │  message: "Add authentication"
                  │  trailer: Entire-Checkpoint: a3b2c4d5e6f7
                  │
                  ▼
  Git fires: post-commit
```

---

## Step 4: post-commit Hook — Phase Transition

The hook reads the new commit, finds the checkpoint trailer, and triggers condensation.

```
  post-commit hook:
  ┌──────────────────────────────────────────────────────────┐
  │                                                          │
  │  1. Read HEAD commit message                             │
  │     → Found trailer: "Entire-Checkpoint: a3b2c4d5e6f7"  │
  │                                                          │
  │  2. Phase state machine transition:                      │
  │                                                          │
  │     ┌──────┐  GitCommit   ┌──────┐                      │
  │     │ IDLE │ ───────────► │ IDLE │                       │
  │     └──────┘              └──────┘                       │
  │                   action: [Condense]                     │
  │                                                          │
  │  3. Proceed to condensation...                           │
  │                                                          │
  └──────────────────────────────────────────────────────────┘
```

---

## Step 5: Condensation — Extract Session Data

The hook reads the shadow branch and live transcript, extracts all session data.

```
  ┌─ Shadow branch: entire/abc1234-f7a8b9 ─────────────────┐
  │                                                          │
  │  .entire/metadata/<session-id>/                          │
  │  ├── full.jsonl          ← session transcript            │
  │  ├── prompt.txt          ← user prompts                  │
  │  └── tasks/              ← subagent data (if any)        │
  │                                                          │
  └──────────────────────────────────────────────────────────┘
                           │
                           │  Extract & collect
                           ▼
  ┌──────────────────────────────────────────────────────────┐
  │  Condensation collects:                                  │
  │                                                          │
  │  • Transcript (full.jsonl) — prefer live file            │
  │  • Prompts (prompt.txt) — from shadow branch             │
  │  • Token usage — parsed from transcript                  │
  │  • Files touched — from session state                    │
  │  • Attribution — agent vs human line changes             │
  │                                                          │
  └──────────────────────────────────────────────────────────┘
```

---

## Step 6: Condensation — Write to Metadata Branch

The collected data is written to the `entire/checkpoints/v1` orphan branch using sharded paths.

```
  Checkpoint ID: a3b2c4d5e6f7
  Sharded path:  a3/b2c4d5e6f7/

  entire/checkpoints/v1 branch (orphan):
  ┌──────────────────────────────────────────────────────────┐
  │                                                          │
  │  Tree at HEAD:                                           │
  │                                                          │
  │  a3/b2c4d5e6f7/                     ← NEW               │
  │  ├── metadata.json                                       │
  │  │   {                                                   │
  │  │     "checkpoint_id": "a3b2c4d5e6f7",                  │
  │  │     "session_id": "2026-03-13-uuid",                  │
  │  │     "session_ids": ["2026-03-13-uuid"],               │
  │  │     "session_count": 1,                               │
  │  │     "strategy": "manual-commit",                      │
  │  │     "files_touched": ["src/auth.go","src/auth_test.go"]│
  │  │   }                                                   │
  │  ├── 0/                              ← session folder    │
  │  │   ├── metadata.json               (agent, model, etc) │
  │  │   ├── full.jsonl                  (transcript)        │
  │  │   ├── prompt.txt                  (user prompts)      │
  │  │   └── content_hash.txt            (SHA256)            │
  │  │                                                       │
  │  (previous checkpoints remain untouched...)              │
  │                                                          │
  └──────────────────────────────────────────────────────────┘

  Commit on entire/checkpoints/v1:
    subject: "Checkpoint: a3b2c4d5e6f7"
    trailers:
      Entire-Session: 2026-03-13-uuid
      Entire-Strategy: manual-commit
      Entire-Agent: Claude Code
```

---

## Step 7: Update State & Cleanup

Session state is updated and the old shadow branch is cleaned up.

```
  Session state BEFORE:                 Session state AFTER:
  ┌──────────────────────────┐          ┌──────────────────────────┐
  │ baseCommit: abc1234      │          │ baseCommit: def5678      │
  │ stepCount: 3             │   ───►   │ stepCount: 0             │
  │ filesTouched: [auth.go,  │          │ filesTouched: []         │
  │   auth_test.go]          │          │ lastCheckpointID:        │
  │ lastCheckpointID: ""     │          │   a3b2c4d5e6f7           │
  └──────────────────────────┘          └──────────────────────────┘

  Shadow branch cleanup:
    entire/abc1234-f7a8b9  → DELETED (no more active sessions need it)
```

---

## Final State: The Bidirectional Link

```
  User's branch (main):
  ┌─────────────────────────────────────────────────────────┐
  │                                                         │
  │  abc1234 ──► def5678  ← HEAD                            │
  │               │                                         │
  │               │ trailer: Entire-Checkpoint: a3b2c4d5e6f7│
  │               │                    │                    │
  └───────────────│────────────────────│────────────────────┘
                  │                    │
                  │       ┌────────────┘
                  │       │  Linked by checkpoint ID
                  │       │
                  │       ▼
  entire/checkpoints/v1:
  ┌─────────────────────────────────────────────────────────┐
  │                                                         │
  │  a3/b2c4d5e6f7/                                         │
  │  ├── metadata.json  ← checkpoint_id: "a3b2c4d5e6f7"    │
  │  └── 0/                                                 │
  │      ├── full.jsonl ← complete session transcript       │
  │      ├── prompt.txt ← what the user asked               │
  │      └── ...                                            │
  │                                                         │
  └─────────────────────────────────────────────────────────┘

  Navigation:
    commit → metadata:  Read trailer → look up a3/b2c4d5e6f7/ on metadata branch
    metadata → commit:  Search branch for commits with Entire-Checkpoint: a3b2c4d5e6f7
```

---

## Summary Timeline

```
  Time ──────────────────────────────────────────────────────────────────►

  Agent works     User commits          Hooks run              Done
  ┌─────────┐    ┌──────────┐    ┌────────────────────┐    ┌─────────┐
  │ SaveStep │    │ git      │    │ prepare-commit-msg │    │ Linked! │
  │ SaveStep │───►│ commit   │───►│  → generate ID     │───►│         │
  │ SaveStep │    │          │    │ post-commit         │    │ Ready   │
  │          │    │          │    │  → condense         │    │ for     │
  │ shadow   │    │ trailer  │    │  → write metadata   │    │ rewind  │
  │ branch   │    │ added    │    │  → cleanup          │    │         │
  └─────────┘    └──────────┘    └────────────────────┘    └─────────┘
```
