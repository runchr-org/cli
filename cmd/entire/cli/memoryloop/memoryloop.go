package memoryloop

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
	"github.com/entireio/cli/cmd/entire/cli/jsonutil"
	"github.com/entireio/cli/cmd/entire/cli/paths"
)

const (
	fileName             = "memory-loop.json"
	DefaultMaxInjected   = 2
	DefaultRefreshWindow = 20
	maxInjectionLogs     = 50
	maxInjectionBytes    = 1200
	maxHistoryEvents     = 100
)

var stopWords = map[string]struct{}{
	"about":  {},
	"all":    {},
	"also":   {},
	"and":    {},
	"are":    {},
	"been":   {},
	"before": {},
	"but":    {},
	"can":    {},
	"does":   {},
	"each":   {},
	"for":    {},
	"from":   {},
	"has":    {},
	"have":   {},
	"how":    {},
	"into":   {},
	"its":    {},
	"just":   {},
	"like":   {},
	"may":    {},
	"more":   {},
	"most":   {},
	"need":   {},
	"not":    {},
	"only":   {},
	"other":  {},
	"our":    {},
	"should": {},
	"some":   {},
	"than":   {},
	"that":   {},
	"the":    {},
	"them":   {},
	"then":   {},
	"this":   {},
	"use":    {},
	"was":    {},
	"were":   {},
	"what":   {},
	"when":   {},
	"will":   {},
	"with":   {},
	"way":    {},
	"your":   {},
}

type Kind string

const (
	KindRepoRule         Kind = "repo_rule"
	KindWorkflowRule     Kind = "workflow_rule"
	KindAgentInstruction Kind = "agent_instruction"
	KindSkillPatch       Kind = "skill_patch"
	KindAntiPattern      Kind = "anti_pattern"
)

type Mode string

const (
	ModeOff    Mode = "off"
	ModeManual Mode = "manual"
	ModeAuto   Mode = "auto"
)

type ActivationPolicy string

const (
	ActivationPolicyReview ActivationPolicy = "review"
	ActivationPolicyAuto   ActivationPolicy = "auto"
)

type Status string

const (
	StatusCandidate  Status = "candidate"
	StatusActive     Status = "active"
	StatusSuppressed Status = "suppressed"
	StatusArchived   Status = "archived"
)

type ScopeKind string

const (
	ScopeKindMe     ScopeKind = "me"
	ScopeKindRepo   ScopeKind = "repo"
	ScopeKindBranch ScopeKind = "branch"
)

// DefaultInjectionScopes returns all scope kinds, used when InjectionScopes is empty (backward compat).
func DefaultInjectionScopes() []ScopeKind {
	return []ScopeKind{ScopeKindMe, ScopeKindRepo, ScopeKindBranch}
}

type Origin string

const (
	OriginGenerated Origin = "generated"
	OriginManual    Origin = "manual"
)

type Outcome string

const (
	OutcomeNeutral     Outcome = "neutral"
	OutcomeReinforced  Outcome = "reinforced"
	OutcomeIneffective Outcome = "ineffective"
)

type LifecycleAction string

const (
	LifecycleActionActivate   LifecycleAction = "activate"
	LifecycleActionPromote    LifecycleAction = "promote"
	LifecycleActionSuppress   LifecycleAction = "suppress"
	LifecycleActionUnsuppress LifecycleAction = "unsuppress"
	LifecycleActionArchive    LifecycleAction = "archive"
)

type HistoryEvent struct {
	Type      string    `json:"type"`
	At        time.Time `json:"at"`
	Detail    string    `json:"detail,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
}

type MemoryRecord struct {
	ID               string         `json:"id"`
	Kind             Kind           `json:"kind"`
	Title            string         `json:"title"`
	Body             string         `json:"body"`
	SkillName        string         `json:"skill_name,omitempty"`
	SkillPath        string         `json:"skill_path,omitempty"`
	Why              string         `json:"why,omitempty"`
	Evidence         []string       `json:"evidence,omitempty"`
	SourceSessionIDs []string       `json:"source_session_ids,omitempty"`
	Confidence       string         `json:"confidence,omitempty"`
	Strength         int            `json:"strength"`
	Status           Status         `json:"status"`
	Fingerprint      string         `json:"fingerprint,omitempty"`
	ScopeKind        ScopeKind      `json:"scope_kind,omitempty"`
	ScopeValue       string         `json:"scope_value,omitempty"`
	Origin           Origin         `json:"origin,omitempty"`
	OwnerEmail       string         `json:"owner_email,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	LastReviewedAt   time.Time      `json:"last_reviewed_at,omitempty"`
	LastInjectedAt   time.Time      `json:"last_injected_at,omitempty"`
	LastMatchedAt    time.Time      `json:"last_matched_at,omitempty"`
	InjectCount      int            `json:"inject_count,omitempty"`
	MatchCount       int            `json:"match_count,omitempty"`
	Outcome          Outcome        `json:"outcome,omitempty"`
	History          []HistoryEvent `json:"history,omitempty"`
	LegacyInferred   bool           `json:"legacy_inferred,omitempty"`
}

type RefreshHistory struct {
	At                      time.Time `json:"at"`
	Scope                   string    `json:"scope,omitempty"`
	ScopeValue              string    `json:"scope_value,omitempty"`
	SourceWindow            int       `json:"source_window,omitempty"`
	GeneratedCount          int       `json:"generated_count,omitempty"`
	ActivatedCount          int       `json:"activated_count,omitempty"`
	CandidateCount          int       `json:"candidate_count,omitempty"`
	FilteredWeakCount       int       `json:"filtered_weak_count,omitempty"`
	FilteredGenericCount    int       `json:"filtered_generic_count,omitempty"`
	FilteredNoEvidenceCount int       `json:"filtered_no_evidence_count,omitempty"`
	DedupedCount            int       `json:"deduped_count,omitempty"`
	DemotedCount            int       `json:"demoted_count,omitempty"`
	PrunedCount             int       `json:"pruned_count,omitempty"`
	InputTokens             int       `json:"input_tokens,omitempty"`
	OutputTokens            int       `json:"output_tokens,omitempty"`
	TotalCostUSD            float64   `json:"total_cost_usd,omitempty"`
}

type Store struct {
	Version          int              `json:"version"`
	GeneratedAt      time.Time        `json:"generated_at"`
	SourceWindow     int              `json:"source_window"`
	Scope            string           `json:"scope,omitempty"`
	ScopeValue       string           `json:"scope_value,omitempty"`
	Records          []MemoryRecord   `json:"records,omitempty"`
	Mode             Mode             `json:"mode,omitempty"`
	Debug            bool             `json:"debug,omitempty"`
	ActivationPolicy ActivationPolicy `json:"activation_policy,omitempty"`
	InjectionEnabled bool             `json:"injection_enabled"`
	MaxInjected      int              `json:"max_injected"`
	InjectionScopes  []ScopeKind      `json:"injection_scopes,omitempty"`
	RefreshHistory   []RefreshHistory `json:"refresh_history,omitempty"`
}

type Snapshot = Store

type InjectionLog struct {
	SessionID         string    `json:"session_id"`
	PromptPreview     string    `json:"prompt_preview"`
	InjectedMemoryIDs []string  `json:"injected_memory_ids,omitempty"`
	InjectedAt        time.Time `json:"injected_at"`
	Reason            string    `json:"reason,omitempty"`
}

type State struct {
	Store         *Store         `json:"-"`
	Snapshot      *Snapshot      `json:"-"`
	InjectionLogs []InjectionLog `json:"injection_logs,omitempty"`
}

type Match struct {
	Record    MemoryRecord
	Score     int
	Reason    string
	Rationale SelectionRationale
}

type SkippedMatch struct {
	Record    MemoryRecord
	Reason    string
	Rationale SelectionRationale
}

type SelectionReport struct {
	Matches []Match
	Skipped []SkippedMatch
}

type SelectionRationale struct {
	BaseScore       int
	OutcomeBonus    int
	ScopeBonus      int
	CooldownPenalty int
	AdjustedScore   int
}

type scoredCandidate struct {
	record          MemoryRecord
	baseScore       int
	outcomeBonus    int
	scopeBonus      int
	cooldownPenalty int
	adjustedScore   int
	reason          string
	rationale       SelectionRationale
}

type ReconcileResult struct {
	Records []MemoryRecord
	History RefreshHistory
}

type ManualRecordInput struct {
	Kind       Kind
	Title      string
	Body       string
	ScopeKind  ScopeKind
	ScopeValue string
	OwnerEmail string
}

type AdoptionInput struct {
	ID         string
	ScopeKind  ScopeKind
	ScopeValue string
	OwnerEmail string
}

type PruneResult struct {
	ArchivedCount int
	DemotedCount  int
	PrunedCount   int
}

type recordMatchSignals struct {
	primaryTokens map[string]struct{}
}

func StatePath(ctx context.Context) (string, error) {
	path, err := paths.AbsPath(ctx, filepath.Join(paths.EntireDir, fileName))
	if err != nil {
		return "", fmt.Errorf("resolve memory-loop path: %w", err)
	}
	return path, nil
}

func LoadState(ctx context.Context) (*State, error) {
	path, err := StatePath(ctx)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path) //nolint:gosec // repo-local metadata path
	if err != nil {
		if os.IsNotExist(err) {
			return &State{}, nil
		}
		return nil, fmt.Errorf("read memory-loop state: %w", err)
	}

	var disk diskState
	if err := json.Unmarshal(data, &disk); err != nil {
		return nil, fmt.Errorf("parse memory-loop state: %w", err)
	}

	state := &State{
		Store:         disk.Store,
		Snapshot:      disk.Snapshot,
		InjectionLogs: disk.InjectionLogs,
	}
	normalizeStateWithSource(state, disk.Store == nil && disk.Snapshot != nil)
	return state, nil
}

func SaveState(ctx context.Context, state *State) error {
	if state == nil {
		state = &State{}
	}
	normalizeState(state)

	path, err := StatePath(ctx)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create memory-loop directory: %w", err)
	}

	data, err := jsonutil.MarshalIndentWithNewline(diskState{
		Store:         state.Store,
		InjectionLogs: state.InjectionLogs,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal memory-loop state: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // repo-local metadata path
		return fmt.Errorf("write memory-loop state: %w", err)
	}
	return nil
}

func AppendInjectionLog(ctx context.Context, log InjectionLog) error {
	state, err := LoadState(ctx)
	if err != nil {
		return err
	}
	state.InjectionLogs = append(state.InjectionLogs, log)
	if len(state.InjectionLogs) > maxInjectionLogs {
		state.InjectionLogs = state.InjectionLogs[len(state.InjectionLogs)-maxInjectionLogs:]
	}
	return SaveState(ctx, state)
}

// selectConfig holds options for SelectRelevant/PreviewSelection.
type selectConfig struct {
	injectionScopes []ScopeKind
	currentBranch   string
	currentOwnerID  string
}

// SelectOption configures the behavior of SelectRelevant/PreviewSelection.
type SelectOption func(*selectConfig)

// WithInjectionScopes limits selection to records matching the given scopes.
// When empty or nil, all scopes are allowed (backward compatible).
func WithInjectionScopes(scopes []ScopeKind) SelectOption {
	return func(c *selectConfig) { c.injectionScopes = scopes }
}

// WithCurrentBranch sets the current branch for branch-scope matching.
// Branch-scoped records are only selected when their ScopeValue matches this branch.
func WithCurrentBranch(branch string) SelectOption {
	return func(c *selectConfig) { c.currentBranch = branch }
}

// WithCurrentOwnerID sets the current owner ID for personal-scope matching.
func WithCurrentOwnerID(ownerID string) SelectOption {
	return func(c *selectConfig) { c.currentOwnerID = strings.TrimSpace(ownerID) }
}

func (c *selectConfig) passesScope(record MemoryRecord) bool {
	if len(c.injectionScopes) == 0 {
		return c.matchesScope(record)
	}
	for _, s := range c.injectionScopes {
		if record.ScopeKind == s {
			return c.matchesScope(record)
		}
	}
	return false
}

func (c *selectConfig) matchesScope(record MemoryRecord) bool {
	switch record.ScopeKind {
	case ScopeKindBranch:
		if c.currentBranch == "" {
			return true
		}
		return record.ScopeValue == c.currentBranch
	case ScopeKindMe:
		if c.currentOwnerID == "" {
			return true
		}
		if record.ScopeValue == "" {
			return record.LegacyInferred
		}
		return strings.EqualFold(record.ScopeValue, c.currentOwnerID)
	case ScopeKindRepo:
		return true
	default:
		return true
	}
}

func SelectRelevant(snapshot Snapshot, prompt string, now time.Time, opts ...SelectOption) []Match {
	return PreviewSelection(snapshot, prompt, now, opts...).Matches
}

func PreviewSelection(snapshot Snapshot, prompt string, now time.Time, opts ...SelectOption) SelectionReport {
	cfg := &selectConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	maxInjected := snapshot.MaxInjected
	if maxInjected <= 0 {
		maxInjected = DefaultMaxInjected
	}

	promptTokens := tokenize(prompt)
	if len(promptTokens) == 0 {
		return SelectionReport{}
	}

	candidates := make([]scoredCandidate, 0, len(snapshot.Records))
	skipped := make([]SkippedMatch, 0, len(snapshot.Records))
	for _, record := range snapshot.Records {
		signals := buildRecordMatchSignals(record)
		primaryOverlap := tokenOverlap(promptTokens, signals.primaryTokens)
		if !cfg.passesScope(record) {
			if primaryOverlap > 0 {
				skipped = append(skipped, SkippedMatch{
					Record: record,
					Reason: "scope mismatch",
				})
			}
			continue
		}
		if reason, rejected := injectionRejectionReason(record, primaryOverlap); rejected {
			if primaryOverlap > 0 {
				skipped = append(skipped, SkippedMatch{
					Record: record,
					Reason: reason,
				})
			}
			continue
		}
		candidate := buildScoredCandidate(record, primaryOverlap, now)
		if candidate.adjustedScore <= 0 {
			skipped = append(skipped, SkippedMatch{
				Record: record,
				Reason: "score adjusted below zero",
			})
			continue
		}
		candidates = append(candidates, candidate)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].adjustedScore != candidates[j].adjustedScore {
			return candidates[i].adjustedScore > candidates[j].adjustedScore
		}
		if candidates[i].baseScore != candidates[j].baseScore {
			return candidates[i].baseScore > candidates[j].baseScore
		}
		if candidates[i].record.Strength != candidates[j].record.Strength {
			return candidates[i].record.Strength > candidates[j].record.Strength
		}
		return candidates[i].record.Title < candidates[j].record.Title
	})

	matches := packSelectedCandidates(candidates, maxInjected)
	skipped = append(skipped, explainSkippedCandidates(candidates, matches)...)
	return SelectionReport{
		Matches: matches,
		Skipped: skipped,
	}
}

func ReconcileGeneratedRecords(existing, generated []MemoryRecord, scopeKind ScopeKind, scopeValue string, policy ActivationPolicy, now time.Time) ReconcileResult {
	records := append([]MemoryRecord(nil), existing...)
	byScopeKey := make(map[string]int, len(records))
	for i := range records {
		fingerprint := records[i].Fingerprint
		if fingerprint == "" {
			fingerprint = FingerprintForRecord(records[i].Kind, records[i].Title, records[i].Body)
			records[i].Fingerprint = fingerprint
		}
		if records[i].ScopeKind == "" {
			records[i].ScopeKind = scopeKind
		}
		byScopeKey[recordScopeKey(records[i].Fingerprint, records[i].ScopeKind, records[i].ScopeValue)] = i
	}

	result := ReconcileResult{
		Records: records,
		History: RefreshHistory{
			At:           now,
			Scope:        string(scopeKind),
			ScopeValue:   scopeValue,
			SourceWindow: DefaultRefreshWindow,
		},
	}

	for _, generatedRecord := range generated {
		result.History.GeneratedCount++

		record := generatedRecord
		if record.Kind == "" {
			record.Kind = KindRepoRule
		}
		if record.Origin == "" {
			record.Origin = OriginGenerated
		}
		if record.Fingerprint == "" {
			record.Fingerprint = FingerprintForRecord(record.Kind, record.Title, record.Body)
		}
		if record.ScopeKind == "" {
			record.ScopeKind = scopeKind
		}
		if record.ScopeValue == "" {
			record.ScopeValue = scopeValue
		}
		if record.CreatedAt.IsZero() {
			record.CreatedAt = now
		}
		record.UpdatedAt = now

		if idx, exists := findReconcileIndex(result.Records, byScopeKey, record); exists {
			reconciled := result.Records[idx]
			reconciled.Kind = record.Kind
			reconciled.Title = record.Title
			reconciled.Body = record.Body
			reconciled.Why = record.Why
			reconciled.Evidence = record.Evidence
			reconciled.SourceSessionIDs = record.SourceSessionIDs
			reconciled.Confidence = record.Confidence
			reconciled.Strength = record.Strength
			reconciled.Fingerprint = record.Fingerprint
			reconciled.ScopeKind = record.ScopeKind
			reconciled.ScopeValue = record.ScopeValue
			reconciled.Origin = record.Origin
			reconciled.UpdatedAt = now
			reconciled.Status = reconciledStatus(reconciled.Status, record.ScopeKind, policy)
			switch reconciled.Status {
			case StatusActive:
				result.History.ActivatedCount++
			case StatusCandidate:
				result.History.CandidateCount++
			case StatusSuppressed, StatusArchived:
			}
			result.Records[idx] = reconciled
			continue
		}

		record.Status = reconciledStatus(record.Status, record.ScopeKind, policy)
		switch record.Status {
		case StatusActive:
			result.History.ActivatedCount++
		case StatusCandidate:
			result.History.CandidateCount++
		case StatusSuppressed, StatusArchived:
		}

		result.Records = append(result.Records, record)
		byScopeKey[recordScopeKey(record.Fingerprint, record.ScopeKind, record.ScopeValue)] = len(result.Records) - 1
	}

	return result
}

func reconciledStatus(existing Status, scopeKind ScopeKind, policy ActivationPolicy) Status {
	switch existing {
	case StatusSuppressed, StatusArchived:
		return existing
	case StatusActive:
		if scopeKind == ScopeKindMe {
			return StatusActive
		}
	case StatusCandidate:
	}

	if scopeKind == ScopeKindRepo {
		return StatusCandidate
	}
	if policy == ActivationPolicyAuto {
		return StatusActive
	}
	return StatusCandidate
}

func recordScopeKey(fingerprint string, scopeKind ScopeKind, scopeValue string) string {
	return strings.Join([]string{fingerprint, string(scopeKind), scopeValue}, "|")
}

func findReconcileIndex(records []MemoryRecord, byScopeKey map[string]int, record MemoryRecord) (int, bool) {
	if idx, exists := byScopeKey[recordScopeKey(record.Fingerprint, record.ScopeKind, record.ScopeValue)]; exists {
		return idx, true
	}
	if record.ScopeKind == ScopeKindMe && record.ScopeValue != "" {
		if idx, exists := byScopeKey[recordScopeKey(record.Fingerprint, record.ScopeKind, "")]; exists {
			return idx, true
		}
	}

	bestIdx := -1
	bestScore := 0.0
	for i, existing := range records {
		if existing.Kind != record.Kind {
			continue
		}
		if !sameReconcileScope(existing, record) {
			continue
		}
		score := logicalRuleSimilarity(existing, record)
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}
	if bestIdx >= 0 && bestScore >= 0.50 {
		return bestIdx, true
	}
	return -1, false
}

func logicalRuleSimilarity(a, b MemoryRecord) float64 {
	aTokens := tokenize(strings.Join([]string{a.Title, a.Body}, " "))
	bTokens := tokenize(strings.Join([]string{b.Title, b.Body}, " "))
	if len(aTokens) == 0 || len(bTokens) == 0 {
		return 0
	}

	intersection := 0
	for token := range aTokens {
		if _, ok := bTokens[token]; ok {
			intersection++
		}
	}
	if intersection < 3 {
		return 0
	}

	union := len(aTokens)
	for token := range bTokens {
		if _, ok := aTokens[token]; !ok {
			union++
		}
	}
	if union == 0 {
		return 0
	}

	jaccard := float64(intersection) / float64(union)
	minSize := len(aTokens)
	if len(bTokens) < minSize {
		minSize = len(bTokens)
	}
	if minSize == 0 {
		return 0
	}
	overlapCoeff := float64(intersection) / float64(minSize)
	if overlapCoeff > jaccard {
		return overlapCoeff
	}
	return jaccard
}

func sameReconcileScope(a, b MemoryRecord) bool {
	if a.ScopeKind != b.ScopeKind {
		return false
	}
	if a.ScopeValue == b.ScopeValue {
		return true
	}
	if a.ScopeKind == ScopeKindMe && (a.ScopeValue == "" || b.ScopeValue == "") {
		return true
	}
	return false
}

func FormatInjectionBlock(matches []Match) string {
	if len(matches) == 0 {
		return ""
	}

	var buf bytes.Buffer
	buf.WriteString("Memory For This Repo")
	for _, match := range matches {
		line := formatInjectionLine(match)
		if buf.Len()+1+len(line) > maxInjectionBytes {
			break
		}
		buf.WriteByte('\n')
		buf.WriteString(line)
	}

	return buf.String()
}

func TransitionRecordLifecycle(records []MemoryRecord, id string, action LifecycleAction, now time.Time) ([]MemoryRecord, MemoryRecord, error) {
	updated := append([]MemoryRecord(nil), records...)
	for i := range updated {
		if updated[i].ID != id {
			continue
		}

		record, err := transitionRecord(updated[i], action, now)
		if err != nil {
			return updated, updated[i], err
		}
		updated[i] = record
		return updated, record, nil
	}

	return updated, MemoryRecord{}, fmt.Errorf("memory not found: %s", id)
}

func AddManualRecord(records []MemoryRecord, input ManualRecordInput, now time.Time) ([]MemoryRecord, MemoryRecord, error) {
	if strings.TrimSpace(input.Title) == "" {
		return records, MemoryRecord{}, errors.New("memory title is required")
	}
	if strings.TrimSpace(input.Body) == "" {
		return records, MemoryRecord{}, errors.New("memory body is required")
	}
	if input.Kind == "" {
		return records, MemoryRecord{}, errors.New("memory kind is required")
	}
	if input.ScopeKind == "" {
		input.ScopeKind = ScopeKindMe
	}

	record := MemoryRecord{
		ID:          MakeRecordID(input.Kind, input.Title),
		Kind:        input.Kind,
		Title:       strings.TrimSpace(input.Title),
		Body:        strings.TrimSpace(input.Body),
		Fingerprint: FingerprintForRecord(input.Kind, input.Title, input.Body),
		ScopeKind:   input.ScopeKind,
		ScopeValue:  strings.TrimSpace(input.ScopeValue),
		Origin:      OriginManual,
		Confidence:  "high",
		Strength:    4,
		OwnerEmail:  strings.TrimSpace(input.OwnerEmail),
		Status:      StatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
		History: []HistoryEvent{
			{Type: "added", At: now},
		},
	}

	updated := append([]MemoryRecord(nil), records...)
	if idx, exists := findReconcileIndex(updated, indexRecordScopeKeys(updated), record); exists {
		existing := updated[idx]
		existing.Kind = record.Kind
		existing.Title = record.Title
		existing.Body = record.Body
		existing.Fingerprint = record.Fingerprint
		existing.ScopeKind = record.ScopeKind
		existing.ScopeValue = record.ScopeValue
		existing.Origin = OriginManual
		existing.Confidence = record.Confidence
		existing.Strength = record.Strength
		existing.OwnerEmail = record.OwnerEmail
		existing.Status = StatusActive
		existing.UpdatedAt = now
		existing.LastReviewedAt = now
		existing.History = append(existing.History, HistoryEvent{Type: "added", At: now})
		updated[idx] = existing
		return updated, existing, nil
	}

	updated = append(updated, record)
	return updated, record, nil
}

func AdoptRecord(records []MemoryRecord, input AdoptionInput, now time.Time) ([]MemoryRecord, MemoryRecord, error) {
	id := strings.TrimSpace(input.ID)
	if id == "" {
		return records, MemoryRecord{}, errors.New("memory id is required")
	}

	sourceIdx := -1
	for i := range records {
		if records[i].ID == id {
			sourceIdx = i
			break
		}
	}
	if sourceIdx < 0 {
		return records, MemoryRecord{}, fmt.Errorf("memory not found: %s", id)
	}

	source := records[sourceIdx]
	if source.Status == StatusSuppressed {
		return records, MemoryRecord{}, fmt.Errorf("memory %q is suppressed", source.ID)
	}
	if source.Status == StatusArchived {
		return records, MemoryRecord{}, fmt.Errorf("memory %q is archived", source.ID)
	}

	targetScopeKind := input.ScopeKind
	targetScopeValue := strings.TrimSpace(input.ScopeValue)
	targetOwnerEmail := strings.TrimSpace(input.OwnerEmail)

	switch targetScopeKind {
	case ScopeKindRepo:
		if targetScopeValue != "" {
			return records, MemoryRecord{}, errors.New("repo-scoped adoption must not set a scope value")
		}
	case ScopeKindMe, ScopeKindBranch:
		if targetScopeValue == "" {
			return records, MemoryRecord{}, fmt.Errorf("%s-scoped adoption requires a scope value", targetScopeKind)
		}
	default:
		return records, MemoryRecord{}, fmt.Errorf("invalid adoption scope: %s", targetScopeKind)
	}

	if source.ScopeKind == targetScopeKind && strings.TrimSpace(source.ScopeValue) == targetScopeValue {
		return records, MemoryRecord{}, fmt.Errorf("memory %q already has scope %s", source.ID, targetScopeKind)
	}

	if targetOwnerEmail == "" {
		targetOwnerEmail = strings.TrimSpace(source.OwnerEmail)
	}

	adopted := source
	adopted.ID = adoptedRecordID(source, targetScopeKind, targetScopeValue)
	adopted.ScopeKind = targetScopeKind
	adopted.ScopeValue = targetScopeValue
	adopted.OwnerEmail = targetOwnerEmail
	adopted.Status = StatusActive
	adopted.Origin = source.Origin
	adopted.CreatedAt = now
	adopted.UpdatedAt = now
	adopted.LastReviewedAt = now
	adopted.LastInjectedAt = time.Time{}
	adopted.LastMatchedAt = time.Time{}
	adopted.InjectCount = 0
	adopted.MatchCount = 0
	adopted.Outcome = OutcomeNeutral
	adopted.History = []HistoryEvent{
		{
			Type:   "adopted",
			At:     now,
			Detail: "from " + source.ID,
		},
	}

	updated := append([]MemoryRecord(nil), records...)
	if idx, exists := findExactRecordScopeIndex(updated, adopted); exists {
		existing := updated[idx]
		existing.Kind = adopted.Kind
		existing.Title = adopted.Title
		existing.Body = adopted.Body
		existing.Why = adopted.Why
		existing.Evidence = adopted.Evidence
		existing.SourceSessionIDs = adopted.SourceSessionIDs
		existing.Confidence = adopted.Confidence
		existing.Strength = adopted.Strength
		existing.Fingerprint = adopted.Fingerprint
		existing.ScopeKind = adopted.ScopeKind
		existing.ScopeValue = adopted.ScopeValue
		existing.Origin = adopted.Origin
		existing.OwnerEmail = adopted.OwnerEmail
		existing.UpdatedAt = adopted.UpdatedAt
		existing.LastReviewedAt = adopted.LastReviewedAt
		existing.Status = adopted.Status
		existing.History = append(existing.History, adopted.History[0])
		updated[idx] = existing
		return updated, existing, nil
	}

	updated = append(updated, adopted)
	return updated, adopted, nil
}

func RecordInjectionActivity(state *State, matches []Match, log InjectionLog, now time.Time) {
	if state == nil || state.Store == nil {
		return
	}

	matchIDs := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		matchIDs[match.Record.ID] = struct{}{}
	}

	for i := range state.Store.Records {
		if _, ok := matchIDs[state.Store.Records[i].ID]; !ok {
			continue
		}
		state.Store.Records[i].MatchCount++
		state.Store.Records[i].LastMatchedAt = now
		state.Store.Records[i].History = append(state.Store.Records[i].History, HistoryEvent{
			Type: "matched",
			At:   now,
		})
		state.Store.Records[i].InjectCount++
		state.Store.Records[i].LastInjectedAt = now
		state.Store.Records[i].History = append(state.Store.Records[i].History, HistoryEvent{
			Type: "injected",
			At:   now,
		})
		if len(state.Store.Records[i].History) > maxHistoryEvents {
			state.Store.Records[i].History = state.Store.Records[i].History[len(state.Store.Records[i].History)-maxHistoryEvents:]
		}
	}

	state.InjectionLogs = append(state.InjectionLogs, log)
	if len(state.InjectionLogs) > maxInjectionLogs {
		state.InjectionLogs = state.InjectionLogs[len(state.InjectionLogs)-maxInjectionLogs:]
	}
}

func DeriveOutcomes(records []MemoryRecord, sessions []insightsdb.SessionRow, _ time.Time) []MemoryRecord {
	return DeriveOutcomesFromEvidence(records, []string{buildOutcomeSignalText(sessions)}, time.Time{})
}

func DeriveOutcomesFromEvidence(records []MemoryRecord, evidence []string, _ time.Time) []MemoryRecord {
	updated := append([]MemoryRecord(nil), records...)
	signalTokens := tokenize(strings.Join(evidence, " "))

	for i := range updated {
		if updated[i].Origin == OriginManual {
			updated[i].Outcome = OutcomeNeutral
			continue
		}

		recordTokens := tokenize(strings.Join([]string{updated[i].Title, updated[i].Body, updated[i].Why}, " "))
		overlap := 0
		for token := range recordTokens {
			if _, ok := signalTokens[token]; ok {
				overlap++
			}
		}

		switch {
		case overlap == 0:
			updated[i].Outcome = OutcomeNeutral
		case updated[i].InjectCount > 0:
			updated[i].Outcome = OutcomeIneffective
		default:
			updated[i].Outcome = OutcomeReinforced
		}
	}

	return updated
}

func PruneRecords(records []MemoryRecord, now time.Time) ([]MemoryRecord, PruneResult) {
	updated := append([]MemoryRecord(nil), records...)
	result := PruneResult{}

	for i := range updated {
		if updated[i].Origin == OriginManual {
			continue
		}

		reason := pruneReason(updated[i], now)
		if reason == "" {
			continue
		}

		updated[i].UpdatedAt = now
		if shouldDemoteRecord(updated[i], reason) {
			updated[i].Status = StatusCandidate
			updated[i].LastReviewedAt = now
			updated[i].History = append(updated[i].History, HistoryEvent{
				Type:   "demoted",
				At:     now,
				Detail: reason,
			})
			result.DemotedCount++
			continue
		}

		updated[i].Status = StatusArchived
		updated[i].History = append(updated[i].History, HistoryEvent{
			Type:   "pruned",
			At:     now,
			Detail: reason,
		})
		result.ArchivedCount++
		result.PrunedCount++
	}

	return updated, result
}

func shouldDemoteRecord(record MemoryRecord, reason string) bool {
	return reason == "ineffective_active" && record.Status == StatusActive
}

func transitionRecord(record MemoryRecord, action LifecycleAction, now time.Time) (MemoryRecord, error) {
	nextStatus, err := nextLifecycleStatus(record, action)
	if err != nil {
		return record, err
	}

	record.Status = nextStatus
	record.UpdatedAt = now
	record.LastReviewedAt = now
	record.History = append(record.History, HistoryEvent{
		Type: lifecycleHistoryType(action),
		At:   now,
	})
	return record, nil
}

func nextLifecycleStatus(record MemoryRecord, action LifecycleAction) (Status, error) {
	switch action {
	case LifecycleActionActivate:
		if record.ScopeKind == ScopeKindRepo {
			return record.Status, fmt.Errorf("repo-scoped memory %q requires promote, not activate", record.ID)
		}
		if record.Status == StatusSuppressed {
			return record.Status, fmt.Errorf("memory %q is suppressed; use unsuppress first", record.ID)
		}
		if record.Status == StatusArchived {
			return record.Status, fmt.Errorf("memory %q is archived", record.ID)
		}
		return StatusActive, nil
	case LifecycleActionPromote:
		if record.ScopeKind != ScopeKindRepo {
			return record.Status, fmt.Errorf("memory %q is not repo-scoped", record.ID)
		}
		if record.Status == StatusSuppressed {
			return record.Status, fmt.Errorf("memory %q is suppressed; use unsuppress first", record.ID)
		}
		if record.Status == StatusArchived {
			return record.Status, fmt.Errorf("memory %q is archived", record.ID)
		}
		return StatusActive, nil
	case LifecycleActionSuppress:
		if record.Status == StatusArchived {
			return record.Status, fmt.Errorf("memory %q is archived", record.ID)
		}
		return StatusSuppressed, nil
	case LifecycleActionUnsuppress:
		if record.Status != StatusSuppressed {
			return record.Status, fmt.Errorf("memory %q is not suppressed", record.ID)
		}
		return StatusCandidate, nil
	case LifecycleActionArchive:
		return StatusArchived, nil
	default:
		return record.Status, fmt.Errorf("unsupported lifecycle action: %s", action)
	}
}

func lifecycleHistoryType(action LifecycleAction) string {
	switch action {
	case LifecycleActionActivate:
		return "activated"
	case LifecycleActionPromote:
		return "promoted"
	case LifecycleActionSuppress:
		return "suppressed"
	case LifecycleActionUnsuppress:
		return "unsuppressed"
	case LifecycleActionArchive:
		return "archived"
	default:
		return string(action)
	}
}

func indexRecordScopeKeys(records []MemoryRecord) map[string]int {
	byScopeKey := make(map[string]int, len(records))
	for i := range records {
		fingerprint := records[i].Fingerprint
		if fingerprint == "" {
			fingerprint = FingerprintForRecord(records[i].Kind, records[i].Title, records[i].Body)
			records[i].Fingerprint = fingerprint
		}
		byScopeKey[recordScopeKey(fingerprint, records[i].ScopeKind, records[i].ScopeValue)] = i
	}
	return byScopeKey
}

func findExactRecordScopeIndex(records []MemoryRecord, record MemoryRecord) (int, bool) {
	scopeKeys := indexRecordScopeKeys(records)
	idx, exists := scopeKeys[recordScopeKey(record.Fingerprint, record.ScopeKind, record.ScopeValue)]
	return idx, exists
}

func adoptedRecordID(source MemoryRecord, scopeKind ScopeKind, scopeValue string) string {
	fingerprint := source.Fingerprint
	if fingerprint == "" {
		fingerprint = FingerprintForRecord(source.Kind, source.Title, source.Body)
	}
	scope := scopeToken(scopeValue)
	return fmt.Sprintf("%s--%s-%s", fingerprint, scopeKind, scope)
}

func scopeToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

func buildOutcomeSignalText(sessions []insightsdb.SessionRow) string {
	parts := make([]string, 0, len(sessions)*4)
	for _, session := range sessions {
		parts = append(parts, session.Friction...)
		for _, learning := range session.Learnings {
			parts = append(parts, learning.Finding)
		}
		parts = append(parts, session.Facets.RepoGotchas...)
		parts = append(parts, session.Facets.WorkflowGaps...)
		for _, item := range session.Facets.MissingContext {
			parts = append(parts, item.Item)
		}
		for _, item := range session.Facets.RepeatedUserInstructions {
			parts = append(parts, item.Instruction)
		}
		for _, item := range session.Facets.FailureLoops {
			parts = append(parts, item.Description)
		}
		for _, item := range session.Facets.SkillSignals {
			parts = append(parts, item.Friction...)
			if item.MissingInstruction != "" {
				parts = append(parts, item.MissingInstruction)
			}
		}
	}
	return strings.Join(parts, " ")
}

func pruneReason(record MemoryRecord, now time.Time) string {
	if record.Status == StatusArchived {
		return ""
	}

	lastActivity := record.UpdatedAt
	for _, candidate := range []time.Time{
		record.CreatedAt,
		record.LastMatchedAt,
		record.LastInjectedAt,
		record.LastReviewedAt,
	} {
		if candidate.After(lastActivity) {
			lastActivity = candidate
		}
	}

	switch {
	case record.Status == StatusCandidate && !lastActivity.IsZero() && now.Sub(lastActivity) >= 30*24*time.Hour:
		return "stale_candidate"
	case record.Status == StatusActive && record.MatchCount == 0 && !lastActivity.IsZero() && now.Sub(lastActivity) >= 60*24*time.Hour:
		return "stale_unmatched_active"
	case record.Status == StatusActive && record.Outcome == OutcomeIneffective && record.InjectCount >= 3:
		return "ineffective_active"
	default:
		return ""
	}
}

func normalizeState(state *State) {
	normalizeStateWithSource(state, false)
}

func normalizeStateWithSource(state *State, loadedFromLegacySnapshot bool) {
	if state == nil {
		return
	}
	loadedFromStoreOnly := state.Store != nil && state.Snapshot == nil
	loadedFromLegacySnapshot = loadedFromLegacySnapshot || (state.Store == nil && state.Snapshot != nil)
	switch {
	case state.Store == nil && state.Snapshot != nil:
		state.Store = state.Snapshot
	case state.Store != nil && state.Snapshot == nil:
		state.Snapshot = state.Store
	}
	if state.Store == nil {
		return
	}

	if state.Store.Version == 0 {
		state.Store.Version = 1
	}
	if state.Store.MaxInjected <= 0 {
		state.Store.MaxInjected = DefaultMaxInjected
	}
	if state.Store.SourceWindow <= 0 {
		state.Store.SourceWindow = DefaultRefreshWindow
	}
	if state.Store.Mode == "" {
		if state.Store.InjectionEnabled {
			state.Store.Mode = ModeAuto
		} else {
			state.Store.Mode = ModeManual
		}
	}
	state.Store.InjectionEnabled = state.Store.Mode == ModeAuto
	if state.Store.ActivationPolicy == "" {
		state.Store.ActivationPolicy = ActivationPolicyReview
	}
	for i := range state.Store.Records {
		record := &state.Store.Records[i]
		inferred := false
		if record.Status == "" {
			record.Status = StatusActive
			inferred = true
		}
		if record.Origin == "" {
			record.Origin = OriginGenerated
			inferred = true
		}
		if record.ScopeKind == "" {
			record.ScopeKind = ScopeKindMe
			inferred = true
		}
		if record.Outcome == "" {
			record.Outcome = OutcomeNeutral
			inferred = true
		}
		if record.Confidence == "" {
			if record.Origin == OriginManual || record.Status == StatusActive || loadedFromLegacySnapshot {
				record.Confidence = "high"
			} else {
				record.Confidence = "medium"
			}
			inferred = true
		}
		if record.Strength == 0 {
			if record.Origin == OriginManual {
				record.Strength = 4
			} else {
				record.Strength = 3
			}
			inferred = true
		}
		if record.Fingerprint == "" {
			record.Fingerprint = FingerprintForRecord(record.Kind, record.Title, record.Body)
		}
		if (loadedFromLegacySnapshot || loadedFromStoreOnly) && inferred {
			record.LegacyInferred = true
		}
	}
	state.Snapshot = state.Store
}

type diskState struct {
	Store         *Store         `json:"store,omitempty"`
	Snapshot      *Snapshot      `json:"snapshot,omitempty"`
	InjectionLogs []InjectionLog `json:"injection_logs,omitempty"`
}

func buildScoredCandidate(record MemoryRecord, primaryOverlap int, now time.Time) scoredCandidate {
	baseScore := scoreRecord(record, primaryOverlap, now)
	outcomeBonus := outcomeBonus(record.Outcome)
	scopeBonus := scopeBonus(record)
	cooldownPenalty := cooldownPenalty(record.LastInjectedAt, now)
	adjustedScore := baseScore + outcomeBonus + scopeBonus - cooldownPenalty

	return scoredCandidate{
		record:          record,
		baseScore:       baseScore,
		outcomeBonus:    outcomeBonus,
		scopeBonus:      scopeBonus,
		cooldownPenalty: cooldownPenalty,
		adjustedScore:   adjustedScore,
		reason:          "title/body overlap",
		rationale: SelectionRationale{
			BaseScore:       baseScore,
			OutcomeBonus:    outcomeBonus,
			ScopeBonus:      scopeBonus,
			CooldownPenalty: cooldownPenalty,
			AdjustedScore:   adjustedScore,
		},
	}
}

func packSelectedCandidates(candidates []scoredCandidate, maxInjected int) []Match {
	if maxInjected <= 0 || len(candidates) == 0 {
		return nil
	}

	selected := make([]Match, 0, minInt(len(candidates), maxInjected))
	seenFingerprints := make(map[string]struct{}, len(candidates))
	seenTopics := make(map[string]struct{}, len(candidates))
	selectedBytes := len("Memory For This Repo\n")

	appendCandidate := func(candidate scoredCandidate) {
		if len(selected) >= maxInjected {
			return
		}

		fingerprint := candidate.record.Fingerprint
		if fingerprint == "" {
			fingerprint = FingerprintForRecord(candidate.record.Kind, candidate.record.Title, candidate.record.Body)
		}
		if _, ok := seenFingerprints[fingerprint]; ok {
			return
		}

		match := Match{
			Record: candidate.record,
			Score:  candidate.adjustedScore,
			Reason: candidate.reason,
			Rationale: SelectionRationale{
				BaseScore:       candidate.baseScore,
				OutcomeBonus:    candidate.outcomeBonus,
				ScopeBonus:      candidate.scopeBonus,
				CooldownPenalty: candidate.cooldownPenalty,
				AdjustedScore:   candidate.adjustedScore,
			},
		}
		lineBytes := len(formatInjectionLine(match))
		if selectedBytes+lineBytes > maxInjectionBytes {
			return
		}

		selected = append(selected, match)
		seenFingerprints[fingerprint] = struct{}{}
		seenTopics[candidateTopicKey(candidate.record)] = struct{}{}
		selectedBytes += lineBytes + 1
	}

	for _, candidate := range candidates {
		if _, ok := seenTopics[candidateTopicKey(candidate.record)]; ok {
			continue
		}
		appendCandidate(candidate)
	}

	for _, candidate := range candidates {
		appendCandidate(candidate)
	}

	return selected
}

func explainSkippedCandidates(candidates []scoredCandidate, selected []Match) []SkippedMatch {
	if len(candidates) == 0 {
		return nil
	}

	selectedIDs := make(map[string]struct{}, len(selected))
	selectedTopics := make(map[string]struct{}, len(selected))
	selectedPersonal := false
	selectedBytes := len("Memory For This Repo\n")
	for _, match := range selected {
		selectedIDs[match.Record.ID] = struct{}{}
		selectedTopics[candidateTopicKey(match.Record)] = struct{}{}
		selectedBytes += len(formatInjectionLine(match)) + 1
		if match.Record.ScopeKind == ScopeKindMe && (!match.Record.LegacyInferred || match.Record.ScopeValue != "") {
			selectedPersonal = true
		}
	}

	skipped := make([]SkippedMatch, 0, len(candidates))
	for _, candidate := range candidates {
		if _, ok := selectedIDs[candidate.record.ID]; ok {
			continue
		}
		reason := "skipped by byte budget"
		switch {
		case candidate.cooldownPenalty > 0:
			reason = "skipped by cooldown"
		case candidate.record.ScopeKind == ScopeKindRepo && selectedPersonal:
			reason = "skipped by scope preference"
		case hasSelectedTopicDuplicate(candidate.record, selectedTopics):
			reason = "skipped by diversity quota"
		case selectedBytes+len(formatInjectionLine(Match{Record: candidate.record})) > maxInjectionBytes:
			reason = "skipped by byte budget"
		}
		skipped = append(skipped, SkippedMatch{
			Record: candidate.record,
			Reason: reason,
			Rationale: SelectionRationale{
				BaseScore:       candidate.baseScore,
				OutcomeBonus:    candidate.outcomeBonus,
				ScopeBonus:      candidate.scopeBonus,
				CooldownPenalty: candidate.cooldownPenalty,
				AdjustedScore:   candidate.adjustedScore,
			},
		})
	}

	if len(skipped) == 0 {
		return nil
	}

	legendReasons := []string{
		"skipped by cooldown",
		"skipped by scope preference",
		"skipped by diversity quota",
		"skipped by byte budget",
	}
	seen := make(map[string]struct{}, len(skipped))
	for _, item := range skipped {
		seen[item.Reason] = struct{}{}
	}
	for _, reason := range legendReasons {
		if _, ok := seen[reason]; ok {
			continue
		}
		skipped = append(skipped, SkippedMatch{Reason: reason})
	}
	return skipped
}

func hasSelectedTopicDuplicate(record MemoryRecord, selectedTopics map[string]struct{}) bool {
	_, ok := selectedTopics[candidateTopicKey(record)]
	return ok
}

func scoreRecord(record MemoryRecord, primaryOverlap int, now time.Time) int {
	if primaryOverlap == 0 {
		return 0
	}

	score := primaryOverlap * 7
	switch record.Kind {
	case KindRepoRule, KindAgentInstruction:
		score += 15
	case KindSkillPatch:
		score += 4
	case KindWorkflowRule, KindAntiPattern, "":
	}
	score += minInt(record.Strength, 5)

	if !record.UpdatedAt.IsZero() && now.After(record.UpdatedAt) {
		age := now.Sub(record.UpdatedAt)
		if age <= 14*24*time.Hour {
			score += 2
		}
	}

	return score
}

func candidateTopicKey(record MemoryRecord) string {
	return strings.Join([]string{
		string(record.Kind),
		strings.ToLower(strings.TrimSpace(record.Body)),
	}, "|")
}

func formatInjectionLine(match Match) string {
	var buf strings.Builder
	buf.WriteString("- ")
	buf.WriteString(strings.TrimSpace(match.Record.Title))
	if body := strings.TrimSpace(match.Record.Body); body != "" {
		buf.WriteString(": ")
		buf.WriteString(body)
	}
	return buf.String()
}

func outcomeBonus(outcome Outcome) int {
	switch outcome {
	case OutcomeReinforced:
		return 3
	case OutcomeNeutral:
		return 0
	case OutcomeIneffective:
		return -3
	default:
		return 0
	}
}

func scopeBonus(record MemoryRecord) int {
	if record.ScopeKind == ScopeKindMe && (!record.LegacyInferred || record.ScopeValue != "") {
		return 1
	}
	return 0
}

func cooldownPenalty(lastInjectedAt time.Time, now time.Time) int {
	if lastInjectedAt.IsZero() || now.Before(lastInjectedAt) {
		return 0
	}
	age := now.Sub(lastInjectedAt)
	switch {
	case age <= 30*time.Minute:
		return 4
	case age <= 2*time.Hour:
		return 3
	case age <= 24*time.Hour:
		return 2
	default:
		return 0
	}
}

func buildRecordMatchSignals(record MemoryRecord) recordMatchSignals {
	return recordMatchSignals{
		primaryTokens: tokenize(strings.Join([]string{
			record.Title,
			record.Body,
		}, " ")),
	}
}

func tokenOverlap(promptTokens, recordTokens map[string]struct{}) int {
	overlap := 0
	for token := range promptTokens {
		if _, ok := recordTokens[token]; ok {
			overlap++
		}
	}
	return overlap
}

func injectionRejectionReason(record MemoryRecord, primaryOverlap int) (string, bool) {
	if record.Status != "" && record.Status != StatusActive {
		return "not active", true
	}
	if strings.EqualFold(record.Confidence, "low") {
		return "low confidence", true
	}
	if record.Strength < 3 {
		return "strength below injection threshold", true
	}
	if primaryOverlap >= 2 {
		return "", false
	}
	if primaryOverlap == 1 &&
		(record.Kind == KindRepoRule || record.Kind == KindAgentInstruction) &&
		strings.EqualFold(record.Confidence, "high") &&
		record.Strength >= 5 {
		return "", false
	}
	if primaryOverlap == 1 {
		return "single-token overlap below injection threshold", true
	}
	return "", true
}

func tokenize(text string) map[string]struct{} {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	tokens := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if len(field) < 3 {
			continue
		}
		if _, ok := stopWords[field]; ok {
			continue
		}
		tokens[field] = struct{}{}
	}
	return tokens
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func FingerprintForRecord(kind Kind, title, body string) string {
	base := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		string(kind),
		strings.TrimSpace(title),
		strings.TrimSpace(body),
	}, "|")))
	if base == "" {
		return "memory"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "memory"
	}
	return out
}
