package memoryloop

import (
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/improve"
	"github.com/stretchr/testify/require"
)

func TestResolveGenerationThreshold_Presets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		preset string
		expect GenerationThresholdConfig
	}{
		{
			name: "balanced", preset: "balanced",
			expect: GenerationThresholdConfig{
				MinStrength: 3, MinConfidence: "medium", EvidenceSessions: 2,
				GenericFilter: true, SingletonPolicy: "review-rules",
			},
		},
		{
			name: "relaxed", preset: "relaxed",
			expect: GenerationThresholdConfig{
				MinStrength: 2, MinConfidence: "low", EvidenceSessions: 1,
				GenericFilter: false, SingletonPolicy: "all",
			},
		},
		{
			name: "strict", preset: "strict",
			expect: GenerationThresholdConfig{
				MinStrength: 4, MinConfidence: "high", EvidenceSessions: 3,
				GenericFilter: true, SingletonPolicy: "none",
			},
		},
		{
			name: "unknown falls back to balanced", preset: "potato",
			expect: GenerationThresholdConfig{
				MinStrength: 3, MinConfidence: "medium", EvidenceSessions: 2,
				GenericFilter: true, SingletonPolicy: "review-rules",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := ResolveGenerationThreshold(tc.preset, nil)
			require.Equal(t, tc.expect, result)
		})
	}
}

func TestResolveGenerationThreshold_Overrides(t *testing.T) {
	t.Parallel()
	strength := 1
	sessions := 5
	policy := "all"
	result := ResolveGenerationThreshold("balanced", &GenerationThresholdOverrides{
		MinStrength:      &strength,
		EvidenceSessions: &sessions,
		SingletonPolicy:  &policy,
	})
	require.Equal(t, 1, result.MinStrength)
	require.Equal(t, 5, result.EvidenceSessions)
	require.Equal(t, "all", result.SingletonPolicy)
	require.Equal(t, "medium", result.MinConfidence, "unset override should keep preset default")
	require.True(t, result.GenericFilter, "unset override should keep preset default")
}

func TestResolveGenerationThreshold_Clamping(t *testing.T) {
	t.Parallel()
	strength := 0
	sessions := 20
	badConfidence := "potato"
	badPolicy := "invalid"
	result := ResolveGenerationThreshold("balanced", &GenerationThresholdOverrides{
		MinStrength:      &strength,
		MinConfidence:    &badConfidence,
		EvidenceSessions: &sessions,
		SingletonPolicy:  &badPolicy,
	})
	require.Equal(t, 1, result.MinStrength, "should clamp to 1")
	require.Equal(t, 10, result.EvidenceSessions, "should clamp to 10")
	require.Equal(t, "medium", result.MinConfidence, "invalid string should use preset default")
	require.Equal(t, "review-rules", result.SingletonPolicy, "invalid string should use preset default")
}

func TestResolveGenerationThreshold_BalancedMatchesHardcoded(t *testing.T) {
	t.Parallel()
	cfg := ResolveGenerationThreshold("balanced", nil)
	require.Less(t, confidenceRank(confidenceLow), confidenceRank(cfg.MinConfidence),
		"balanced must filter low confidence via rank comparison")
	require.Equal(t, 3, cfg.MinStrength)
	require.Equal(t, 2, cfg.EvidenceSessions)
	require.True(t, cfg.GenericFilter)
	require.Equal(t, "review-rules", cfg.SingletonPolicy)
}

func TestPassesEvidenceGate_ConfigurableSessions(t *testing.T) {
	t.Parallel()
	analysis := improve.PatternAnalysis{
		RepeatedInstructions: []improve.RecurringSignal{{
			Value:            "always run lint",
			Count:            1,
			AffectedSessions: []string{"sess-1"},
		}},
	}
	record := MemoryRecord{
		SourceSessionIDs: []string{"sess-1"},
	}
	signal := &sourceSignal{Type: "repeated_instruction", Key: "always run lint"}
	validIDs := map[string]bool{"sess-1": true}

	require.True(t, passesEvidenceGate(record, signal, analysis, validIDs, 1, "all"))
	require.False(t, passesEvidenceGate(record, signal, analysis, validIDs, 2, "review-rules"))
}
