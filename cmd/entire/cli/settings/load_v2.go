// Schema-v2 loader and legacy synthesizer.
// See synthesizeFromLegacy for the v1→v2 mapping.

package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/entireio/cli/cmd/entire/cli/paths"
)

// Backend type constants used in BackendConfig.Type.
const (
	BackendTypeV1    = "v1"
	BackendTypeV2    = "v2"
	BackendTypeGmeta = "gmeta"
)

// Legacy strategy_options keys read by the synthesizer. The legacy package
// has no constants for these; defining them here keeps the synthesizer's
// references typo-proof without modifying legacy accessors.
const (
	legacyKeyGmeta                       = "gmeta"
	legacyKeyPushSessions                = "push_sessions"
	legacyKeyFullTranscriptRetentionDays = "full_transcript_generation_retention_days"
)

// LoadV2 loads settings as the v2 shape. Files declaring "schema": 2 are
// parsed natively; older files are loaded via the legacy parser and
// synthesized. The local override file is merged the same way.
//
// Returns Settings with sensible defaults populated (Schema=2, Enabled=true)
// if neither settings file exists.
func LoadV2(ctx context.Context) (*Settings, error) {
	mainAbs, err := paths.AbsPath(ctx, EntireSettingsFile)
	if err != nil {
		mainAbs = EntireSettingsFile
	}
	localAbs, err := paths.AbsPath(ctx, EntireSettingsLocalFile)
	if err != nil {
		localAbs = EntireSettingsLocalFile
	}

	mainData, mainErr := readSettingsFileIfExists(mainAbs)
	if mainErr != nil {
		return nil, fmt.Errorf("reading settings file: %w", mainErr)
	}
	localData, localErr := readSettingsFileIfExists(localAbs)
	if localErr != nil {
		return nil, fmt.Errorf("reading local settings file: %w", localErr)
	}

	switch {
	case mainData == nil && localData == nil:
		return defaultSettings(), nil

	case hasMixedShapes(mainData, localData):
		return nil, errors.New("mixed schema versions in settings files; run `entire migrate-config` to convert all settings to schema 2")

	case isSchemaV2(mainData) || isSchemaV2(localData):
		return loadFromV2Files(mainData, localData)

	default:
		// Both files (if present) are legacy shape. Defer to the legacy
		// loader to handle merging, then synthesize into v2.
		legacy, err := loadMergedSettings(mainAbs, localAbs)
		if err != nil {
			return nil, err
		}
		return synthesizeFromLegacy(legacy), nil
	}
}

// LoadV2FromBytes parses a single settings document from raw bytes.
// Accepts either schema-v2 JSON or legacy JSON; the shape is auto-detected
// via the schema field. No local-override merge is performed.
func LoadV2FromBytes(data []byte) (*Settings, error) {
	if len(data) == 0 {
		return defaultSettings(), nil
	}
	if isSchemaV2(data) {
		return parseV2Bytes(data)
	}
	legacy, err := LoadFromBytes(data)
	if err != nil {
		return nil, err
	}
	return synthesizeFromLegacy(legacy), nil
}

// readSettingsFileIfExists returns the file contents, or (nil, nil) if the
// file does not exist. Other I/O errors propagate.
func readSettingsFileIfExists(path string) ([]byte, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is from AbsPath or constant
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err //nolint:wrapcheck // caller wraps with file context
	}
	return data, nil
}

// isSchemaV2 reports whether a settings JSON document declares schema >= 2.
// Returns false for nil/empty input or invalid JSON; callers handle those
// cases explicitly downstream.
func isSchemaV2(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	var probe struct {
		Schema int `json:"schema"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	return probe.Schema >= CurrentSchemaVersion
}

// hasMixedShapes reports whether main and local settings files declare
// different schema shapes (one v2, one legacy). Returns false when either
// file is missing — only the both-present-different-shape case is mixed.
func hasMixedShapes(mainData, localData []byte) bool {
	if mainData == nil || localData == nil {
		return false
	}
	return isSchemaV2(mainData) != isSchemaV2(localData)
}

// loadFromV2Files parses one or both files as schema-v2 JSON and applies the
// local override on top of the main settings. At least one of the inputs is
// guaranteed by the caller to be a v2 document.
func loadFromV2Files(mainData, localData []byte) (*Settings, error) {
	settings := defaultSettings()
	if mainData != nil {
		parsed, err := parseV2Bytes(mainData)
		if err != nil {
			return nil, fmt.Errorf("parsing settings file: %w", err)
		}
		settings = parsed
	}
	if localData != nil {
		if err := mergeV2Override(settings, localData); err != nil {
			return nil, fmt.Errorf("merging local settings: %w", err)
		}
	}
	return settings, nil
}

// parseV2Bytes parses a single schema-v2 JSON document into Settings.
// Strict decoding rejects unknown fields so typos become loud failures.
func parseV2Bytes(data []byte) (*Settings, error) {
	settings := defaultSettings()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(settings); err != nil {
		return nil, fmt.Errorf("parsing settings: %w", err)
	}
	return settings, nil
}

// mergeV2Override applies local overrides on top of an existing Settings.
// Only top-level fields explicitly present in the override JSON are
// replaced; absent fields preserve the base. Per-field merge of nested
// groups is intentionally not done — the legacy code only got more
// granular for redaction/PII, and we will revisit when a real call site
// needs it.
func mergeV2Override(base *Settings, data []byte) error {
	var presence map[string]json.RawMessage
	if err := json.Unmarshal(data, &presence); err != nil {
		return fmt.Errorf("parsing local settings: %w", err)
	}
	if _, ok := presence["schema"]; !ok {
		// Defensive: the v2 path is only entered when at least one file
		// declares schema 2; a local-only override without it would have
		// taken the legacy branch in LoadV2.
		return errors.New("local settings file is missing required schema field")
	}

	if err := decodeOverrideField(presence, "enabled", &base.Enabled); err != nil {
		return err
	}
	if err := decodeOverrideField(presence, "local_dev", &base.LocalDev); err != nil {
		return err
	}
	if err := decodeOverrideField(presence, "logging", &base.Logging); err != nil {
		return err
	}
	if err := decodeOverrideField(presence, "checkpoints", &base.Checkpoints); err != nil {
		return err
	}
	if err := decodeOverrideField(presence, "hooks", &base.Hooks); err != nil {
		return err
	}
	if err := decodeOverrideField(presence, "features", &base.Features); err != nil {
		return err
	}
	if err := decodeOverrideField(presence, "redaction", &base.Redaction); err != nil {
		return err
	}
	if err := decodeOverrideField(presence, "telemetry", &base.Telemetry); err != nil {
		return err
	}
	if err := decodeOverrideField(presence, "summary_generation", &base.SummaryGeneration); err != nil {
		return err
	}
	return nil
}

// decodeOverrideField decodes the named override field into dst if present
// in the parsed top-level map. No-op when the key is absent, preserving
// the base value.
func decodeOverrideField(presence map[string]json.RawMessage, key string, dst any) error {
	raw, ok := presence[key]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("parsing %s override: %w", key, err)
	}
	return nil
}

// defaultSettings returns the zero-value Settings with documented defaults
// applied (Schema = 2, Enabled = true). Callers should treat this as the
// starting point before parsing a file or merging overrides.
func defaultSettings() *Settings {
	return &Settings{
		Schema:  CurrentSchemaVersion,
		Enabled: true,
	}
}

// synthesizeFromLegacy maps an EntireSettings into a Settings, encoding the
// legacy → v2 migration. This is the single source of truth for the
// translation and is reused by `entire migrate-config`.
//
// Dropped (no v2 destination):
//   - push_v2_refs: was a dual-write transition knob; v2 ref pushes will
//     be a property of the v2 backend's CheckpointSyncer, not a settings flag.
//   - strategy: deprecated tolerator field.
//
// See the function body for field-by-field mapping.
func synthesizeFromLegacy(s *EntireSettings) *Settings {
	if s == nil {
		return defaultSettings()
	}

	out := &Settings{
		Schema:   CurrentSchemaVersion,
		Enabled:  s.Enabled,
		LocalDev: s.LocalDev,
		Logging: LoggingConfig{
			Level: s.LogLevel,
		},
		Hooks: HooksConfig{
			CommitLinking:       s.CommitLinking,
			AbsoluteGitHookPath: s.AbsoluteGitHookPath,
		},
		Features: FeaturesConfig{
			Summarize:      s.IsSummarizeEnabled(),
			ExternalAgents: s.ExternalAgents,
			Vercel:         s.Vercel,
		},
		Redaction: s.Redaction,
		Telemetry: s.Telemetry,
	}

	out.Checkpoints = synthesizeCheckpointsConfig(s)

	if hasSummaryGeneration(s) {
		out.SummaryGeneration = &SummaryGenerationConfig{
			TimeoutSeconds: s.SummaryTimeoutSeconds,
		}
		if s.SummaryGeneration != nil {
			out.SummaryGeneration.Provider = s.SummaryGeneration.Provider
			out.SummaryGeneration.Model = s.SummaryGeneration.Model
		}
	}

	return out
}

// synthesizeCheckpointsConfig builds the v2 Checkpoints group from the
// legacy EntireSettings. Split out for readability since the mapping pulls
// from both top-level fields and the strategy_options soup.
func synthesizeCheckpointsConfig(s *EntireSettings) CheckpointsConfig {
	cfg := CheckpointsConfig{
		Primary:                     BackendConfig{Type: legacyPrimaryBackend(s)},
		Remote:                      s.GetCheckpointRemote(),
		FullTranscriptRetentionDays: legacyFullTranscriptRetention(s),
		SignCommits:                 s.SignCheckpointCommits,
		FilteredFetches:             s.IsFilteredFetchesEnabled(),
		PushSessions:                legacyPushSessions(s),
	}

	if legacyGmetaEnabled(s) {
		cfg.Mirrors = append(cfg.Mirrors, BackendConfig{Type: BackendTypeGmeta})
	}

	return cfg
}

// legacyPrimaryBackend returns "v2" if either checkpoints_version or
// checkpoints_v2 enables v2 in the legacy settings, "v1" otherwise.
func legacyPrimaryBackend(s *EntireSettings) string {
	if s.IsCheckpointsV2Enabled() {
		return BackendTypeV2
	}
	return BackendTypeV1
}

// legacyGmetaEnabled checks the strategy_options.gmeta flag. The flag does
// not exist on this branch (it ships with the gmeta mainport), but the
// synthesizer recognizes it ahead of time so settings files written by
// that branch round-trip cleanly.
func legacyGmetaEnabled(s *EntireSettings) bool {
	if s.StrategyOptions == nil {
		return false
	}
	val, ok := s.StrategyOptions[legacyKeyGmeta].(bool)
	return ok && val
}

// legacyFullTranscriptRetention returns the configured retention only when
// the legacy key is explicitly set; the legacy accessor would return a
// default for unset values, which we suppress here so the v2 file stays
// minimal.
func legacyFullTranscriptRetention(s *EntireSettings) int {
	if s.StrategyOptions == nil {
		return 0
	}
	if _, ok := s.StrategyOptions[legacyKeyFullTranscriptRetentionDays]; !ok {
		return 0
	}
	return s.GetFullTranscriptGenerationRetentionDays()
}

// legacyPushSessions extracts the explicit push_sessions boolean if set;
// otherwise returns nil so the v2 file stays minimal. Tri-state preserves
// the legacy "explicit false disables, otherwise default" semantics.
func legacyPushSessions(s *EntireSettings) *bool {
	if s.StrategyOptions == nil {
		return nil
	}
	val, ok := s.StrategyOptions[legacyKeyPushSessions]
	if !ok {
		return nil
	}
	b, ok := val.(bool)
	if !ok {
		return nil
	}
	return &b
}

// hasSummaryGeneration reports whether any summary-generation field on the
// legacy settings is non-default. Used to decide whether to allocate the
// v2 SummaryGenerationConfig at all (vs. leaving it nil).
func hasSummaryGeneration(s *EntireSettings) bool {
	if s.SummaryGeneration != nil && (s.SummaryGeneration.Provider != "" || s.SummaryGeneration.Model != "") {
		return true
	}
	return s.SummaryTimeoutSeconds > 0
}
