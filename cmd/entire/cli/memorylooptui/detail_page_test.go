package memorylooptui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

func TestRootUpdate_EnterOpensMemoryDetailPage(t *testing.T) {
	t.Parallel()

	root := newRootModelForStyleTest()

	next, _ := root.Update(tea.KeyMsg{Type: tea.KeyDown})
	root, ok := next.(rootModel)
	require.True(t, ok)

	next, cmd := root.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)

	root, ok = next.(rootModel)
	require.True(t, ok)
	next, _ = root.Update(cmd())
	updated, ok := next.(rootModel)
	require.True(t, ok)

	require.NotNil(t, updated.detailPage)
	require.Equal(t, "memory-2", updated.detailPage.record.ID)
	require.NotNil(t, updated.wizard)
}

func TestRootUpdate_EscapeClosesMemoryDetailPage(t *testing.T) {
	t.Parallel()

	root := newRootModelForStyleTest()
	root.detailPage = root.newDetailPage(sampleStateForStyleTest().Store.Records[0])
	root.wizard = &root.detailPage.wizard

	next, cmd := root.Update(tea.KeyMsg{Type: tea.KeyEsc})
	require.NotNil(t, cmd)

	root, ok := next.(rootModel)
	require.True(t, ok)
	next, _ = root.Update(cmd())
	updated, ok := next.(rootModel)
	require.True(t, ok)

	require.Nil(t, updated.detailPage)
	require.Equal(t, 0, updated.memoriesTab.table.Cursor())
}
