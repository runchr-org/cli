package memorylooptui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
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

func TestMemoriesView_RendersFilterBarAsTwoSections(t *testing.T) {
	t.Parallel()

	root := newRootModelForStyleTest()

	out := root.memoriesTab.view()

	require.Contains(t, out, "Status")
	require.Contains(t, out, "Scope")
	require.Contains(t, out, "ACTIVE (1)")
	require.Contains(t, out, "Me (1)")
}

func TestMemoriesView_EmptyFilteredStateStillShowsFilterBar(t *testing.T) {
	t.Parallel()

	root := newRootModelForStyleTest()
	root.memoriesTab.filter = filterSuppressed
	root.memoriesTab.rebuildTable()

	out := root.memoriesTab.view()

	require.Contains(t, out, "Status")
	require.Contains(t, out, "Scope")
	require.Contains(t, out, "No SUPPRESSED / All memories")
}

func TestRootView_RendersMemoryDetailPageWhenActive(t *testing.T) {
	t.Parallel()

	m := newRootModelForStyleTest()
	detail := m.newDetailPage(
		sampleStateForStyleTest().Store.Records[0],
	)
	detail.wizard = newWizardModel(
		m.styles,
		sampleStateForStyleTest().Store.Records[0],
		func(_ memoryloop.MemoryRecord, _ memoryloop.FileLocation) ([]string, error) {
			return []string{"/repo/AGENTS.md"}, nil
		},
	)
	detail.wizard.stage = wizardStagePreview
	detail.wizard.request.Intent = WizardIntentApply
	detail.wizard.request.Location = memoryloop.FileLocationProject
	detail.wizard.previewTargets = []string{"/repo/AGENTS.md"}
	m.detailPage = detail

	out := m.View()

	require.Contains(t, out, "MEMORY DETAIL")
	require.Contains(t, out, "Preview")
	require.Contains(t, out, "/repo/AGENTS.md")
	require.Contains(t, out, "Run tests before merging")
}

func TestRootView_MemoryDetailPageFitsWithinWidth(t *testing.T) {
	t.Parallel()

	m := newRootModelForStyleTest()
	m.width = 80
	record := sampleStateForStyleTest().Store.Records[0]
	record.Title = "test-driven-development: Apply RED to all changed code paths"
	record.Body = "Apply the RED-GREEN cycle to every changed function including result structs, cache key logic, and helper functions."
	record.Why = "TDD was applied to ResolveSkillName but not to PopulateResult or cache key changes; the missed paths later produced bugs."
	record.Kind = memoryloop.KindSkillPatch
	record.SkillName = "test-driven-development"
	record.SkillPath = "superpowers/test-driven-development"

	detail := m.newDetailPage(record)
	m.detailPage = detail

	out := m.View()
	for _, line := range strings.Split(out, "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), m.width, "line exceeds width: %q", line)
	}
}

func TestRootView_MemoryDetailPageCardsStayIndented(t *testing.T) {
	t.Parallel()

	m := newRootModelForStyleTest()
	detail := m.newDetailPage(sampleStateForStyleTest().Store.Records[0])
	m.detailPage = detail

	out := m.View()
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.Contains(line, "╭") || strings.Contains(line, "│") || strings.Contains(line, "╰") {
			require.True(t, strings.HasPrefix(line, "  "), "card line lost indentation: %q", line)
		}
	}
}

func TestRootView_ExternalSkillPatchUsesAgentFileLabel(t *testing.T) {
	t.Parallel()

	m := newRootModelForStyleTest()
	record := sampleStateForStyleTest().Store.Records[0]
	record.Kind = memoryloop.KindSkillPatch
	record.SkillName = "test-driven-development"
	record.SkillPath = "superpowers/test-driven-development"

	detail := m.newDetailPage(record)
	m.detailPage = detail

	out := m.View()
	require.Contains(t, out, "Apply to agent files")
	require.NotContains(t, out, "Apply to skill files")
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
