package settings

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

const (
	debugLevel       = "debug"
	providerClaudeCC = "claude-code"
)

func boolPtr(b bool) *bool { return &b }

type synthCase struct {
	name   string
	legacy *EntireSettings
	check  func(t *testing.T, got *Settings)
}

func runSynthCases(t *testing.T, cases []synthCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := synthesizeFromLegacy(tc.legacy)
			if got == nil {
				t.Fatal("synthesizeFromLegacy returned nil")
			}
			if got.Schema != CurrentSchemaVersion {
				t.Fatalf("Schema = %d, want %d", got.Schema, CurrentSchemaVersion)
			}
			tc.check(t, got)
		})
	}
}

// TestSynthesizeFromLegacy_BackendSelection covers how the Primary backend
// type and Mirrors are derived from legacy strategy_options.
func TestSynthesizeFromLegacy_BackendSelection(t *testing.T) {
	t.Parallel()
	runSynthCases(t, []synthCase{
		{
			name:   "nil legacy returns defaults",
			legacy: nil,
			check: func(t *testing.T, got *Settings) {
				if !got.Enabled {
					t.Fatal("Enabled = false, want true")
				}
			},
		},
		{
			name:   "empty legacy → primary v1, no mirrors",
			legacy: &EntireSettings{Enabled: true},
			check: func(t *testing.T, got *Settings) {
				if got.Checkpoints.Primary.Type != BackendTypeV1 {
					t.Fatalf("Primary.Type = %q, want %q", got.Checkpoints.Primary.Type, BackendTypeV1)
				}
				if len(got.Checkpoints.Mirrors) != 0 {
					t.Fatalf("Mirrors = %v, want empty", got.Checkpoints.Mirrors)
				}
			},
		},
		{
			name: "checkpoints_v2 alone → primary v2",
			legacy: &EntireSettings{
				Enabled:         true,
				StrategyOptions: map[string]any{"checkpoints_v2": true},
			},
			check: func(t *testing.T, got *Settings) {
				if got.Checkpoints.Primary.Type != BackendTypeV2 {
					t.Fatalf("Primary.Type = %q, want %q", got.Checkpoints.Primary.Type, BackendTypeV2)
				}
			},
		},
		{
			name: "checkpoints_version=2 → primary v2",
			legacy: &EntireSettings{
				Enabled:         true,
				StrategyOptions: map[string]any{"checkpoints_version": float64(2)},
			},
			check: func(t *testing.T, got *Settings) {
				if got.Checkpoints.Primary.Type != BackendTypeV2 {
					t.Fatalf("Primary.Type = %q, want %q", got.Checkpoints.Primary.Type, BackendTypeV2)
				}
			},
		},
		{
			name: "checkpoints_version=1 → primary v1",
			legacy: &EntireSettings{
				Enabled:         true,
				StrategyOptions: map[string]any{"checkpoints_version": float64(1)},
			},
			check: func(t *testing.T, got *Settings) {
				if got.Checkpoints.Primary.Type != BackendTypeV1 {
					t.Fatalf("Primary.Type = %q, want %q", got.Checkpoints.Primary.Type, BackendTypeV1)
				}
			},
		},
		{
			name: "gmeta alone → primary v1, gmeta mirror",
			legacy: &EntireSettings{
				Enabled:         true,
				StrategyOptions: map[string]any{"gmeta": true},
			},
			check: func(t *testing.T, got *Settings) {
				if got.Checkpoints.Primary.Type != BackendTypeV1 {
					t.Fatalf("Primary.Type = %q, want v1", got.Checkpoints.Primary.Type)
				}
				if len(got.Checkpoints.Mirrors) != 1 || got.Checkpoints.Mirrors[0].Type != BackendTypeGmeta {
					t.Fatalf("Mirrors = %v, want [{gmeta}]", got.Checkpoints.Mirrors)
				}
			},
		},
		{
			name: "checkpoints_v2 + gmeta → primary v2, gmeta mirror",
			legacy: &EntireSettings{
				Enabled: true,
				StrategyOptions: map[string]any{
					"checkpoints_v2": true,
					"gmeta":          true,
				},
			},
			check: func(t *testing.T, got *Settings) {
				if got.Checkpoints.Primary.Type != BackendTypeV2 {
					t.Fatalf("Primary.Type = %q, want v2", got.Checkpoints.Primary.Type)
				}
				if len(got.Checkpoints.Mirrors) != 1 || got.Checkpoints.Mirrors[0].Type != BackendTypeGmeta {
					t.Fatalf("Mirrors = %v, want [{gmeta}]", got.Checkpoints.Mirrors)
				}
			},
		},
		{
			name: "push_v2_refs is silently dropped (no v2 destination)",
			legacy: &EntireSettings{
				Enabled: true,
				StrategyOptions: map[string]any{
					"checkpoints_v2": true,
					"push_v2_refs":   true,
				},
			},
			check: func(t *testing.T, got *Settings) {
				if got.Checkpoints.Primary.Type != BackendTypeV2 {
					t.Fatalf("Primary.Type = %q, want v2", got.Checkpoints.Primary.Type)
				}
			},
		},
		{
			name: "deprecated 'strategy' field is silently dropped",
			legacy: &EntireSettings{
				Enabled:  true,
				Strategy: "auto-commit",
			},
			check: func(t *testing.T, got *Settings) {
				if got.Checkpoints.Primary.Type != BackendTypeV1 {
					t.Fatalf("Primary.Type = %q, want v1", got.Checkpoints.Primary.Type)
				}
			},
		},
	})
}

// TestSynthesizeFromLegacy_CheckpointFields covers Checkpoints sub-fields
// other than Primary/Mirrors: remote, retention, signing, filtered fetches,
// push_sessions tri-state.
func TestSynthesizeFromLegacy_CheckpointFields(t *testing.T) {
	t.Parallel()
	runSynthCases(t, []synthCase{
		{
			name: "checkpoint_remote → checkpoints.remote",
			legacy: &EntireSettings{
				Enabled: true,
				StrategyOptions: map[string]any{
					"checkpoint_remote": map[string]any{
						"provider": "github",
						"repo":     "org/checkpoints",
					},
				},
			},
			check: func(t *testing.T, got *Settings) {
				if got.Checkpoints.Remote == nil {
					t.Fatal("Remote = nil, want populated")
				}
				if got.Checkpoints.Remote.Provider != "github" || got.Checkpoints.Remote.Repo != "org/checkpoints" {
					t.Fatalf("Remote = %+v, want github/org/checkpoints", got.Checkpoints.Remote)
				}
			},
		},
		{
			name: "filtered_fetches → checkpoints.filtered_fetches",
			legacy: &EntireSettings{
				Enabled:         true,
				StrategyOptions: map[string]any{"filtered_fetches": true},
			},
			check: func(t *testing.T, got *Settings) {
				if !got.Checkpoints.FilteredFetches {
					t.Fatal("FilteredFetches = false, want true")
				}
			},
		},
		{
			name: "push_sessions=false → explicit false pointer",
			legacy: &EntireSettings{
				Enabled:         true,
				StrategyOptions: map[string]any{"push_sessions": false},
			},
			check: func(t *testing.T, got *Settings) {
				if got.Checkpoints.PushSessions == nil || *got.Checkpoints.PushSessions {
					t.Fatalf("PushSessions = %v, want explicit false", got.Checkpoints.PushSessions)
				}
			},
		},
		{
			name: "push_sessions=true → explicit true pointer",
			legacy: &EntireSettings{
				Enabled:         true,
				StrategyOptions: map[string]any{"push_sessions": true},
			},
			check: func(t *testing.T, got *Settings) {
				if got.Checkpoints.PushSessions == nil || !*got.Checkpoints.PushSessions {
					t.Fatalf("PushSessions = %v, want explicit true", got.Checkpoints.PushSessions)
				}
			},
		},
		{
			name:   "push_sessions absent → nil pointer",
			legacy: &EntireSettings{Enabled: true},
			check: func(t *testing.T, got *Settings) {
				if got.Checkpoints.PushSessions != nil {
					t.Fatalf("PushSessions = %v, want nil", got.Checkpoints.PushSessions)
				}
			},
		},
		{
			name: "full_transcript_generation_retention_days set → preserved",
			legacy: &EntireSettings{
				Enabled:         true,
				StrategyOptions: map[string]any{"full_transcript_generation_retention_days": float64(90)},
			},
			check: func(t *testing.T, got *Settings) {
				if got.Checkpoints.FullTranscriptRetentionDays != 90 {
					t.Fatalf("FullTranscriptRetentionDays = %d, want 90", got.Checkpoints.FullTranscriptRetentionDays)
				}
			},
		},
		{
			name:   "full_transcript_generation_retention_days absent → 0",
			legacy: &EntireSettings{Enabled: true},
			check: func(t *testing.T, got *Settings) {
				if got.Checkpoints.FullTranscriptRetentionDays != 0 {
					t.Fatalf("FullTranscriptRetentionDays = %d, want 0", got.Checkpoints.FullTranscriptRetentionDays)
				}
			},
		},
		{
			name: "sign_checkpoint_commits=false → checkpoints.sign_commits=false",
			legacy: &EntireSettings{
				Enabled:               true,
				SignCheckpointCommits: boolPtr(false),
			},
			check: func(t *testing.T, got *Settings) {
				if got.Checkpoints.SignCommits == nil || *got.Checkpoints.SignCommits {
					t.Fatalf("SignCommits = %v, want explicit false", got.Checkpoints.SignCommits)
				}
			},
		},
		{
			name:   "sign_checkpoint_commits unset → checkpoints.sign_commits nil",
			legacy: &EntireSettings{Enabled: true},
			check: func(t *testing.T, got *Settings) {
				if got.Checkpoints.SignCommits != nil {
					t.Fatalf("SignCommits = %v, want nil", got.Checkpoints.SignCommits)
				}
			},
		},
	})
}

// TestSynthesizeFromLegacy_TopLevelFields covers logging, hooks, features,
// redaction, and telemetry mappings.
func TestSynthesizeFromLegacy_TopLevelFields(t *testing.T) {
	t.Parallel()
	runSynthCases(t, []synthCase{
		{
			name: "log_level → logging.level",
			legacy: &EntireSettings{
				Enabled:  true,
				LogLevel: debugLevel,
			},
			check: func(t *testing.T, got *Settings) {
				if got.Logging.Level != debugLevel {
					t.Fatalf("Logging.Level = %q, want %q", got.Logging.Level, debugLevel)
				}
			},
		},
		{
			name: "summarize → features.summarize",
			legacy: &EntireSettings{
				Enabled: true,
				StrategyOptions: map[string]any{
					"summarize": map[string]any{"enabled": true},
				},
			},
			check: func(t *testing.T, got *Settings) {
				if !got.Features.Summarize {
					t.Fatal("Features.Summarize = false, want true")
				}
			},
		},
		{
			name: "external_agents → features.external_agents",
			legacy: &EntireSettings{
				Enabled:        true,
				ExternalAgents: true,
			},
			check: func(t *testing.T, got *Settings) {
				if !got.Features.ExternalAgents {
					t.Fatal("Features.ExternalAgents = false, want true")
				}
			},
		},
		{
			name: "vercel → features.vercel",
			legacy: &EntireSettings{
				Enabled: true,
				Vercel:  true,
			},
			check: func(t *testing.T, got *Settings) {
				if !got.Features.Vercel {
					t.Fatal("Features.Vercel = false, want true")
				}
			},
		},
		{
			name: "commit_linking → hooks.commit_linking",
			legacy: &EntireSettings{
				Enabled:       true,
				CommitLinking: CommitLinkingAlways,
			},
			check: func(t *testing.T, got *Settings) {
				if got.Hooks.CommitLinking != CommitLinkingAlways {
					t.Fatalf("Hooks.CommitLinking = %q, want %q", got.Hooks.CommitLinking, CommitLinkingAlways)
				}
			},
		},
		{
			name: "absolute_git_hook_path → hooks.absolute_git_hook_path",
			legacy: &EntireSettings{
				Enabled:             true,
				AbsoluteGitHookPath: true,
			},
			check: func(t *testing.T, got *Settings) {
				if !got.Hooks.AbsoluteGitHookPath {
					t.Fatal("Hooks.AbsoluteGitHookPath = false, want true")
				}
			},
		},
		{
			name: "redaction passes through",
			legacy: &EntireSettings{
				Enabled: true,
				Redaction: &RedactionSettings{
					PII: &PIISettings{Enabled: true, Email: boolPtr(true)},
				},
			},
			check: func(t *testing.T, got *Settings) {
				if got.Redaction == nil || got.Redaction.PII == nil || !got.Redaction.PII.Enabled {
					t.Fatalf("Redaction = %+v, want PII enabled", got.Redaction)
				}
			},
		},
		{
			name: "telemetry pointer passes through",
			legacy: &EntireSettings{
				Enabled:   true,
				Telemetry: boolPtr(true),
			},
			check: func(t *testing.T, got *Settings) {
				if got.Telemetry == nil || !*got.Telemetry {
					t.Fatalf("Telemetry = %v, want explicit true", got.Telemetry)
				}
			},
		},
	})
}

// TestSynthesizeFromLegacy_SummaryGeneration covers the summary_generation
// group consolidation (provider, model, timeout).
func TestSynthesizeFromLegacy_SummaryGeneration(t *testing.T) {
	t.Parallel()
	runSynthCases(t, []synthCase{
		{
			name: "summary_timeout_seconds → summary_generation.timeout_seconds",
			legacy: &EntireSettings{
				Enabled:               true,
				SummaryTimeoutSeconds: 30,
			},
			check: func(t *testing.T, got *Settings) {
				if got.SummaryGeneration == nil {
					t.Fatal("SummaryGeneration = nil, want allocated")
				}
				if got.SummaryGeneration.TimeoutSeconds != 30 {
					t.Fatalf("TimeoutSeconds = %d, want 30", got.SummaryGeneration.TimeoutSeconds)
				}
			},
		},
		{
			name: "provider/model preserved",
			legacy: &EntireSettings{
				Enabled: true,
				SummaryGeneration: &SummaryGenerationSettings{
					Provider: providerClaudeCC,
					Model:    "sonnet",
				},
			},
			check: func(t *testing.T, got *Settings) {
				if got.SummaryGeneration == nil {
					t.Fatal("SummaryGeneration = nil")
				}
				if got.SummaryGeneration.Provider != providerClaudeCC || got.SummaryGeneration.Model != "sonnet" {
					t.Fatalf("SummaryGeneration = %+v, want claude-code/sonnet", got.SummaryGeneration)
				}
			},
		},
		{
			name:   "no summary fields → summary_generation nil",
			legacy: &EntireSettings{Enabled: true},
			check: func(t *testing.T, got *Settings) {
				if got.SummaryGeneration != nil {
					t.Fatalf("SummaryGeneration = %+v, want nil", got.SummaryGeneration)
				}
			},
		},
	})
}

// TestLoadV2_FileScenarios covers how LoadV2 reads from disk.
// These tests use t.Chdir, so they cannot run in parallel.
func TestLoadV2_NoFiles(t *testing.T) {
	setupSettingsDir(t, "", "")
	got, err := LoadV2(context.Background())
	if err != nil {
		t.Fatalf("LoadV2: %v", err)
	}
	if got.Schema != CurrentSchemaVersion || !got.Enabled {
		t.Fatalf("defaults wrong: %+v", got)
	}
	if got.Checkpoints.Primary.Type != "" {
		t.Fatalf("expected empty primary when no files present, got %q", got.Checkpoints.Primary.Type)
	}
}

func TestLoadV2_LegacyMainOnly(t *testing.T) {
	setupSettingsDir(t, `{"enabled": true, "strategy_options": {"checkpoints_v2": true}}`, "")
	got, err := LoadV2(context.Background())
	if err != nil {
		t.Fatalf("LoadV2: %v", err)
	}
	if got.Checkpoints.Primary.Type != BackendTypeV2 {
		t.Fatalf("Primary.Type = %q, want v2", got.Checkpoints.Primary.Type)
	}
}

func TestLoadV2_LegacyMainAndLocal(t *testing.T) {
	setupSettingsDir(t,
		`{"enabled": true}`,
		`{"log_level": "`+debugLevel+`"}`,
	)
	got, err := LoadV2(context.Background())
	if err != nil {
		t.Fatalf("LoadV2: %v", err)
	}
	if got.Logging.Level != debugLevel {
		t.Fatalf("Logging.Level = %q, want %q", got.Logging.Level, debugLevel)
	}
}

func TestLoadV2_V2MainOnly(t *testing.T) {
	setupSettingsDir(t, `{
		"schema": 2,
		"enabled": true,
		"checkpoints": {"primary": {"type": "v2"}, "mirrors": [{"type": "gmeta"}]}
	}`, "")
	got, err := LoadV2(context.Background())
	if err != nil {
		t.Fatalf("LoadV2: %v", err)
	}
	if got.Checkpoints.Primary.Type != BackendTypeV2 {
		t.Fatalf("Primary.Type = %q, want v2", got.Checkpoints.Primary.Type)
	}
	if len(got.Checkpoints.Mirrors) != 1 || got.Checkpoints.Mirrors[0].Type != BackendTypeGmeta {
		t.Fatalf("Mirrors = %v, want [gmeta]", got.Checkpoints.Mirrors)
	}
}

func TestLoadV2_V2MainAndLocalOverride(t *testing.T) {
	setupSettingsDir(t,
		`{"schema": 2, "enabled": true, "logging": {"level": "info"}}`,
		`{"schema": 2, "logging": {"level": "`+debugLevel+`"}}`,
	)
	got, err := LoadV2(context.Background())
	if err != nil {
		t.Fatalf("LoadV2: %v", err)
	}
	if got.Logging.Level != debugLevel {
		t.Fatalf("Logging.Level = %q, want %q (overridden)", got.Logging.Level, debugLevel)
	}
	if !got.Enabled {
		t.Fatal("Enabled should be preserved across override")
	}
}

func TestLoadV2_MixedShapesRejected_V2MainLegacyLocal(t *testing.T) {
	setupSettingsDir(t,
		`{"schema": 2, "enabled": true}`,
		`{"log_level": "`+debugLevel+`"}`,
	)
	_, err := LoadV2(context.Background())
	if err == nil {
		t.Fatal("expected error for mixed shapes")
	}
	if !strings.Contains(err.Error(), "mixed schema") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadV2_MixedShapesRejected_LegacyMainV2Local(t *testing.T) {
	setupSettingsDir(t,
		`{"enabled": true}`,
		`{"schema": 2, "logging": {"level": "`+debugLevel+`"}}`,
	)
	_, err := LoadV2(context.Background())
	if err == nil {
		t.Fatal("expected error for mixed shapes")
	}
}

func TestLoadV2FromBytes_Empty(t *testing.T) {
	t.Parallel()
	got, err := LoadV2FromBytes(nil)
	if err != nil {
		t.Fatalf("LoadV2FromBytes(nil): %v", err)
	}
	if got.Schema != CurrentSchemaVersion || !got.Enabled {
		t.Fatalf("defaults wrong: %+v", got)
	}
}

func TestLoadV2FromBytes_V2(t *testing.T) {
	t.Parallel()
	got, err := LoadV2FromBytes([]byte(`{"schema": 2, "enabled": true, "checkpoints": {"primary": {"type": "v2"}}}`))
	if err != nil {
		t.Fatalf("LoadV2FromBytes: %v", err)
	}
	if got.Checkpoints.Primary.Type != BackendTypeV2 {
		t.Fatalf("Primary.Type = %q, want v2", got.Checkpoints.Primary.Type)
	}
}

func TestLoadV2FromBytes_Legacy(t *testing.T) {
	t.Parallel()
	got, err := LoadV2FromBytes([]byte(`{"enabled": true, "strategy_options": {"checkpoints_v2": true}}`))
	if err != nil {
		t.Fatalf("LoadV2FromBytes: %v", err)
	}
	if got.Checkpoints.Primary.Type != BackendTypeV2 {
		t.Fatalf("Primary.Type = %q, want v2", got.Checkpoints.Primary.Type)
	}
}

func TestLoadV2FromBytes_UnknownFieldRejected(t *testing.T) {
	t.Parallel()
	_, err := LoadV2FromBytes([]byte(`{"schema": 2, "totally_unknown": true}`))
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	if !containsUnknownField(err.Error()) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadV2FromBytes_InvalidJSON(t *testing.T) {
	t.Parallel()
	_, err := LoadV2FromBytes([]byte(`{not json`))
	if err == nil {
		t.Fatal("expected JSON parse error")
	}
}

// TestSynthesizeFromLegacy_RoundTripFromBytes verifies the fully-loaded
// legacy parser feeds the synthesizer correctly. Catches mismatches between
// accessors and the synthesizer that unit cases on synthesize alone would miss.
func TestSynthesizeFromLegacy_RoundTripFromBytes(t *testing.T) {
	t.Parallel()
	legacyJSON := `{
		"enabled": true,
		"log_level": "` + debugLevel + `",
		"commit_linking": "always",
		"absolute_git_hook_path": true,
		"external_agents": true,
		"vercel": true,
		"telemetry": true,
		"sign_checkpoint_commits": false,
		"summary_timeout_seconds": 45,
		"summary_generation": {"provider": "` + providerClaudeCC + `", "model": "sonnet"},
		"redaction": {"pii": {"enabled": true}},
		"strategy_options": {
			"checkpoints_version": 2,
			"gmeta": true,
			"summarize": {"enabled": true},
			"checkpoint_remote": {"provider": "github", "repo": "org/repo"},
			"filtered_fetches": true,
			"push_sessions": false,
			"full_transcript_generation_retention_days": 30
		}
	}`
	legacy, err := LoadFromBytes([]byte(legacyJSON))
	if err != nil {
		t.Fatalf("LoadFromBytes: %v", err)
	}

	got := synthesizeFromLegacy(legacy)

	want := &Settings{
		Schema:   CurrentSchemaVersion,
		Enabled:  true,
		Logging:  LoggingConfig{Level: debugLevel},
		Hooks:    HooksConfig{CommitLinking: CommitLinkingAlways, AbsoluteGitHookPath: true},
		Features: FeaturesConfig{Summarize: true, ExternalAgents: true, Vercel: true},
		Checkpoints: CheckpointsConfig{
			Primary:                     BackendConfig{Type: BackendTypeV2},
			Mirrors:                     []BackendConfig{{Type: BackendTypeGmeta}},
			Remote:                      &CheckpointRemoteConfig{Provider: "github", Repo: "org/repo"},
			FullTranscriptRetentionDays: 30,
			SignCommits:                 boolPtr(false),
			FilteredFetches:             true,
			PushSessions:                boolPtr(false),
		},
		Redaction: &RedactionSettings{PII: &PIISettings{Enabled: true}},
		Telemetry: boolPtr(true),
		SummaryGeneration: &SummaryGenerationConfig{
			Provider:       providerClaudeCC,
			Model:          "sonnet",
			TimeoutSeconds: 45,
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}
