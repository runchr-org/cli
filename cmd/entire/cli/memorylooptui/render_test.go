package memorylooptui

import (
	"context"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/memoryloop"
	"github.com/stretchr/testify/require"
)

func TestRootView_IncludesAppTitleAndSummaryCards(t *testing.T) {
	t.Parallel()

	out := newRootModelForStyleTest().View()

	require.Contains(t, out, "MEMORY LOOP")
	require.Contains(t, out, "ACTIVE")
	require.Contains(t, out, "CANDIDATE")
}

func TestMemoriesView_RendersDetailsPanelLabel(t *testing.T) {
	t.Parallel()

	out := newRootModelForStyleTest().View()

	require.Contains(t, out, "DETAILS")
}

func newRootModelForStyleTest() rootModel {
	styles := newStyles()
	m := rootModel{
		ctx:          context.Background(),
		styles:       styles,
		width:        100,
		height:       40,
		memoriesTab:  newMemoriesModel(styles),
		injectionTab: newInjectionModel(styles),
		historyTab:   newHistoryModel(styles),
		settingsTab:  settingsModel{styles: styles},
		state:        sampleStateForStyleTest(),
	}
	m.pushState()
	m.memoriesTab.setSize(m.width, m.contentHeight())
	m.injectionTab.setSize(m.width, m.contentHeight())
	m.historyTab.setSize(m.width, m.contentHeight())
	m.settingsTab.setSize(m.width, m.contentHeight())
	return m
}

func sampleStateForStyleTest() *memoryloop.State {
	now := time.Date(2026, 3, 26, 12, 0, 0, 0, time.UTC)

	return &memoryloop.State{
		Store: &memoryloop.Store{
			Version:          1,
			GeneratedAt:      now,
			SourceWindow:     20,
			Mode:             memoryloop.ModeAuto,
			ActivationPolicy: memoryloop.ActivationPolicyReview,
			InjectionEnabled: true,
			MaxInjected:      3,
			RefreshHistory: []memoryloop.RefreshHistory{
				{
					At:             now.Add(-24 * time.Hour),
					Scope:          "repo",
					ScopeValue:     "entireio/cli",
					SourceWindow:   20,
					GeneratedCount: 5,
					ActivatedCount: 2,
					CandidateCount: 3,
					InputTokens:    4200,
					OutputTokens:   800,
					TotalCostUSD:   0.0156,
				},
			},
			Records: []memoryloop.MemoryRecord{
				{
					ID:             "memory-1",
					Kind:           memoryloop.KindWorkflowRule,
					Title:          "Run tests before merging",
					Body:           "Use focused package tests before broader verification.",
					Why:            "Keeps risky changes scoped before wider verification.",
					Strength:       4,
					Status:         memoryloop.StatusActive,
					ScopeKind:      memoryloop.ScopeKindMe,
					Origin:         memoryloop.OriginManual,
					CreatedAt:      now.Add(-2 * time.Hour),
					UpdatedAt:      now.Add(-time.Hour),
					LastInjectedAt: now.Add(-30 * time.Minute),
					InjectCount:    3,
					MatchCount:     2,
					Outcome:        memoryloop.OutcomeReinforced,
				},
				{
					ID:          "memory-2",
					Kind:        memoryloop.KindRepoRule,
					Title:       "Keep repo memory reviewable",
					Body:        "Repo-scoped generated memories should stay candidate until promoted.",
					Strength:    3,
					Status:      memoryloop.StatusCandidate,
					ScopeKind:   memoryloop.ScopeKindRepo,
					ScopeValue:  "entireio/cli",
					Origin:      memoryloop.OriginGenerated,
					CreatedAt:   now.Add(-6 * time.Hour),
					UpdatedAt:   now.Add(-90 * time.Minute),
					InjectCount: 0,
					MatchCount:  1,
					Outcome:     memoryloop.OutcomeNeutral,
				},
			},
		},
		InjectionLogs: []memoryloop.InjectionLog{
			{
				SessionID:         "session-1",
				PromptPreview:     "please fix the failing test",
				InjectedMemoryIDs: []string{"memory-1"},
				InjectedAt:        now.Add(-20 * time.Minute),
				Reason:            "workflow guidance",
			},
		},
	}
}
