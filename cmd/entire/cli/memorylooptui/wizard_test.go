package memorylooptui

import (
	"context"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/entireio/cli/cmd/entire/cli/memoryloop"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWizard_OpenFromSelectedMemory(t *testing.T) {
	t.Parallel()

	root := newRootModelForStyleTest()

	next, _ := root.Update(tea.KeyMsg{Type: tea.KeyDown})
	root, ok := next.(rootModel)
	require.True(t, ok)

	next, cmd := root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	require.NotNil(t, cmd)

	msg := cmd()
	root, ok = next.(rootModel)
	require.True(t, ok)
	next, _ = root.Update(msg)
	updated, ok := next.(rootModel)
	require.True(t, ok)

	require.NotNil(t, updated.wizard)
	require.Equal(t, "memory-2", updated.wizard.record.ID)
}

func TestWizard_AdoptToScope_EmitsAdoptionRequest(t *testing.T) {
	t.Parallel()

	w := newWizardModel(
		newStyles(),
		sampleStateForStyleTest().Store.Records[1],
		func(_ memoryloop.MemoryRecord, _ memoryloop.FileLocation) ([]string, error) {
			return []string{"ignored"}, nil
		},
	)

	var cmd tea.Cmd
	w, _ = w.update(tea.KeyMsg{Type: tea.KeyEnter})

	w, _ = w.update(tea.KeyMsg{Type: tea.KeyDown})
	w, _ = w.update(tea.KeyMsg{Type: tea.KeyEnter})

	_, cmd = w.update(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)

	msg, ok := cmd().(wizardResultMsg)
	require.True(t, ok)
	require.True(t, msg.success)
	require.Equal(t, WizardIntentAdopt, msg.request.Intent)
	require.Equal(t, memoryloop.ScopeKindMe, msg.request.Scope)
	require.Equal(t, "memory-2", msg.request.RecordID)
}

func TestWizard_ApplyToFiles_ProjectShowsResolvedTargets(t *testing.T) {
	t.Parallel()

	w := newWizardModel(
		newStyles(),
		sampleStateForStyleTest().Store.Records[0],
		func(_ memoryloop.MemoryRecord, location memoryloop.FileLocation) ([]string, error) {
			require.Equal(t, memoryloop.FileLocationProject, location)
			return []string{"/repo/AGENTS.md", "/repo/CLAUDE.md"}, nil
		},
	)

	var cmd tea.Cmd
	w, _ = w.update(tea.KeyMsg{Type: tea.KeyDown})

	w, _ = w.update(tea.KeyMsg{Type: tea.KeyEnter})

	w, _ = w.update(tea.KeyMsg{Type: tea.KeyEnter})

	w, cmd = w.update(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)

	view := w.view()
	require.Contains(t, view, "Preview")
	require.Contains(t, view, "/repo/AGENTS.md")
	require.Contains(t, view, "/repo/CLAUDE.md")

	msg, ok := cmd().(wizardResultMsg)
	require.True(t, ok)
	require.True(t, msg.success)
	require.Equal(t, WizardIntentApply, msg.request.Intent)
	require.Equal(t, memoryloop.FileLocationProject, msg.request.Location)
	require.Equal(t, []string{"/repo/AGENTS.md", "/repo/CLAUDE.md"}, msg.request.Targets)
}

func TestWizard_PreviewStateBeforeConfirm(t *testing.T) {
	t.Parallel()

	w := newWizardModel(
		newStyles(),
		sampleStateForStyleTest().Store.Records[0],
		func(_ memoryloop.MemoryRecord, _ memoryloop.FileLocation) ([]string, error) {
			return []string{"/repo/AGENTS.md"}, nil
		},
	)

	w, _ = w.update(tea.KeyMsg{Type: tea.KeyDown})
	w, _ = w.update(tea.KeyMsg{Type: tea.KeyEnter})
	w, _ = w.update(tea.KeyMsg{Type: tea.KeyEnter})

	require.Contains(t, w.view(), "Preview")
	require.Contains(t, w.view(), "/repo/AGENTS.md")
	require.Equal(t, wizardStagePreview, w.stage)
}

func TestWizard_SuccessAndFailureFlashesAfterConfirm(t *testing.T) {
	t.Parallel()

	root := newRootModelForStyleTest()
	wizard := newWizardModel(
		root.styles,
		sampleStateForStyleTest().Store.Records[0],
		func(_ memoryloop.MemoryRecord, _ memoryloop.FileLocation) ([]string, error) {
			return []string{"/repo/AGENTS.md"}, nil
		},
	)
	root.wizard = &wizard

	next, _ := root.Update(wizardResultMsg{
		success: true,
		flash:   "Applied memory to files",
		request: WizardRequest{
			Intent:   WizardIntentApply,
			RecordID: "memory-1",
		},
	})
	updated, ok := next.(rootModel)
	require.True(t, ok)
	require.Nil(t, updated.wizard)
	require.Equal(t, "Applied memory to files", updated.flashText)

	rootFail := newRootModelForStyleTest()
	wizardFail := newWizardModel(
		rootFail.styles,
		sampleStateForStyleTest().Store.Records[0],
		func(_ memoryloop.MemoryRecord, _ memoryloop.FileLocation) ([]string, error) {
			return []string{}, nil
		},
	)
	rootFail.wizard = &wizardFail

	next, _ = rootFail.Update(wizardResultMsg{
		success: false,
		flash:   "No targets resolved",
		request: WizardRequest{
			Intent:   WizardIntentApply,
			RecordID: "memory-1",
		},
	})
	updated, ok = next.(rootModel)
	require.True(t, ok)
	require.NotNil(t, updated.wizard)
	require.Equal(t, "No targets resolved", updated.flashText)
}

func TestRootResolveWizardTargets_SkillPatchUsesRecordMetadata(t *testing.T) {
	repoRoot := t.TempDir()
	testutil.InitRepo(t, repoRoot)
	testutil.WriteFile(t, repoRoot, ".claude/skills/review/SKILL.md", "# Review\n")
	t.Chdir(repoRoot)
	paths.ClearWorktreeRootCache()
	t.Cleanup(paths.ClearWorktreeRootCache)

	root := newRootModelForStyleTest()
	root.ctx = context.Background()

	targets, err := root.resolveWizardTargets(memoryloop.MemoryRecord{
		ID:        "review-skill",
		Kind:      memoryloop.KindSkillPatch,
		Title:     "Tighten the review skill",
		Body:      "Add the missing retry step to the review skill instructions.",
		SkillName: "review",
		SkillPath: ".claude/skills/review/SKILL.md",
	}, memoryloop.FileLocationProject)
	require.NoError(t, err)
	resolvedRepoRoot, err := filepath.EvalSymlinks(repoRoot)
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(resolvedRepoRoot, ".claude", "skills", "review", "SKILL.md")}, targets)
}

func TestRootResolveWizardTargets_ExternalSkillPatchFallsBackToInstructionFiles(t *testing.T) {
	repoRoot := t.TempDir()
	testutil.InitRepo(t, repoRoot)
	testutil.WriteFile(t, repoRoot, "AGENTS.md", "# Agents\n")
	testutil.WriteFile(t, repoRoot, "CLAUDE.md", "# Claude\n")
	t.Chdir(repoRoot)
	paths.ClearWorktreeRootCache()
	t.Cleanup(paths.ClearWorktreeRootCache)

	root := newRootModelForStyleTest()
	root.ctx = context.Background()
	resolvedRepoRoot, err := filepath.EvalSymlinks(repoRoot)
	require.NoError(t, err)

	targets, err := root.resolveWizardTargets(memoryloop.MemoryRecord{
		ID:        "tdd-skill",
		Kind:      memoryloop.KindSkillPatch,
		Title:     "Apply RED to all changed code paths",
		Body:      "Apply the RED-GREEN cycle to every changed function.",
		SkillName: "test-driven-development",
		SkillPath: "superpowers/test-driven-development",
	}, memoryloop.FileLocationProject)
	require.NoError(t, err)
	require.Equal(t, []string{
		filepath.Join(resolvedRepoRoot, "AGENTS.md"),
		filepath.Join(resolvedRepoRoot, "CLAUDE.md"),
	}, targets)
}

func TestRootUpdate_WizardSuppressPersistsLifecycleChange(t *testing.T) {
	repoRoot := t.TempDir()
	testutil.InitRepo(t, repoRoot)
	t.Chdir(repoRoot)
	paths.ClearWorktreeRootCache()
	t.Cleanup(paths.ClearWorktreeRootCache)

	root := newRootModelForStyleTest()
	root.ctx = context.Background()
	wizard := newWizardModel(
		root.styles,
		sampleStateForStyleTest().Store.Records[0],
		func(_ memoryloop.MemoryRecord, _ memoryloop.FileLocation) ([]string, error) {
			return nil, nil
		},
	)
	root.wizard = &wizard

	next, _ := root.Update(wizardResultMsg{
		success: true,
		flash:   "Suppressed Run tests before merging",
		request: WizardRequest{
			Intent:   WizardIntentSuppress,
			RecordID: "memory-1",
		},
	})
	updated, ok := next.(rootModel)
	require.True(t, ok)
	require.Nil(t, updated.wizard)
	require.Equal(t, memoryloop.StatusSuppressed, updated.state.Store.Records[0].Status)

	state, err := memoryloop.LoadState(context.Background())
	require.NoError(t, err)
	require.Equal(t, memoryloop.StatusSuppressed, state.Store.Records[0].Status)
}

func TestRootUpdate_WizardApplyUsesInjectedHandlerAndReloadsState(t *testing.T) {
	repoRoot := t.TempDir()
	testutil.InitRepo(t, repoRoot)
	testutil.WriteFile(t, repoRoot, "init.txt", "init")
	t.Chdir(repoRoot)
	paths.ClearWorktreeRootCache()
	t.Cleanup(paths.ClearWorktreeRootCache)

	initialState := &memoryloop.State{
		Store: &memoryloop.Store{
			Version: 1,
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
	}
	require.NoError(t, memoryloop.SaveState(context.Background(), initialState))

	root := newRootModelForStyleTest()
	root.ctx = context.Background()
	root.state = initialState
	root.pushState()
	root.wizardActionHandler = func(ctx context.Context, _ WizardRequest) (string, error) {
		loaded, err := memoryloop.LoadState(ctx)
		if err != nil {
			return "", err
		}
		loaded.Store.Records[0].Status = memoryloop.StatusArchived
		if err := memoryloop.SaveState(ctx, loaded); err != nil {
			return "", err
		}
		return "Applied memory to files", nil
	}
	wizard := newWizardModel(
		root.styles,
		initialState.Store.Records[0],
		func(_ memoryloop.MemoryRecord, _ memoryloop.FileLocation) ([]string, error) {
			return []string{filepath.Join(repoRoot, "AGENTS.md")}, nil
		},
	)
	root.wizard = &wizard

	next, _ := root.Update(wizardResultMsg{
		success: true,
		flash:   "Prepared apply request for 1 target(s)",
		request: WizardRequest{
			Intent:   WizardIntentApply,
			RecordID: "lint",
			Location: memoryloop.FileLocationProject,
		},
	})
	updated, ok := next.(rootModel)
	require.True(t, ok)
	require.Nil(t, updated.wizard)
	require.Equal(t, "Applied memory to files", updated.flashText)
	require.Equal(t, memoryloop.StatusArchived, updated.state.Store.Records[0].Status)
}

func TestRootUpdate_WizardHandlerErrorKeepsWizardOpenAndShowsErrorFlash(t *testing.T) {
	repoRoot := t.TempDir()
	testutil.InitRepo(t, repoRoot)
	testutil.WriteFile(t, repoRoot, "init.txt", "init")
	t.Chdir(repoRoot)
	paths.ClearWorktreeRootCache()
	t.Cleanup(paths.ClearWorktreeRootCache)

	initialState := &memoryloop.State{
		Store: &memoryloop.Store{
			Version: 1,
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
	}
	require.NoError(t, memoryloop.SaveState(context.Background(), initialState))

	root := newRootModelForStyleTest()
	root.ctx = context.Background()
	root.state = initialState
	root.pushState()
	root.wizardActionHandler = func(_ context.Context, _ WizardRequest) (string, error) {
		return "", assert.AnError
	}
	wizard := newWizardModel(
		root.styles,
		initialState.Store.Records[0],
		func(_ memoryloop.MemoryRecord, _ memoryloop.FileLocation) ([]string, error) {
			return []string{filepath.Join(repoRoot, "AGENTS.md")}, nil
		},
	)
	root.wizard = &wizard

	next, _ := root.Update(wizardResultMsg{
		success: true,
		flash:   "Prepared apply request for 1 target(s)",
		request: WizardRequest{
			Intent:   WizardIntentApply,
			RecordID: "lint",
			Location: memoryloop.FileLocationProject,
		},
	})
	updated, ok := next.(rootModel)
	require.True(t, ok)
	require.NotNil(t, updated.wizard)
	require.Equal(t, assert.AnError.Error(), updated.flashText)
	require.Equal(t, memoryloop.StatusActive, updated.state.Store.Records[0].Status)
}
