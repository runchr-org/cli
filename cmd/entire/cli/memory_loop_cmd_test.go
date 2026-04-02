package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
	"github.com/entireio/cli/cmd/entire/cli/memoryloop"
	"github.com/entireio/cli/cmd/entire/cli/memorylooptui"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
	"github.com/stretchr/testify/require"
)

func TestFilterMemoryLoopRows_ScopeMe(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 26, 12, 0, 0, 0, time.UTC)
	rows := []insightsdb.SessionRow{
		{SessionID: "mine-newer", OwnerID: "alishakawaguchi", Branch: "main", CreatedAt: now.Add(-time.Hour)},
		{SessionID: "coworker", OwnerID: "teammate", Branch: "main", CreatedAt: now.Add(-2 * time.Hour)},
		{SessionID: "mine-older", OwnerID: "alishakawaguchi", Branch: "feature", CreatedAt: now.Add(-3 * time.Hour)},
	}

	filtered, err := filterMemoryLoopRows(rows, memoryLoopScope{
		Mode:    memoryLoopScopeMe,
		OwnerID: "alishakawaguchi",
	}, 10)
	require.NoError(t, err)
	require.Len(t, filtered, 2)
	require.Equal(t, "mine-newer", filtered[0].SessionID)
	require.Equal(t, "mine-older", filtered[1].SessionID)
}

func TestFilterMemoryLoopRows_ScopeBranch(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 26, 12, 0, 0, 0, time.UTC)
	rows := []insightsdb.SessionRow{
		{SessionID: "main-newer", Branch: "main", CreatedAt: now.Add(-time.Hour)},
		{SessionID: "feature", Branch: "feature/owner", CreatedAt: now.Add(-2 * time.Hour)},
		{SessionID: "main-older", Branch: "main", CreatedAt: now.Add(-3 * time.Hour)},
	}

	filtered, err := filterMemoryLoopRows(rows, memoryLoopScope{
		Mode:   memoryLoopScopeBranch,
		Branch: "main",
	}, 10)
	require.NoError(t, err)
	require.Len(t, filtered, 2)
	require.Equal(t, "main-newer", filtered[0].SessionID)
	require.Equal(t, "main-older", filtered[1].SessionID)
}

func TestQueryMemoryLoopRows_RepoScopeFiltersByDefaultBranchReachabilityBeforeLimiting(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	gitRunMemoryLoop(t, tmpDir, "branch", "-m", "main")
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, paths.EntireDir), 0o755))
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()
	t.Cleanup(paths.ClearWorktreeRootCache)

	idb, err := insightsdb.Open(filepath.Join(tmpDir, paths.EntireDir, "insights.db"))
	require.NoError(t, err)
	defer func() { require.NoError(t, idb.Close()) }()

	testutil.WriteFile(t, tmpDir, "main.txt", "main")
	testutil.GitAdd(t, tmpDir, "main.txt")
	testutil.GitCommit(t, tmpDir, "main checkpoint\n\nEntire-Checkpoint: aaaabbbbcccc\n")

	testutil.GitCheckoutNewBranch(t, tmpDir, "feature/merged")
	testutil.WriteFile(t, tmpDir, "merged.txt", "merged")
	testutil.GitAdd(t, tmpDir, "merged.txt")
	testutil.GitCommit(t, tmpDir, "merged checkpoint\n\nEntire-Checkpoint: dddd1111eeee\n")

	gitCheckout(t, tmpDir, "main")
	gitRunMemoryLoop(t, tmpDir, "merge", "--no-ff", "--no-edit", "feature/merged")

	testutil.GitCheckoutNewBranch(t, tmpDir, "feature/unmerged")
	testutil.WriteFile(t, tmpDir, "unmerged.txt", "unmerged")
	testutil.GitAdd(t, tmpDir, "unmerged.txt")
	testutil.GitCommit(t, tmpDir, "unmerged checkpoint\n\nEntire-Checkpoint: ffff2222aaaa\n")

	now := time.Date(2026, time.April, 2, 12, 0, 0, 0, time.UTC)
	require.NoError(t, idb.InsertSession(context.Background(), insightsdb.SessionRow{
		CheckpointID: "aaaabbbbcccc",
		SessionID:    "session-main",
		SessionIndex: 0,
		Branch:       "main",
		CreatedAt:    now.Add(-3 * time.Hour),
	}))
	require.NoError(t, idb.InsertSession(context.Background(), insightsdb.SessionRow{
		CheckpointID: "dddd1111eeee",
		SessionID:    "session-merged",
		SessionIndex: 0,
		Branch:       "feature/merged",
		CreatedAt:    now.Add(-2 * time.Hour),
	}))
	require.NoError(t, idb.InsertSession(context.Background(), insightsdb.SessionRow{
		CheckpointID: "ffff2222aaaa",
		SessionID:    "session-unmerged",
		SessionIndex: 0,
		Branch:       "feature/unmerged",
		CreatedAt:    now.Add(-1 * time.Hour),
	}))

	rows, err := queryMemoryLoopRows(context.Background(), idb, memoryLoopScope{Mode: memoryLoopScopeRepo}, 2)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "dddd1111eeee", rows[0].CheckpointID)
	require.Equal(t, "aaaabbbbcccc", rows[1].CheckpointID)
}

func TestRunMemoryLoopShow_GroupsDetailedInventory(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	now := time.Date(2026, time.March, 26, 12, 0, 0, 0, time.UTC)
	state := &memoryloop.State{
		Store: &memoryloop.Store{
			Version:          1,
			GeneratedAt:      now,
			SourceWindow:     20,
			Scope:            "me",
			ScopeValue:       "alishakawaguchi",
			Mode:             memoryloop.ModeManual,
			ActivationPolicy: memoryloop.ActivationPolicyAuto,
			Records: []memoryloop.MemoryRecord{
				{ID: "active", Title: "Active memory", Body: "body", Kind: memoryloop.KindRepoRule, Status: memoryloop.StatusActive},
				{ID: "candidate", Title: "Candidate memory", Body: "body", Kind: memoryloop.KindRepoRule, Status: memoryloop.StatusCandidate},
				{ID: "suppressed", Title: "Suppressed memory", Body: "body", Kind: memoryloop.KindRepoRule, Status: memoryloop.StatusSuppressed},
				{ID: "archived", Title: "Archived memory", Body: "body", Kind: memoryloop.KindRepoRule, Status: memoryloop.StatusArchived},
			},
			InjectionEnabled: true,
			MaxInjected:      3,
			RefreshHistory: []memoryloop.RefreshHistory{
				{At: now, Scope: "me", ScopeValue: "alishakawaguchi", GeneratedCount: 2, ActivatedCount: 1, CandidateCount: 1},
			},
		},
	}
	require.NoError(t, memoryloop.SaveState(context.Background(), state))

	var buf bytes.Buffer
	require.NoError(t, runMemoryLoopShow(context.Background(), &buf, ""))

	out := buf.String()
	require.Contains(t, out, "Mode: manual")
	require.Contains(t, out, "Activation policy: auto")
	require.Contains(t, out, "Active Memories")
	require.Contains(t, out, "Candidate Memories")
	require.Contains(t, out, "Suppressed Memories")
	require.Contains(t, out, "Archived Memories")
	require.Contains(t, out, "Active memory [repo_rule] (active)")
	require.Contains(t, out, "Candidate memory [repo_rule] (candidate)")
	require.Contains(t, out, "Suppressed memory [repo_rule] (suppressed)")
	require.Contains(t, out, "Archived memory [repo_rule] (archived)")
	require.Contains(t, out, "Recent Refreshes")
	require.Contains(t, out, "generated 2, activated 1, candidate 1")
	require.Contains(t, out, "Recent Injections")
}

func TestRunMemoryLoopShow_DisplaysRecentLifecycleReasons(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	now := time.Date(2026, time.March, 26, 12, 5, 0, 0, time.UTC)
	state := &memoryloop.State{
		Store: &memoryloop.Store{
			Version:      1,
			GeneratedAt:  now,
			SourceWindow: 20,
			Mode:         memoryloop.ModeManual,
			Records: []memoryloop.MemoryRecord{
				{
					ID:        "pruned-active",
					Title:     "Old generated rule",
					Body:      "Prune me with a reason.",
					Kind:      memoryloop.KindRepoRule,
					Status:    memoryloop.StatusArchived,
					Origin:    memoryloop.OriginGenerated,
					History:   []memoryloop.HistoryEvent{{Type: "pruned", At: now, Detail: "stale_unmatched_active"}},
					ScopeKind: memoryloop.ScopeKindMe,
				},
				{
					ID:        "demoted-active",
					Title:     "Ineffective generated rule",
					Body:      "Demote me with a reason.",
					Kind:      memoryloop.KindRepoRule,
					Status:    memoryloop.StatusCandidate,
					Origin:    memoryloop.OriginGenerated,
					History:   []memoryloop.HistoryEvent{{Type: "demoted", At: now, Detail: "ineffective_active"}},
					ScopeKind: memoryloop.ScopeKindMe,
				},
			},
		},
	}
	require.NoError(t, memoryloop.SaveState(context.Background(), state))

	var buf bytes.Buffer
	require.NoError(t, runMemoryLoopShow(context.Background(), &buf, ""))

	out := buf.String()
	require.Contains(t, out, "stale_unmatched_active")
	require.Contains(t, out, "ineffective_active")
	require.Contains(t, out, "pruned")
	require.Contains(t, out, "demoted")
}

func TestRunMemoryLoopShow_ListsExtendedRefreshHistoryCounters(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	now := time.Date(2026, time.March, 26, 12, 10, 0, 0, time.UTC)
	state := &memoryloop.State{
		Store: &memoryloop.Store{
			Version:      1,
			GeneratedAt:  now,
			SourceWindow: 20,
			Mode:         memoryloop.ModeManual,
			RefreshHistory: []memoryloop.RefreshHistory{
				{
					At:             now,
					Scope:          "me",
					ScopeValue:     "test@example.com",
					GeneratedCount: 4,
					ActivatedCount: 2,
					CandidateCount: 1,
				},
			},
		},
	}
	require.NoError(t, memoryloop.SaveState(context.Background(), state))

	var buf bytes.Buffer
	require.NoError(t, runMemoryLoopShow(context.Background(), &buf, ""))

	out := buf.String()
	require.Contains(t, out, "filtered weak")
	require.Contains(t, out, "filtered generic")
	require.Contains(t, out, "deduped")
	require.Contains(t, out, "demoted")
	require.Contains(t, out, "pruned")
}

func TestRunMemoryLoopShow_PrunesDeletedBranchScopedMemories(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()
	t.Cleanup(paths.ClearWorktreeRootCache)

	now := time.Date(2026, time.April, 2, 12, 0, 0, 0, time.UTC)
	state := &memoryloop.State{
		Store: &memoryloop.Store{
			Version:      1,
			GeneratedAt:  now,
			SourceWindow: 20,
			Records: []memoryloop.MemoryRecord{
				{
					ID:         "branch-stale",
					Title:      "Only for feature-x",
					Body:       "Branch-scoped memory",
					Kind:       memoryloop.KindRepoRule,
					Status:     memoryloop.StatusActive,
					ScopeKind:  memoryloop.ScopeKindBranch,
					ScopeValue: "feature-x",
				},
				{
					ID:        "repo-keep",
					Title:     "Keep repo memory",
					Body:      "Repo-scoped memory",
					Kind:      memoryloop.KindRepoRule,
					Status:    memoryloop.StatusActive,
					ScopeKind: memoryloop.ScopeKindRepo,
				},
			},
		},
	}
	require.NoError(t, memoryloop.SaveState(context.Background(), state))

	var buf bytes.Buffer
	require.NoError(t, runMemoryLoopShow(context.Background(), &buf, ""))

	loaded, err := memoryloop.LoadState(context.Background())
	require.NoError(t, err)
	require.Len(t, loaded.Store.Records, 1)
	require.Equal(t, "repo-keep", loaded.Store.Records[0].ID)
	require.NotContains(t, buf.String(), "Only for feature-x")
}

func TestPruneDeletedBranchScopedMemories_KeepsExistingBranchRecords(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	testutil.GitCheckoutNewBranch(t, tmpDir, "feature-x")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()
	t.Cleanup(paths.ClearWorktreeRootCache)

	state := &memoryloop.State{
		Store: &memoryloop.Store{
			Version: 1,
			Records: []memoryloop.MemoryRecord{
				{
					ID:         "branch-keep",
					Title:      "Only for feature-x",
					Body:       "Branch-scoped memory",
					Kind:       memoryloop.KindRepoRule,
					Status:     memoryloop.StatusActive,
					ScopeKind:  memoryloop.ScopeKindBranch,
					ScopeValue: "feature-x",
				},
			},
		},
	}

	changed, err := pruneDeletedBranchScopedMemories(context.Background(), state)
	require.NoError(t, err)
	require.False(t, changed)
	require.Len(t, state.Store.Records, 1)
	require.Equal(t, "branch-keep", state.Store.Records[0].ID)
}

func TestRunMemoryLoopStatus_IsConciseAndSupportsVerbosePromptPreview(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	installFakeGitHubCLI(t, "#!/bin/sh\ncat <<'EOF'\ngithub.com\n  ✓ Logged in to github.com account alishakawaguchi (/tmp/hosts.yml)\n  - Active account: true\nEOF\n")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()
	t.Cleanup(paths.ClearWorktreeRootCache)

	now := time.Date(2026, time.March, 26, 12, 0, 0, 0, time.UTC)
	state := &memoryloop.State{
		Store: &memoryloop.Store{
			Version:          1,
			GeneratedAt:      now,
			Scope:            "me",
			ScopeValue:       "alishakawaguchi",
			Mode:             memoryloop.ModeAuto,
			ActivationPolicy: memoryloop.ActivationPolicyReview,
			MaxInjected:      3,
			Records: []memoryloop.MemoryRecord{
				{
					ID:         "lint",
					Title:      "Run lint before finishing",
					Body:       "Run golangci-lint before claiming completion.",
					Kind:       memoryloop.KindRepoRule,
					Confidence: "high",
					Strength:   5,
					ScopeKind:  memoryloop.ScopeKindMe,
					ScopeValue: "alishakawaguchi",
					Status:     memoryloop.StatusActive,
				},
				{
					ID:        "candidate-skill",
					Title:     "Tighten the project skill",
					Body:      "Add the missing retry step.",
					Kind:      memoryloop.KindSkillPatch,
					ScopeKind: memoryloop.ScopeKindMe,
					Status:    memoryloop.StatusCandidate,
				},
			},
		},
	}
	require.NoError(t, memoryloop.SaveState(context.Background(), state))

	var buf bytes.Buffer
	require.NoError(t, runMemoryLoopStatus(context.Background(), &buf, "fix the lint failure", true))

	out := buf.String()
	require.Contains(t, out, "Last refresh: 2026-03-26T12:00:00Z")
	require.Contains(t, out, "Mode: auto")
	require.Contains(t, out, "Activation policy: review")
	require.Contains(t, out, "Active memories: 1")
	require.Contains(t, out, "Candidate memories: 1")
	require.NotContains(t, out, "Recent Injections")
	require.NotContains(t, out, "Active Memories")
	require.Contains(t, out, "Prompt Preview")
	require.Contains(t, out, "Memory For This Repo")
	require.Contains(t, out, "lint [repo_rule] score=")
	require.Contains(t, out, "base_score=")
	require.Contains(t, out, "adjusted_score=")
	require.Contains(t, out, "reason=")
	require.Contains(t, out, "scope=me(alishakawaguchi)")
	require.Contains(t, out, "status=active")
}

func TestRunMemoryLoopStatus_NonVerbosePreviewOmitsSelectionRationale(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	installFakeGitHubCLI(t, "#!/bin/sh\ncat <<'EOF'\ngithub.com\n  ✓ Logged in to github.com account alishakawaguchi (/tmp/hosts.yml)\n  - Active account: true\nEOF\n")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	now := time.Date(2026, time.March, 26, 12, 0, 0, 0, time.UTC)
	state := &memoryloop.State{
		Store: &memoryloop.Store{
			Version:          1,
			GeneratedAt:      now,
			Scope:            "me",
			ScopeValue:       "alishakawaguchi",
			Mode:             memoryloop.ModeAuto,
			ActivationPolicy: memoryloop.ActivationPolicyReview,
			MaxInjected:      3,
			Records: []memoryloop.MemoryRecord{
				{
					ID:         "lint",
					Title:      "Run lint before finishing",
					Body:       "Run golangci-lint before claiming completion.",
					Kind:       memoryloop.KindRepoRule,
					Confidence: "high",
					Strength:   5,
					ScopeKind:  memoryloop.ScopeKindMe,
					ScopeValue: "alishakawaguchi",
					Status:     memoryloop.StatusActive,
				},
			},
		},
	}
	require.NoError(t, memoryloop.SaveState(context.Background(), state))

	var buf bytes.Buffer
	require.NoError(t, runMemoryLoopStatus(context.Background(), &buf, "fix the lint failure", false))

	out := buf.String()
	require.Contains(t, out, "Prompt Preview")
	require.Contains(t, out, "Memory For This Repo")
	require.NotContains(t, out, "base_score=")
	require.NotContains(t, out, "adjusted_score=")
	require.NotContains(t, out, "cooldown_penalty=")
	require.NotContains(t, out, "outcome_bonus=")
	require.NotContains(t, out, "scope_bonus=")
}

func TestRunMemoryLoopStatus_VerbosePreviewShowsSelectionRationale(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	installFakeGitHubCLI(t, "#!/bin/sh\ncat <<'EOF'\ngithub.com\n  ✓ Logged in to github.com account alishakawaguchi (/tmp/hosts.yml)\n  - Active account: true\nEOF\n")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	now := time.Date(2026, time.March, 26, 12, 0, 0, 0, time.UTC)
	state := &memoryloop.State{
		Store: &memoryloop.Store{
			Version:          1,
			GeneratedAt:      now,
			Scope:            "me",
			ScopeValue:       "alishakawaguchi",
			Mode:             memoryloop.ModeAuto,
			ActivationPolicy: memoryloop.ActivationPolicyReview,
			MaxInjected:      3,
			Records: []memoryloop.MemoryRecord{
				{
					ID:             "lint",
					Title:          "Run lint before finishing",
					Body:           "Run golangci-lint before claiming completion.",
					Kind:           memoryloop.KindRepoRule,
					Confidence:     "high",
					Strength:       5,
					Outcome:        memoryloop.OutcomeReinforced,
					LastInjectedAt: now.Add(-10 * time.Minute),
					ScopeKind:      memoryloop.ScopeKindMe,
					ScopeValue:     "alishakawaguchi",
					Status:         memoryloop.StatusActive,
				},
			},
		},
	}
	require.NoError(t, memoryloop.SaveState(context.Background(), state))

	var buf bytes.Buffer
	require.NoError(t, runMemoryLoopStatus(context.Background(), &buf, "fix the lint failure", true))

	out := buf.String()
	require.Contains(t, out, "Prompt Preview")
	require.Contains(t, out, "base_score=")
	require.Contains(t, out, "adjusted_score=")
	require.Contains(t, out, "cooldown_penalty=")
	require.Contains(t, out, "outcome_bonus=")
	require.Contains(t, out, "scope_bonus=")
}

func TestRunMemoryLoopStatus_VerbosePreviewExplainsSkippedCandidates(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	installFakeGitHubCLI(t, "#!/bin/sh\ncat <<'EOF'\ngithub.com\n  ✓ Logged in to github.com account alishakawaguchi (/tmp/hosts.yml)\n  - Active account: true\nEOF\n")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	now := time.Date(2026, time.March, 26, 12, 20, 0, 0, time.UTC)
	state := &memoryloop.State{
		Store: &memoryloop.Store{
			Version:          1,
			GeneratedAt:      now,
			Scope:            "me",
			ScopeValue:       "alishakawaguchi",
			Mode:             memoryloop.ModeAuto,
			ActivationPolicy: memoryloop.ActivationPolicyReview,
			MaxInjected:      1,
			Records: []memoryloop.MemoryRecord{
				{
					ID:             "cooldown",
					Title:          "Run lint before finishing",
					Body:           "Run golangci-lint before claiming completion.",
					Kind:           memoryloop.KindRepoRule,
					Confidence:     "high",
					Strength:       4,
					Outcome:        memoryloop.OutcomeReinforced,
					LastInjectedAt: now.Add(-5 * time.Minute),
					ScopeKind:      memoryloop.ScopeKindMe,
					ScopeValue:     "alishakawaguchi",
					Status:         memoryloop.StatusActive,
				},
				{
					ID:         "scope",
					Title:      "Keep repo rule concise",
					Body:       "Use concise repo guidance.",
					Kind:       memoryloop.KindRepoRule,
					Confidence: "high",
					Strength:   4,
					ScopeKind:  memoryloop.ScopeKindRepo,
					ScopeValue: "main",
					Status:     memoryloop.StatusActive,
				},
				{
					ID:         "diversity",
					Title:      "Run lint before wrapping up",
					Body:       "Run golangci-lint before you say the task is done.",
					Kind:       memoryloop.KindRepoRule,
					Confidence: "high",
					Strength:   4,
					ScopeKind:  memoryloop.ScopeKindMe,
					ScopeValue: "alishakawaguchi",
					Status:     memoryloop.StatusActive,
				},
				{
					ID:         "byte-budget",
					Title:      "Keep commit messages short",
					Body:       "Use concise commit subjects and avoid verbose summaries when closing tasks.",
					Kind:       memoryloop.KindWorkflowRule,
					Confidence: "high",
					Strength:   4,
					ScopeKind:  memoryloop.ScopeKindMe,
					ScopeValue: "alishakawaguchi",
					Status:     memoryloop.StatusActive,
				},
			},
		},
	}
	require.NoError(t, memoryloop.SaveState(context.Background(), state))

	var buf bytes.Buffer
	require.NoError(t, runMemoryLoopStatus(context.Background(), &buf, "fix the lint failure and keep the repo rule concise", true))

	out := buf.String()
	require.Contains(t, out, "skipped by cooldown")
	require.Contains(t, out, "skipped by scope preference")
	require.Contains(t, out, "skipped by diversity quota")
	require.Contains(t, out, "skipped by byte budget")
}

func TestSetMemoryLoopMode_PersistsAuthoritativeMode(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	state := &memoryloop.State{
		Store: &memoryloop.Store{
			Version:          1,
			GeneratedAt:      time.Date(2026, time.March, 26, 12, 0, 0, 0, time.UTC),
			SourceWindow:     20,
			Mode:             memoryloop.ModeAuto,
			ActivationPolicy: memoryloop.ActivationPolicyReview,
			InjectionEnabled: true,
			MaxInjected:      3,
		},
	}
	require.NoError(t, memoryloop.SaveState(context.Background(), state))

	var buf bytes.Buffer
	require.NoError(t, setMemoryLoopMode(context.Background(), &buf, memoryloop.ModeOff))

	loaded, err := memoryloop.LoadState(context.Background())
	require.NoError(t, err)
	require.Equal(t, memoryloop.ModeOff, loaded.Store.Mode)
	require.False(t, loaded.Store.InjectionEnabled)
	require.Contains(t, buf.String(), "Memory loop mode: off")
}

func TestSetMemoryLoopMode_CreatesStoreBeforeRefresh(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	var buf bytes.Buffer
	require.NoError(t, setMemoryLoopMode(context.Background(), &buf, memoryloop.ModeManual))

	loaded, err := memoryloop.LoadState(context.Background())
	require.NoError(t, err)
	require.NotNil(t, loaded.Store)
	require.Equal(t, memoryloop.ModeManual, loaded.Store.Mode)
	require.Equal(t, memoryloop.ActivationPolicyReview, loaded.Store.ActivationPolicy)
	require.Equal(t, memoryloop.DefaultMaxInjected, loaded.Store.MaxInjected)
	require.Contains(t, buf.String(), "Memory loop mode: manual")
}

func TestSetMemoryLoopPolicy_PersistsActivationPolicy(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	state := &memoryloop.State{
		Store: &memoryloop.Store{
			Version:          1,
			GeneratedAt:      time.Date(2026, time.March, 26, 12, 0, 0, 0, time.UTC),
			SourceWindow:     20,
			Mode:             memoryloop.ModeManual,
			ActivationPolicy: memoryloop.ActivationPolicyReview,
			MaxInjected:      3,
		},
	}
	require.NoError(t, memoryloop.SaveState(context.Background(), state))

	var buf bytes.Buffer
	require.NoError(t, setMemoryLoopPolicy(context.Background(), &buf, memoryloop.ActivationPolicyAuto))

	loaded, err := memoryloop.LoadState(context.Background())
	require.NoError(t, err)
	require.Equal(t, memoryloop.ActivationPolicyAuto, loaded.Store.ActivationPolicy)
	require.Contains(t, buf.String(), "Memory loop activation policy: auto")
}

func TestSetMemoryLoopPolicy_CreatesStoreBeforeRefresh(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	var buf bytes.Buffer
	require.NoError(t, setMemoryLoopPolicy(context.Background(), &buf, memoryloop.ActivationPolicyAuto))

	loaded, err := memoryloop.LoadState(context.Background())
	require.NoError(t, err)
	require.NotNil(t, loaded.Store)
	require.Equal(t, memoryloop.ModeOff, loaded.Store.Mode)
	require.Equal(t, memoryloop.ActivationPolicyAuto, loaded.Store.ActivationPolicy)
	require.Equal(t, memoryloop.DefaultMaxInjected, loaded.Store.MaxInjected)
	require.Contains(t, buf.String(), "Memory loop activation policy: auto")
}

func TestSetMemoryLoopDebug_PersistsToExistingStore(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	state := &memoryloop.State{
		Store: &memoryloop.Store{
			Version:          1,
			GeneratedAt:      time.Date(2026, time.March, 26, 12, 0, 0, 0, time.UTC),
			SourceWindow:     20,
			Mode:             memoryloop.ModeManual,
			ActivationPolicy: memoryloop.ActivationPolicyReview,
			MaxInjected:      3,
		},
	}
	require.NoError(t, memoryloop.SaveState(context.Background(), state))

	var buf bytes.Buffer
	require.NoError(t, setMemoryLoopDebug(context.Background(), &buf, true))

	loaded, err := memoryloop.LoadState(context.Background())
	require.NoError(t, err)
	require.True(t, loaded.Store.Debug)
	require.Contains(t, buf.String(), "Memory loop debug: on")
}

func TestSetMemoryLoopDebug_CreatesStoreBeforeRefresh(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	var buf bytes.Buffer
	require.NoError(t, setMemoryLoopDebug(context.Background(), &buf, true))

	loaded, err := memoryloop.LoadState(context.Background())
	require.NoError(t, err)
	require.NotNil(t, loaded.Store)
	require.Equal(t, memoryloop.ModeOff, loaded.Store.Mode)
	require.True(t, loaded.Store.Debug)
	require.Contains(t, buf.String(), "Memory loop debug: on")
}

func TestSetMemoryLoopMode_InvalidSettingsReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".entire"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, settings.EntireSettingsFile), []byte(`{
  "memory_loop": {
    "mode": "manuall"
  }
}`), 0o644))
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	var buf bytes.Buffer
	err := setMemoryLoopMode(context.Background(), &buf, memoryloop.ModeManual)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid memory_loop.mode")

	loaded, loadErr := memoryloop.LoadState(context.Background())
	require.NoError(t, loadErr)
	require.Nil(t, loaded.Store)
}

func TestSetMemoryLoopPolicy_InvalidSettingsReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".entire"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, settings.EntireSettingsFile), []byte(`{
  "memory_loop": {
    "activation_policy": "autoreview"
  }
}`), 0o644))
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	var buf bytes.Buffer
	err := setMemoryLoopPolicy(context.Background(), &buf, memoryloop.ActivationPolicyAuto)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid memory_loop.activation_policy")

	loaded, loadErr := memoryloop.LoadState(context.Background())
	require.NoError(t, loadErr)
	require.Nil(t, loaded.Store)
}

func TestRunMemoryLoopRefresh_InvalidSettingsReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".entire"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, settings.EntireSettingsFile), []byte(`{
  "memory_loop": {
    "activation_policy": "autoreview"
  }
}`), 0o644))
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	var buf bytes.Buffer
	err := runMemoryLoopRefresh(context.Background(), &buf, 10, "repo", "", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid memory_loop.activation_policy")
}

func TestRunMemoryLoopRefresh_ScopeMeUsesGitHubUsername(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".entire"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, settings.EntireSettingsFile), []byte(`{
  "memory_loop": {
    "mode": "manual"
  }
}`), 0o644))
	installFakeGitHubCLI(t, "#!/bin/sh\ncat <<'EOF'\ngithub.com\n  X Failed to log in to github.com account alishakawaguchi (default)\n  - Active account: true\nEOF\nexit 1\n")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	var buf bytes.Buffer
	require.NoError(t, runMemoryLoopRefresh(context.Background(), &buf, 10, "me", "", false))
	require.Contains(t, buf.String(), `No personal sessions found for owner_id "alishakawaguchi" because the insights cache has no owner_id data yet.`)
}

func TestRunMemoryLoopRefresh_ScopeRepoFailsWhenDefaultBranchUnavailable(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	gitRunMemoryLoop(t, tmpDir, "branch", "-m", "trunk")
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".entire"), 0o755))
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()
	t.Cleanup(paths.ClearWorktreeRootCache)

	var buf bytes.Buffer
	err := runMemoryLoopRefresh(context.Background(), &buf, 10, "repo", "", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "default branch")
}

func TestRunMemoryLoopRefresh_ScopeMeFailsWhenGitHubUsernameUnavailable(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".entire"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, settings.EntireSettingsFile), []byte(`{
  "memory_loop": {
    "mode": "manual"
  }
}`), 0o644))
	installFakeGitHubCLI(t, "#!/bin/sh\nprintf 'github.com\\n' >&2\nexit 1\n")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	var buf bytes.Buffer
	err := runMemoryLoopRefresh(context.Background(), &buf, 10, "me", "", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "GitHub username")
}

func TestRunMemoryLoopRefresh_ScopeMeWithoutMatchingSessionsLeavesSnapshotUntouched(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".entire"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, settings.EntireSettingsFile), []byte(`{
  "memory_loop": {
    "mode": "manual"
  }
}`), 0o644))
	installFakeGitHubCLI(t, "#!/bin/sh\ncat <<'EOF'\ngithub.com\n  ✓ Logged in to github.com account alishakawaguchi (/tmp/hosts.yml)\n  - Active account: true\nEOF\n")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	original := time.Date(2026, time.April, 1, 10, 0, 0, 0, time.UTC)
	require.NoError(t, memoryloop.SaveState(context.Background(), &memoryloop.State{
		Store: &memoryloop.Store{
			Version:          1,
			GeneratedAt:      original,
			Scope:            "repo",
			Mode:             memoryloop.ModeManual,
			ActivationPolicy: memoryloop.ActivationPolicyReview,
			MaxInjected:      2,
			Records: []memoryloop.MemoryRecord{
				{ID: "keep", Title: "Keep me", Body: "body", Kind: memoryloop.KindRepoRule, Status: memoryloop.StatusActive},
			},
		},
	}))

	var buf bytes.Buffer
	require.NoError(t, runMemoryLoopRefresh(context.Background(), &buf, 10, "me", "", false))
	require.Contains(t, buf.String(), "No personal sessions found for owner_id \"alishakawaguchi\" because the insights cache has no owner_id data yet.")

	loaded, err := memoryloop.LoadState(context.Background())
	require.NoError(t, err)
	require.NotNil(t, loaded.Store)
	require.Equal(t, original, loaded.Store.GeneratedAt)
	require.Len(t, loaded.Store.Records, 1)
	require.Equal(t, "keep", loaded.Store.Records[0].ID)
}

func TestRunMemoryLoopRefresh_DebugPrintsBackfillSkipDetailsToTerminal(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, paths.EntireDir), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, settings.EntireSettingsFile), []byte(`{
  "strategy_options": {
    "summarize": {
      "enabled": true
    }
  }
}`), 0o644))
	installFakeGitHubCLI(t, "#!/bin/sh\ncat <<'EOF'\ngithub.com\n  ✓ Logged in to github.com account alishakawaguchi (/tmp/hosts.yml)\n  - Active account: true\nEOF\n")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	idb, err := insightsdb.Open(filepath.Join(tmpDir, paths.EntireDir, "insights.db"))
	require.NoError(t, err)
	require.NoError(t, idb.InsertSession(context.Background(), insightsdb.SessionRow{
		CheckpointID: "invalid-checkpoint-id",
		SessionID:    "session-debug-skip",
		SessionIndex: 0,
		OwnerID:      "alishakawaguchi",
		CreatedAt:    time.Date(2026, time.April, 2, 12, 0, 0, 0, time.UTC),
	}))
	require.NoError(t, idb.Close())

	var buf bytes.Buffer
	require.NoError(t, runMemoryLoopRefresh(context.Background(), &buf, 10, "me", "", true))
	require.Contains(t, buf.String(), "skipped 1")
	require.Contains(t, buf.String(), "debug: invalid checkpoint ID")
	require.Contains(t, buf.String(), "invalid-checkpoint-id")
}

func installFakeGitHubCLI(t *testing.T, script string) {
	t.Helper()

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "gh")
	require.NoError(t, os.WriteFile(binPath, []byte(script), 0o755))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func gitRunMemoryLoop(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func TestRenderMemoryLoopRefreshProgress(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	renderMemoryLoopRefreshProgress(&buf, "Refreshing cache...")
	renderMemoryLoopRefreshProgress(&buf, "Loading scoped sessions...")

	require.Equal(t, "Refreshing cache...\nLoading scoped sessions...\n", buf.String())
}

func TestRenderMemoryLoopRefreshSummary(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	renderMemoryLoopRefreshSummary(&buf, memoryLoopRefreshSummary{
		SessionCount:    12,
		ScopeLabel:      "me (me@example.com)",
		GeneratedCount:  5,
		ActivatedCount:  2,
		CandidateCount:  3,
		ActiveCount:     4,
		SuppressedCount: 1,
		ArchivedCount:   2,
		GeneratedTitles: []string{"Run lint before finishing", "Tighten the project skill"},
	})

	out := buf.String()
	require.Contains(t, out, "Memory Loop refreshed from 12 sessions")
	require.Contains(t, out, "Scope: me (me@example.com)")
	require.Contains(t, out, "This refresh: generated 5, activated 2, candidate 3")
	require.Contains(t, out, "Stored memories: active 4, candidate 3, suppressed 1, archived 2")
	require.Contains(t, out, "  - Run lint before finishing")
	require.Contains(t, out, "  - Tighten the project skill")
}

func TestRunMemoryLoopActivate_UpdatesPersonalCandidate(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	require.NoError(t, memoryloop.SaveState(context.Background(), &memoryloop.State{
		Store: &memoryloop.Store{
			Version:          1,
			Mode:             memoryloop.ModeManual,
			ActivationPolicy: memoryloop.ActivationPolicyReview,
			MaxInjected:      3,
			Records: []memoryloop.MemoryRecord{
				{
					ID:        "candidate-skill",
					Title:     "Tighten the project skill",
					Kind:      memoryloop.KindSkillPatch,
					ScopeKind: memoryloop.ScopeKindMe,
					Status:    memoryloop.StatusCandidate,
				},
			},
		},
	}))

	var buf bytes.Buffer
	require.NoError(t, runMemoryLoopActivate(context.Background(), &buf, "candidate-skill"))
	require.Contains(t, buf.String(), "Activated memory: candidate-skill")

	loaded, err := memoryloop.LoadState(context.Background())
	require.NoError(t, err)
	require.Equal(t, memoryloop.StatusActive, loaded.Store.Records[0].Status)
}

func TestRunMemoryLoopActivate_RejectsRepoCandidate(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	require.NoError(t, memoryloop.SaveState(context.Background(), &memoryloop.State{
		Store: &memoryloop.Store{
			Version:          1,
			Mode:             memoryloop.ModeManual,
			ActivationPolicy: memoryloop.ActivationPolicyReview,
			MaxInjected:      3,
			Records: []memoryloop.MemoryRecord{
				{
					ID:         "repo-candidate",
					Title:      "Keep generated repo memories pending",
					Kind:       memoryloop.KindRepoRule,
					ScopeKind:  memoryloop.ScopeKindRepo,
					ScopeValue: "main",
					Status:     memoryloop.StatusCandidate,
				},
			},
		},
	}))

	var buf bytes.Buffer
	err := runMemoryLoopActivate(context.Background(), &buf, "repo-candidate")
	require.Error(t, err)
	require.Contains(t, err.Error(), "promote")
}

func TestRunMemoryLoopPromote_UpdatesRepoCandidate(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	require.NoError(t, memoryloop.SaveState(context.Background(), &memoryloop.State{
		Store: &memoryloop.Store{
			Version:          1,
			Mode:             memoryloop.ModeManual,
			ActivationPolicy: memoryloop.ActivationPolicyReview,
			MaxInjected:      3,
			Records: []memoryloop.MemoryRecord{
				{
					ID:         "repo-candidate",
					Title:      "Keep generated repo memories pending",
					Kind:       memoryloop.KindRepoRule,
					ScopeKind:  memoryloop.ScopeKindRepo,
					ScopeValue: "main",
					Status:     memoryloop.StatusCandidate,
				},
			},
		},
	}))

	var buf bytes.Buffer
	require.NoError(t, runMemoryLoopPromote(context.Background(), &buf, "repo-candidate"))
	require.Contains(t, buf.String(), "Promoted memory: repo-candidate")

	loaded, err := memoryloop.LoadState(context.Background())
	require.NoError(t, err)
	require.Equal(t, memoryloop.StatusActive, loaded.Store.Records[0].Status)
}

func TestRunMemoryLoopSuppress_UpdatesRecord(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	require.NoError(t, memoryloop.SaveState(context.Background(), &memoryloop.State{
		Store: &memoryloop.Store{
			Version:          1,
			Mode:             memoryloop.ModeManual,
			ActivationPolicy: memoryloop.ActivationPolicyReview,
			MaxInjected:      3,
			Records: []memoryloop.MemoryRecord{
				{
					ID:        "active-lint",
					Title:     "Run lint before finishing",
					Kind:      memoryloop.KindRepoRule,
					ScopeKind: memoryloop.ScopeKindMe,
					Status:    memoryloop.StatusActive,
				},
			},
		},
	}))

	var buf bytes.Buffer
	require.NoError(t, runMemoryLoopSuppress(context.Background(), &buf, "active-lint"))
	require.Contains(t, buf.String(), "Suppressed memory: active-lint")

	loaded, err := memoryloop.LoadState(context.Background())
	require.NoError(t, err)
	require.Equal(t, memoryloop.StatusSuppressed, loaded.Store.Records[0].Status)
}

func TestRunMemoryLoopUnsuppress_UpdatesRecord(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	require.NoError(t, memoryloop.SaveState(context.Background(), &memoryloop.State{
		Store: &memoryloop.Store{
			Version:          1,
			Mode:             memoryloop.ModeManual,
			ActivationPolicy: memoryloop.ActivationPolicyReview,
			MaxInjected:      3,
			Records: []memoryloop.MemoryRecord{
				{
					ID:        "suppressed-lint",
					Title:     "Run lint before finishing",
					Kind:      memoryloop.KindRepoRule,
					ScopeKind: memoryloop.ScopeKindMe,
					Status:    memoryloop.StatusSuppressed,
				},
			},
		},
	}))

	var buf bytes.Buffer
	require.NoError(t, runMemoryLoopUnsuppress(context.Background(), &buf, "suppressed-lint"))
	require.Contains(t, buf.String(), "Unsuppressed memory: suppressed-lint")

	loaded, err := memoryloop.LoadState(context.Background())
	require.NoError(t, err)
	require.Equal(t, memoryloop.StatusCandidate, loaded.Store.Records[0].Status)
}

func TestRunMemoryLoopArchive_UpdatesRecord(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	require.NoError(t, memoryloop.SaveState(context.Background(), &memoryloop.State{
		Store: &memoryloop.Store{
			Version:          1,
			Mode:             memoryloop.ModeManual,
			ActivationPolicy: memoryloop.ActivationPolicyReview,
			MaxInjected:      3,
			Records: []memoryloop.MemoryRecord{
				{
					ID:        "active-lint",
					Title:     "Run lint before finishing",
					Kind:      memoryloop.KindRepoRule,
					ScopeKind: memoryloop.ScopeKindMe,
					Status:    memoryloop.StatusActive,
				},
			},
		},
	}))

	var buf bytes.Buffer
	require.NoError(t, runMemoryLoopArchive(context.Background(), &buf, "active-lint"))
	require.Contains(t, buf.String(), "Archived memory: active-lint")

	loaded, err := memoryloop.LoadState(context.Background())
	require.NoError(t, err)
	require.Equal(t, memoryloop.StatusArchived, loaded.Store.Records[0].Status)
}

func TestHandleMemoryLoopWizardAction_AdoptPersistsScopedRecord(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	installFakeGitHubCLI(t, "#!/bin/sh\ncat <<'EOF'\ngithub.com\n  ✓ Logged in to github.com account alishakawaguchi (/tmp/hosts.yml)\n  - Active account: true\nEOF\n")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	require.NoError(t, memoryloop.SaveState(context.Background(), &memoryloop.State{
		Store: &memoryloop.Store{
			Version:          1,
			Mode:             memoryloop.ModeManual,
			ActivationPolicy: memoryloop.ActivationPolicyReview,
			MaxInjected:      3,
			Records: []memoryloop.MemoryRecord{
				{
					ID:         "repo-candidate",
					Title:      "Keep generated repo memories pending",
					Body:       "Repo candidates stay candidate until a user adopts them.",
					Kind:       memoryloop.KindRepoRule,
					ScopeKind:  memoryloop.ScopeKindRepo,
					ScopeValue: "entireio/cli",
					Status:     memoryloop.StatusCandidate,
				},
			},
		},
	}))

	flash, err := handleMemoryLoopWizardAction(context.Background(), memorylooptui.WizardRequest{
		Intent:   memorylooptui.WizardIntentAdopt,
		RecordID: "repo-candidate",
		Scope:    memoryloop.ScopeKindMe,
	})
	require.NoError(t, err)
	require.Contains(t, flash, "Adopted memory")

	loaded, err := memoryloop.LoadState(context.Background())
	require.NoError(t, err)
	require.Len(t, loaded.Store.Records, 2)
	require.Equal(t, memoryloop.ScopeKindRepo, loaded.Store.Records[0].ScopeKind)
	require.Equal(t, memoryloop.ScopeKindMe, loaded.Store.Records[1].ScopeKind)
	require.Equal(t, "alishakawaguchi", loaded.Store.Records[1].ScopeValue)
	require.Equal(t, memoryloop.StatusActive, loaded.Store.Records[1].Status)
}

func TestHandleMemoryLoopWizardAction_ApplyPersistsArchiveAndWritesFiles(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	testutil.WriteFile(t, tmpDir, "AGENTS.md", "# Agents\n")
	testutil.WriteFile(t, tmpDir, "CLAUDE.md", "# Claude\n")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()
	t.Cleanup(paths.ClearWorktreeRootCache)

	require.NoError(t, memoryloop.SaveState(context.Background(), &memoryloop.State{
		Store: &memoryloop.Store{
			Version:          1,
			Mode:             memoryloop.ModeManual,
			ActivationPolicy: memoryloop.ActivationPolicyReview,
			MaxInjected:      3,
			Records: []memoryloop.MemoryRecord{
				{
					ID:        "lint",
					Title:     "Run lint before finishing",
					Body:      "Run golangci-lint before claiming completion.",
					Kind:      memoryloop.KindRepoRule,
					ScopeKind: memoryloop.ScopeKindMe,
					Status:    memoryloop.StatusActive,
				},
			},
		},
	}))

	flash, err := handleMemoryLoopWizardAction(context.Background(), memorylooptui.WizardRequest{
		Intent:   memorylooptui.WizardIntentApply,
		RecordID: "lint",
		Location: memoryloop.FileLocationProject,
	})
	require.NoError(t, err)
	require.Contains(t, flash, "Applied memory to 2 file(s)")

	loaded, err := memoryloop.LoadState(context.Background())
	require.NoError(t, err)
	require.Equal(t, memoryloop.StatusArchived, loaded.Store.Records[0].Status)
	require.Contains(t, testutil.ReadFile(t, tmpDir, "AGENTS.md"), "Run golangci-lint before claiming completion.")
	require.Contains(t, testutil.ReadFile(t, tmpDir, "CLAUDE.md"), "Run golangci-lint before claiming completion.")
}

func TestHandleMemoryLoopWizardAction_ApplyRejectsInvalidSelection(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()
	t.Cleanup(paths.ClearWorktreeRootCache)

	require.NoError(t, memoryloop.SaveState(context.Background(), &memoryloop.State{
		Store: &memoryloop.Store{
			Version:          1,
			Mode:             memoryloop.ModeManual,
			ActivationPolicy: memoryloop.ActivationPolicyReview,
			MaxInjected:      3,
			Records: []memoryloop.MemoryRecord{
				{
					ID:        "lint",
					Title:     "Run lint before finishing",
					Body:      "Run golangci-lint before claiming completion.",
					Kind:      memoryloop.KindRepoRule,
					ScopeKind: memoryloop.ScopeKindMe,
					Status:    memoryloop.StatusActive,
				},
			},
		},
	}))

	_, err := handleMemoryLoopWizardAction(context.Background(), memorylooptui.WizardRequest{
		Intent:   memorylooptui.WizardIntentApply,
		RecordID: "lint",
		Location: memoryloop.FileLocationProject,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no project instruction files found")

	loaded, loadErr := memoryloop.LoadState(context.Background())
	require.NoError(t, loadErr)
	require.Equal(t, memoryloop.StatusActive, loaded.Store.Records[0].Status)
}

func TestRunMemoryLoopAdd_AddsPersonalManualMemory(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, ".entire"), 0o755))
	installFakeGitHubCLI(t, "#!/bin/sh\ncat <<'EOF'\ngithub.com\n  ✓ Logged in to github.com account alishakawaguchi (/tmp/hosts.yml)\n  - Active account: true\nEOF\n")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	var buf bytes.Buffer
	require.NoError(t, runMemoryLoopAdd(context.Background(), &buf, memoryLoopAddOptions{
		Kind:  string(memoryloop.KindRepoRule),
		Title: "Run lint before finishing",
		Body:  "Run golangci-lint before claiming completion.",
		Scope: "me",
	}))
	require.Contains(t, buf.String(), "Added memory:")

	loaded, err := memoryloop.LoadState(context.Background())
	require.NoError(t, err)
	require.Len(t, loaded.Store.Records, 1)
	require.Equal(t, memoryloop.OriginManual, loaded.Store.Records[0].Origin)
	require.Equal(t, memoryloop.StatusActive, loaded.Store.Records[0].Status)
	require.Equal(t, memoryloop.ScopeKindMe, loaded.Store.Records[0].ScopeKind)
	require.Equal(t, "alishakawaguchi", loaded.Store.Records[0].ScopeValue)
	require.Equal(t, "test@example.com", loaded.Store.Records[0].OwnerEmail)
}

func TestRunMemoryLoopAdd_AddsRepoScopedManualMemory(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	var buf bytes.Buffer
	require.NoError(t, runMemoryLoopAdd(context.Background(), &buf, memoryLoopAddOptions{
		Kind:  string(memoryloop.KindWorkflowRule),
		Title: "Keep commit subjects concise",
		Body:  "Use short imperative commit subjects.",
		Scope: "repo",
	}))

	loaded, err := memoryloop.LoadState(context.Background())
	require.NoError(t, err)
	require.Len(t, loaded.Store.Records, 1)
	require.Equal(t, memoryloop.ScopeKindRepo, loaded.Store.Records[0].ScopeKind)
	require.Empty(t, loaded.Store.Records[0].ScopeValue)
	require.Equal(t, memoryloop.OriginManual, loaded.Store.Records[0].Origin)
}

func TestRunMemoryLoopPrune_ArchivesEligibleGeneratedMemories(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	now := time.Date(2026, time.March, 26, 12, 0, 0, 0, time.UTC)
	require.NoError(t, memoryloop.SaveState(context.Background(), &memoryloop.State{
		Store: &memoryloop.Store{
			Version:          1,
			Mode:             memoryloop.ModeManual,
			ActivationPolicy: memoryloop.ActivationPolicyReview,
			MaxInjected:      3,
			Records: []memoryloop.MemoryRecord{
				{
					ID:        "stale-candidate",
					Title:     "Pending lint rule",
					Kind:      memoryloop.KindRepoRule,
					Status:    memoryloop.StatusCandidate,
					Origin:    memoryloop.OriginGenerated,
					CreatedAt: now.Add(-31 * 24 * time.Hour),
					UpdatedAt: now.Add(-31 * 24 * time.Hour),
				},
				{
					ID:        "manual-memory",
					Title:     "Personal preference",
					Kind:      memoryloop.KindWorkflowRule,
					Status:    memoryloop.StatusActive,
					Origin:    memoryloop.OriginManual,
					CreatedAt: now.Add(-90 * 24 * time.Hour),
					UpdatedAt: now.Add(-90 * 24 * time.Hour),
				},
			},
		},
	}))

	var buf bytes.Buffer
	require.NoError(t, runMemoryLoopPrune(context.Background(), &buf, now))

	out := buf.String()
	require.Contains(t, out, "Pruned memories: archived 1")

	loaded, err := memoryloop.LoadState(context.Background())
	require.NoError(t, err)
	require.Equal(t, memoryloop.StatusArchived, loaded.Store.Records[0].Status)
	require.Equal(t, memoryloop.StatusActive, loaded.Store.Records[1].Status)
}

func TestScopeKindForRefresh_ReturnsBranchForBranchMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mode memoryLoopScopeMode
		want memoryloop.ScopeKind
	}{
		{memoryLoopScopeRepo, memoryloop.ScopeKindRepo},
		{memoryLoopScopeMe, memoryloop.ScopeKindMe},
		{memoryLoopScopeBranch, memoryloop.ScopeKindBranch},
	}
	for _, tt := range tests {
		got := scopeKindForRefresh(memoryLoopScope{Mode: tt.mode})
		require.Equal(t, tt.want, got, "mode=%s", tt.mode)
	}
}
