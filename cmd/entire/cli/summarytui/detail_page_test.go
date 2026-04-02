package summarytui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

func TestDetailView_RendersMetadataAndAllSections(t *testing.T) {
	t.Parallel()

	detail := newDetailModel(newStyles(), sampleRowsForTest()[0], false)
	detail.setSize(100, 12)

	require.Contains(t, detail.view(), "SESSION DETAIL")

	view := detail.renderContent()

	require.Contains(t, view, "alishakawaguchi")
	require.Contains(t, view, "Alisha Kawaguchi")
	require.Contains(t, view, "alisha@example.com")
	require.Contains(t, view, "sonnet")
	require.Contains(t, view, "3200")
	require.Contains(t, view, "7")
	require.Contains(t, view, "Failure Loops")
	require.Contains(t, view, "Skill Signals")
	require.Contains(t, view, "Repo Gotchas")
	require.Contains(t, view, "Workflow Gaps")
}

func TestDetailUpdate_ScrollsViewport(t *testing.T) {
	t.Parallel()

	detail := newDetailModel(newStyles(), manyFacetRowForTest(), false)
	detail.setSize(80, 8)

	require.Equal(t, 0, detail.viewport.YOffset)

	next, _ := detail.update(tea.KeyMsg{Type: tea.KeyDown})
	*detail = next

	require.Positive(t, detail.viewport.YOffset)
}
