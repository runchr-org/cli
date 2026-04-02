package summarytui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

func TestDetailView_RendersMetadataAndAllSections(t *testing.T) {
	t.Parallel()

	detail := newDetailModel(newStyles(), sampleRowsForTest()[0])
	detail.setSize(100, 12)

	require.Contains(t, detail.view(), "SESSION DETAIL")

	view := detail.renderContent()

	require.Contains(t, view, "Author: alishakawaguchi")
	require.Contains(t, view, "Author Name: Alisha Kawaguchi")
	require.Contains(t, view, "Author Email: alisha@example.com")
	require.Contains(t, view, "Model: sonnet")
	require.Contains(t, view, "Tokens: 3200")
	require.Contains(t, view, "Turns: 7")
	require.Contains(t, view, "Failure Loops")
	require.Contains(t, view, "Skill Signals")
	require.Contains(t, view, "Repo Gotchas")
	require.Contains(t, view, "Workflow Gaps")
}

func TestDetailUpdate_ScrollsViewport(t *testing.T) {
	t.Parallel()

	detail := newDetailModel(newStyles(), manyFacetRowForTest())
	detail.setSize(80, 8)

	require.Equal(t, 0, detail.viewport.YOffset)

	next, _ := detail.update(tea.KeyMsg{Type: tea.KeyDown})
	*detail = next

	require.Greater(t, detail.viewport.YOffset, 0)
}
