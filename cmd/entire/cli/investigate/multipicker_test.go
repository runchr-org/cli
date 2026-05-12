package investigate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPickInvestigateAgents_RequiresTwo(t *testing.T) {
	t.Parallel()
	_, err := PickInvestigateAgents(context.Background(), []AgentChoice{{Name: "claude-code"}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least 2")
}

func TestPickInvestigateAgents_ContextCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := PickInvestigateAgents(ctx, []AgentChoice{
		{Name: "claude-code"}, {Name: "codex"},
	})
	require.ErrorIs(t, err, ErrInvestigatePickerCancelled)
}
