package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
	"github.com/entireio/cli/cmd/entire/cli/memoryloop"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
	"github.com/stretchr/testify/require"
)

func TestFilterMemoryLoopRows_ScopeMe(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 26, 12, 0, 0, 0, time.UTC)
	rows := []insightsdb.SessionRow{
		{SessionID: "mine-newer", OwnerEmail: "me@example.com", Branch: "main", CreatedAt: now.Add(-time.Hour)},
		{SessionID: "coworker", OwnerEmail: "teammate@example.com", Branch: "main", CreatedAt: now.Add(-2 * time.Hour)},
		{SessionID: "mine-older", OwnerEmail: "me@example.com", Branch: "feature", CreatedAt: now.Add(-3 * time.Hour)},
	}

	filtered, err := filterMemoryLoopRows(rows, memoryLoopScope{
		Mode:       memoryLoopScopeMe,
		OwnerEmail: "me@example.com",
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
			ScopeValue:       "test@example.com",
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
				{At: now, Scope: "me", ScopeValue: "test@example.com", GeneratedCount: 2, ActivatedCount: 1, CandidateCount: 1},
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

func TestRunMemoryLoopStatus_IsConciseAndSupportsVerbosePromptPreview(t *testing.T) {
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
			Scope:            "me",
			ScopeValue:       "test@example.com",
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
					Strength:   4,
					ScopeKind:  memoryloop.ScopeKindMe,
					ScopeValue: "test@example.com",
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
	require.Contains(t, out, "scope=me(test@example.com)")
	require.Contains(t, out, "status=active")
}

func TestRunMemoryLoopStatus_NonVerbosePreviewOmitsSelectionRationale(t *testing.T) {
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
			Scope:            "me",
			ScopeValue:       "test@example.com",
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
					Strength:   4,
					ScopeKind:  memoryloop.ScopeKindMe,
					ScopeValue: "test@example.com",
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
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	now := time.Date(2026, time.March, 26, 12, 0, 0, 0, time.UTC)
	state := &memoryloop.State{
		Store: &memoryloop.Store{
			Version:          1,
			GeneratedAt:      now,
			Scope:            "me",
			ScopeValue:       "test@example.com",
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
					Strength:       4,
					Outcome:        memoryloop.OutcomeReinforced,
					LastInjectedAt: now.Add(-10 * time.Minute),
					ScopeKind:      memoryloop.ScopeKindMe,
					ScopeValue:     "test@example.com",
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
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()

	now := time.Date(2026, time.March, 26, 12, 20, 0, 0, time.UTC)
	state := &memoryloop.State{
		Store: &memoryloop.Store{
			Version:          1,
			GeneratedAt:      now,
			Scope:            "me",
			ScopeValue:       "test@example.com",
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
					ScopeValue:     "test@example.com",
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
					ScopeValue: "test@example.com",
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
					ScopeValue: "test@example.com",
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
	err := runMemoryLoopRefresh(context.Background(), &buf, 10, "repo", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid memory_loop.activation_policy")
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

func TestRunMemoryLoopAdd_AddsPersonalManualMemory(t *testing.T) {
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "init.txt", "init")
	testutil.GitAdd(t, tmpDir, "init.txt")
	testutil.GitCommit(t, tmpDir, "init")
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
