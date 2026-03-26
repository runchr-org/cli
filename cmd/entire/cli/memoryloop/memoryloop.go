package memoryloop

import (
	"bytes"
	"context"
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
	DefaultMaxInjected   = 3
	DefaultRefreshWindow = 20
	maxInjectionLogs     = 50
	maxInjectionBytes    = 1800
)

var stopWords = map[string]struct{}{
	"and":    {},
	"before": {},
	"from":   {},
	"into":   {},
	"that":   {},
	"the":    {},
	"this":   {},
	"use":    {},
	"with":   {},
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
	ScopeKindMe   ScopeKind = "me"
	ScopeKindRepo ScopeKind = "repo"
)

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
	At             time.Time `json:"at"`
	Scope          string    `json:"scope,omitempty"`
	ScopeValue     string    `json:"scope_value,omitempty"`
	SourceWindow   int       `json:"source_window,omitempty"`
	GeneratedCount int       `json:"generated_count,omitempty"`
	ActivatedCount int       `json:"activated_count,omitempty"`
	CandidateCount int       `json:"candidate_count,omitempty"`
}

type Store struct {
	Version          int              `json:"version"`
	GeneratedAt      time.Time        `json:"generated_at"`
	SourceWindow     int              `json:"source_window"`
	Scope            string           `json:"scope,omitempty"`
	ScopeValue       string           `json:"scope_value,omitempty"`
	Records          []MemoryRecord   `json:"records,omitempty"`
	Mode             Mode             `json:"mode,omitempty"`
	ActivationPolicy ActivationPolicy `json:"activation_policy,omitempty"`
	InjectionEnabled bool             `json:"injection_enabled"`
	MaxInjected      int              `json:"max_injected"`
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
	Record MemoryRecord
	Score  int
	Reason string
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

type PruneResult struct {
	ArchivedCount int
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

func SelectRelevant(snapshot Snapshot, prompt string, now time.Time) []Match {
	maxInjected := snapshot.MaxInjected
	if maxInjected <= 0 {
		maxInjected = DefaultMaxInjected
	}

	promptTokens := tokenize(prompt)
	if len(promptTokens) == 0 {
		return nil
	}

	matches := make([]Match, 0, len(snapshot.Records))
	for _, record := range snapshot.Records {
		if record.Status != "" && record.Status != StatusActive {
			continue
		}
		score, reason := scoreRecord(record, promptTokens, now)
		if score <= 0 {
			continue
		}
		matches = append(matches, Match{
			Record: record,
			Score:  score,
			Reason: reason,
		})
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		if matches[i].Record.Strength != matches[j].Record.Strength {
			return matches[i].Record.Strength > matches[j].Record.Strength
		}
		return matches[i].Record.Title < matches[j].Record.Title
	})

	if len(matches) > maxInjected {
		matches = matches[:maxInjected]
	}
	return matches
}

func ReconcileGeneratedRecords(existing, generated []MemoryRecord, scopeKind ScopeKind, scopeValue string, policy ActivationPolicy, now time.Time) ReconcileResult {
	records := append([]MemoryRecord(nil), existing...)
	byScopeKey := make(map[string]int, len(records))
	for i := range records {
		fingerprint := records[i].Fingerprint
		if fingerprint == "" {
			fingerprint = fingerprintForRecord(records[i].Kind, records[i].Title, records[i].Body)
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
			record.Fingerprint = fingerprintForRecord(record.Kind, record.Title, record.Body)
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
	buf.WriteString("Memory For This Repo\n")
	for _, match := range matches {
		buf.WriteString("- ")
		buf.WriteString(strings.TrimSpace(match.Record.Title))
		if body := strings.TrimSpace(match.Record.Body); body != "" {
			buf.WriteString(": ")
			buf.WriteString(body)
		}
		if why := strings.TrimSpace(match.Record.Why); why != "" {
			buf.WriteString(" Why: ")
			buf.WriteString(why)
		}
		buf.WriteByte('\n')
		if buf.Len() >= maxInjectionBytes {
			break
		}
	}

	out := strings.TrimSpace(buf.String())
	if len(out) > maxInjectionBytes {
		out = out[:maxInjectionBytes]
	}
	return out
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
		ID:          makeRecordID(input.Kind, input.Title),
		Kind:        input.Kind,
		Title:       strings.TrimSpace(input.Title),
		Body:        strings.TrimSpace(input.Body),
		Fingerprint: fingerprintForRecord(input.Kind, input.Title, input.Body),
		ScopeKind:   input.ScopeKind,
		ScopeValue:  strings.TrimSpace(input.ScopeValue),
		Origin:      OriginManual,
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

		updated[i].Status = StatusArchived
		updated[i].UpdatedAt = now
		updated[i].History = append(updated[i].History, HistoryEvent{
			Type:   "pruned",
			At:     now,
			Detail: reason,
		})
		result.ArchivedCount++
	}

	return updated, result
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
			fingerprint = fingerprintForRecord(records[i].Kind, records[i].Title, records[i].Body)
			records[i].Fingerprint = fingerprint
		}
		byScopeKey[recordScopeKey(fingerprint, records[i].ScopeKind, records[i].ScopeValue)] = i
	}
	return byScopeKey
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
		if record.Fingerprint == "" {
			record.Fingerprint = fingerprintForRecord(record.Kind, record.Title, record.Body)
		}
		if loadedFromLegacySnapshot && inferred {
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

func scoreRecord(record MemoryRecord, promptTokens map[string]struct{}, now time.Time) (int, string) {
	recordTokens := tokenize(strings.Join([]string{
		record.Title,
		record.Body,
		record.Why,
		strings.Join(record.Evidence, " "),
	}, " "))
	if len(recordTokens) == 0 {
		return 0, ""
	}

	overlap := 0
	for token := range promptTokens {
		if _, ok := recordTokens[token]; ok {
			overlap++
		}
	}
	if overlap == 0 {
		return 0, ""
	}

	score := overlap * 10
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

	return score, "keyword overlap"
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

func fingerprintForRecord(kind Kind, title, body string) string {
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
