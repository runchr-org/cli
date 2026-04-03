# Memory Loop: Edit, Keywords, and Generation Threshold — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add memory editing, keyword-based phrase matching for injection scoring, and configurable generation threshold presets to the memory-loop system.

**Architecture:** Three independent feature slices that share the `MemoryRecord` type. Keywords add a `[]string` field and phrase-based substring matching to injection scoring. Editing adds an `EditRecord()` function and TUI wizard stage. Thresholds replace hardcoded generator filters with a configurable preset system resolved from settings. All features are additive — no breaking changes to existing data.

**Tech Stack:** Go, Bubble Tea (TUI), charmbracelet/huh (forms), cobra (CLI), JSON file store

**Spec:** `docs/superpowers/specs/2026-04-03-memory-loop-edit-keywords-threshold-design.md`

---

## File Structure

### New Files
- None — all changes are modifications to existing files

### Modified Files
| File | Responsibility |
|---|---|
| `cmd/entire/cli/memoryloop/memoryloop.go` | `Keywords` field, `EditRecordInput`, `EditRecord()`, `ParseKeywords()`, `countMatchedKeywords()`, updated `recordMatchSignals`, `buildRecordMatchSignals()`, `scoreRecord()`, `buildScoredCandidate()`, `injectionRejectionReason()`, `ManualRecordInput`, `AddManualRecord()`, `ReconcileGeneratedRecords()` |
| `cmd/entire/cli/memoryloop/memoryloop_test.go` | Tests for keywords, editing, scoring, reconciliation |
| `cmd/entire/cli/memoryloop/generator.go` | `GenerationThresholdConfig`, `ResolveGenerationThreshold()`, updated `GenerateInput`, `buildGeneratedRecordsDetailed()`, `passesEvidenceGate()` |
| `cmd/entire/cli/memoryloop/generator_test.go` | Tests for threshold presets, evidence gate with configurable params (new file) |
| `cmd/entire/cli/settings/settings.go` | `GenerationThreshold`, `GenerationOverrides`, `GenerationThresholdOverrides` on `MemoryLoopSettings`; `GenerationThreshold`, `SingletonPolicy` on `MemoryLoopConfig` |
| `cmd/entire/cli/memorylooptui/wizard.go` | `WizardIntentEdit`, edit stages, `editInEditor()` |
| `cmd/entire/cli/memorylooptui/messages.go` | `WizardIntentEdit`, `editMemoryMsg` |
| `cmd/entire/cli/memorylooptui/detail_page.go` | Keywords row in content card |
| `cmd/entire/cli/memorylooptui/tab_settings.go` | Threshold card, override indicator |
| `cmd/entire/cli/memorylooptui/keys.go` | Threshold cycling keybinding |
| `cmd/entire/cli/memory_loop_cmd.go` | `edit` and `threshold` subcommands |

---

### Task 1: `ParseKeywords()` and `countMatchedKeywords()`

**Files:**
- Modify: `cmd/entire/cli/memoryloop/memoryloop.go:1593` (after `tokenize()`)
- Test: `cmd/entire/cli/memoryloop/memoryloop_test.go`

- [ ] **Step 1: Write failing tests for `ParseKeywords()`**

```go
func TestParseKeywords(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{name: "simple", input: "go test, deploy", expected: []string{"go test", "deploy"}},
		{name: "trims whitespace", input: "  go test , deploy  ", expected: []string{"go test", "deploy"}},
		{name: "drops empty", input: "go test,,deploy,", expected: []string{"go test", "deploy"}},
		{name: "dedup case insensitive", input: "Go Test, go test, DEPLOY", expected: []string{"Go Test", "DEPLOY"}},
		{name: "drops short", input: "a, go test, b", expected: []string{"go test"}},
		{name: "max 10", input: "a1,a2,a3,a4,a5,a6,a7,a8,a9,a10,a11,a12", expected: []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9", "a10"}},
		{name: "empty string", input: "", expected: nil},
		{name: "all short", input: "a, b, c", expected: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := ParseKeywords(tc.input)
			require.Equal(t, tc.expected, result)
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd cmd/entire/cli/memoryloop && go test -run TestParseKeywords -v`
Expected: FAIL — `ParseKeywords` undefined

- [ ] **Step 3: Implement `ParseKeywords()`**

Add after `tokenize()` function in `memoryloop.go` (~line 1593):

```go
const MaxKeywordsPerRecord = 10
const MinKeywordLength = 2

// ParseKeywords parses a comma-separated keyword string into a deduplicated,
// trimmed slice. Drops entries shorter than MinKeywordLength. Caps at MaxKeywordsPerRecord.
func ParseKeywords(input string) []string {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	parts := strings.Split(input, ",")
	seen := make(map[string]struct{}, len(parts))
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		kw := strings.TrimSpace(part)
		if len(kw) < MinKeywordLength {
			continue
		}
		lower := strings.ToLower(kw)
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		result = append(result, kw)
		if len(result) >= MaxKeywordsPerRecord {
			break
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd cmd/entire/cli/memoryloop && go test -run TestParseKeywords -v`
Expected: PASS

- [ ] **Step 5: Write failing tests for `countMatchedKeywords()`**

```go
func TestCountMatchedKeywords(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		prompt   string
		phrases  []string
		expected int
	}{
		{name: "exact phrase match", prompt: "run go test -v", phrases: []string{"go test"}, expected: 1},
		{name: "no match wrong order", prompt: "test the go module", phrases: []string{"go test"}, expected: 0},
		{name: "case insensitive", prompt: "Run Go Test", phrases: []string{"go test"}, expected: 1},
		{name: "multiple matches", prompt: "run go test and deploy", phrases: []string{"go test", "deploy"}, expected: 2},
		{name: "partial no match", prompt: "pytest coverage", phrases: []string{"go test"}, expected: 0},
		{name: "overlapping phrases", prompt: "run go test now", phrases: []string{"go", "go test"}, expected: 2},
		{name: "empty phrases", prompt: "anything", phrases: nil, expected: 0},
		{name: "empty prompt", prompt: "", phrases: []string{"go test"}, expected: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := countMatchedKeywords(strings.ToLower(tc.prompt), tc.phrases)
			require.Equal(t, tc.expected, result)
		})
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `cd cmd/entire/cli/memoryloop && go test -run TestCountMatchedKeywords -v`
Expected: FAIL — `countMatchedKeywords` undefined

- [ ] **Step 7: Implement `countMatchedKeywords()`**

Add after `ParseKeywords()` in `memoryloop.go`:

```go
func countMatchedKeywords(promptLower string, phrases []string) int {
	count := 0
	for _, phrase := range phrases {
		if strings.Contains(promptLower, phrase) {
			count++
		}
	}
	return count
}
```

- [ ] **Step 8: Run test to verify it passes**

Run: `cd cmd/entire/cli/memoryloop && go test -run TestCountMatchedKeywords -v`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add cmd/entire/cli/memoryloop/memoryloop.go cmd/entire/cli/memoryloop/memoryloop_test.go
git commit -m "feat(memoryloop): add ParseKeywords and countMatchedKeywords"
```

---

### Task 2: Add `Keywords` field to `MemoryRecord` and update scoring

**Files:**
- Modify: `cmd/entire/cli/memoryloop/memoryloop.go:159-186` (MemoryRecord), `304-306` (recordMatchSignals), `1533-1540` (buildRecordMatchSignals), `1449-1472` (scoreRecord), `1293-1316` (buildScoredCandidate)
- Test: `cmd/entire/cli/memoryloop/memoryloop_test.go`

- [ ] **Step 1: Add `Keywords` field to `MemoryRecord`**

In `memoryloop.go`, add after `LegacyInferred` field (line 186):

```go
type MemoryRecord struct {
	// ... existing fields through LegacyInferred ...
	LegacyInferred   bool           `json:"legacy_inferred,omitempty"`
	Keywords         []string       `json:"keywords,omitempty"`
}
```

- [ ] **Step 2: Update `recordMatchSignals` and `buildRecordMatchSignals()`**

Update the struct at line 304:

```go
type recordMatchSignals struct {
	primaryTokens  map[string]struct{}
	keywordPhrases []string
}
```

Update `buildRecordMatchSignals()` at line 1533:

```go
func buildRecordMatchSignals(record MemoryRecord) recordMatchSignals {
	phrases := make([]string, 0, len(record.Keywords))
	for _, kw := range record.Keywords {
		kw = strings.TrimSpace(strings.ToLower(kw))
		if kw != "" {
			phrases = append(phrases, kw)
		}
	}
	return recordMatchSignals{
		primaryTokens: tokenize(strings.Join([]string{
			record.Title,
			record.Body,
		}, " ")),
		keywordPhrases: phrases,
	}
}
```

- [ ] **Step 3: Update `scoreRecord()` to include keyword boost**

Update `scoreRecord()` at line 1449. Add `keywordOverlap int` parameter:

```go
func scoreRecord(record MemoryRecord, primaryOverlap, keywordOverlap int, now time.Time) int {
	if primaryOverlap == 0 && keywordOverlap == 0 {
		return 0
	}

	score := primaryOverlap*7 + keywordOverlap*21
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
```

- [ ] **Step 4: Update `buildScoredCandidate()` to pass keyword overlap**

Find every call site of `scoreRecord` and `buildScoredCandidate`. Update `buildScoredCandidate` at line 1293 to accept and use `keywordOverlap`:

```go
func buildScoredCandidate(record MemoryRecord, primaryOverlap, keywordOverlap int, now time.Time) scoredCandidate {
	baseScore := scoreRecord(record, primaryOverlap, keywordOverlap, now)
	outcomeBonus := outcomeBonus(record.Outcome)
	scopeBonus := scopeBonus(record)
	cooldownPenalty := cooldownPenalty(record.LastInjectedAt, now)
	adjustedScore := baseScore + outcomeBonus + scopeBonus - cooldownPenalty

	reason := "title/body overlap"
	if keywordOverlap > 0 {
		reason = fmt.Sprintf("title/body overlap + %d keyword match(es)", keywordOverlap)
	}

	return scoredCandidate{
		record:          record,
		baseScore:       baseScore,
		outcomeBonus:    outcomeBonus,
		scopeBonus:      scopeBonus,
		cooldownPenalty: cooldownPenalty,
		adjustedScore:   adjustedScore,
		reason:          reason,
		rationale: SelectionRationale{
			BaseScore:       baseScore,
			OutcomeBonus:    outcomeBonus,
			ScopeBonus:      scopeBonus,
			CooldownPenalty: cooldownPenalty,
			AdjustedScore:   adjustedScore,
		},
	}
}
```

- [ ] **Step 5: Update all callers of `buildScoredCandidate` and `scoreRecord`**

Search for all call sites in `memoryloop.go`. The main caller is in the selection loop (around `PreviewSelection` / `SelectRelevant`). Update each call to compute `keywordOverlap` from `countMatchedKeywords(promptLower, signals.keywordPhrases)` and pass it through. The prompt needs to be lowercased once at the top of the selection function and threaded through.

Also update `injectionRejectionReason()` at line 1552 to accept keyword overlap so records with zero primary overlap but keyword matches aren't rejected:

```go
func injectionRejectionReason(record MemoryRecord, primaryOverlap, keywordOverlap int) (string, bool) {
	if record.Status != "" && record.Status != StatusActive {
		return "not active", true
	}
	if strings.EqualFold(record.Confidence, "low") {
		return "low confidence", true
	}
	if record.Strength < 3 {
		return "strength below injection threshold", true
	}
	totalOverlap := primaryOverlap + keywordOverlap
	if totalOverlap >= 2 {
		return "", false
	}
	if totalOverlap == 1 &&
		(record.Kind == KindRepoRule || record.Kind == KindAgentInstruction) &&
		strings.EqualFold(record.Confidence, "high") &&
		record.Strength >= 5 {
		return "", false
	}
	if totalOverlap == 1 {
		return "single-token overlap below injection threshold", true
	}
	return "", true
}
```

- [ ] **Step 6: Write tests for keyword scoring**

```go
func TestScoreRecord_KeywordBoost(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	record := MemoryRecord{
		Kind:      KindRepoRule,
		Title:     "Run lint",
		Body:      "Always run lint before committing",
		Strength:  4,
		UpdatedAt: now.Add(-1 * time.Hour),
	}

	scoreNoKeyword := scoreRecord(record, 2, 0, now)
	scoreWithKeyword := scoreRecord(record, 2, 1, now)

	require.Equal(t, scoreWithKeyword-scoreNoKeyword, 21, "one keyword match should add 21 to score")
}

func TestScoreRecord_KeywordOnlyNoBypass(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	record := MemoryRecord{
		Kind:     KindRepoRule,
		Title:    "Run lint",
		Body:     "Always run lint",
		Strength: 4,
	}

	score := scoreRecord(record, 0, 0, now)
	require.Equal(t, 0, score, "zero overlap with zero keywords should still be 0")
}
```

- [ ] **Step 7: Run tests**

Run: `cd cmd/entire/cli/memoryloop && go test -run TestScoreRecord -v`
Expected: PASS

- [ ] **Step 8: Run full test suite to check for compilation**

Run: `mise run test`
Expected: PASS (may need to fix callers that pass wrong arg count to updated functions)

- [ ] **Step 9: Commit**

```bash
git add cmd/entire/cli/memoryloop/memoryloop.go cmd/entire/cli/memoryloop/memoryloop_test.go
git commit -m "feat(memoryloop): add Keywords field and keyword boost to injection scoring"
```

---

### Task 3: Update `ManualRecordInput` and `AddManualRecord()` for keywords

**Files:**
- Modify: `cmd/entire/cli/memoryloop/memoryloop.go:282-289` (ManualRecordInput), `757-814` (AddManualRecord)
- Test: `cmd/entire/cli/memoryloop/memoryloop_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestAddManualRecord_WithKeywords(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	records, record, err := AddManualRecord(nil, ManualRecordInput{
		Kind:     KindRepoRule,
		Title:    "Test memory",
		Body:     "Always test",
		Keywords: []string{"go test", "testing"},
	}, now)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, []string{"go test", "testing"}, record.Keywords)
	require.Equal(t, FingerprintForRecord(KindRepoRule, "Test memory", "Always test"), record.Fingerprint,
		"keywords must not affect fingerprint")
}

func TestAddManualRecord_KeywordsCapped(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	keywords := make([]string, 15)
	for i := range keywords {
		keywords[i] = fmt.Sprintf("keyword-%02d", i)
	}
	_, record, err := AddManualRecord(nil, ManualRecordInput{
		Kind:     KindRepoRule,
		Title:    "Test",
		Body:     "Test body",
		Keywords: keywords,
	}, now)
	require.NoError(t, err)
	require.Len(t, record.Keywords, MaxKeywordsPerRecord)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd cmd/entire/cli/memoryloop && go test -run TestAddManualRecord_WithKeywords -v`
Expected: FAIL — `ManualRecordInput` has no `Keywords` field

- [ ] **Step 3: Add `Keywords` to `ManualRecordInput` and update `AddManualRecord()`**

Update `ManualRecordInput` at line 282:

```go
type ManualRecordInput struct {
	Kind       Kind
	Title      string
	Body       string
	Keywords   []string
	ScopeKind  ScopeKind
	ScopeValue string
	OwnerEmail string
}
```

In `AddManualRecord()`, after building the `record` struct (line 790), add:

```go
	record := MemoryRecord{
		// ... existing fields ...
		Keywords:    capKeywords(input.Keywords),
	}
```

Add the helper:

```go
func capKeywords(keywords []string) []string {
	if len(keywords) == 0 {
		return nil
	}
	if len(keywords) > MaxKeywordsPerRecord {
		keywords = keywords[:MaxKeywordsPerRecord]
	}
	return keywords
}
```

Also update the reconcile path inside `AddManualRecord()` (line 793-810) to copy keywords:

```go
	if idx, exists := findReconcileIndex(updated, indexRecordScopeKeys(updated), record); exists {
		existing := updated[idx]
		// ... existing field copies ...
		existing.Keywords = record.Keywords
		// ... rest of existing code ...
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd cmd/entire/cli/memoryloop && go test -run TestAddManualRecord_With -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/entire/cli/memoryloop/memoryloop.go cmd/entire/cli/memoryloop/memoryloop_test.go
git commit -m "feat(memoryloop): support keywords in ManualRecordInput and AddManualRecord"
```

---

### Task 4: Implement `EditRecord()`

**Files:**
- Modify: `cmd/entire/cli/memoryloop/memoryloop.go` (add after `AddManualRecord`, ~line 815)
- Test: `cmd/entire/cli/memoryloop/memoryloop_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestEditRecord_UpdateTitle(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	records := []MemoryRecord{{
		ID: "repo-rule-test", Kind: KindRepoRule, Title: "Old Title", Body: "Body",
		Status: StatusActive, Origin: OriginGenerated, Fingerprint: FingerprintForRecord(KindRepoRule, "Old Title", "Body"),
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	}}
	newTitle := "New Title"
	updated, record, err := EditRecord(records, "repo-rule-test", EditRecordInput{Title: &newTitle}, now)
	require.NoError(t, err)
	require.Equal(t, "New Title", record.Title)
	require.Equal(t, "Body", record.Body)
	require.Equal(t, FingerprintForRecord(KindRepoRule, "New Title", "Body"), record.Fingerprint)
	require.Equal(t, OriginGenerated, record.Origin, "origin must not change")
	require.Equal(t, now, record.UpdatedAt)
	require.Len(t, updated, 1)
	require.NotEmpty(t, record.History)
	lastEvent := record.History[len(record.History)-1]
	require.Equal(t, "edited", lastEvent.Type)
	require.Contains(t, lastEvent.Detail, "prev_title")
}

func TestEditRecord_UpdateKeywords(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	oldFP := FingerprintForRecord(KindRepoRule, "Title", "Body")
	records := []MemoryRecord{{
		ID: "repo-rule-test", Kind: KindRepoRule, Title: "Title", Body: "Body",
		Status: StatusActive, Fingerprint: oldFP,
	}}
	kw := []string{"go test", "deploy"}
	_, record, err := EditRecord(records, "repo-rule-test", EditRecordInput{Keywords: &kw}, now)
	require.NoError(t, err)
	require.Equal(t, []string{"go test", "deploy"}, record.Keywords)
	require.Equal(t, oldFP, record.Fingerprint, "keywords must not change fingerprint")
}

func TestEditRecord_EmptyTitleRejected(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	records := []MemoryRecord{{
		ID: "repo-rule-test", Kind: KindRepoRule, Title: "Title", Body: "Body", Status: StatusActive,
	}}
	empty := ""
	_, _, err := EditRecord(records, "repo-rule-test", EditRecordInput{Title: &empty}, now)
	require.Error(t, err)
	require.Contains(t, err.Error(), "title")
}

func TestEditRecord_ArchivedRejected(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	records := []MemoryRecord{{
		ID: "repo-rule-test", Kind: KindRepoRule, Title: "Title", Body: "Body", Status: StatusArchived,
	}}
	newTitle := "New"
	_, _, err := EditRecord(records, "repo-rule-test", EditRecordInput{Title: &newTitle}, now)
	require.Error(t, err)
	require.Contains(t, err.Error(), "archived")
}

func TestEditRecord_NilFieldsNoOp(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	records := []MemoryRecord{{
		ID: "repo-rule-test", Kind: KindRepoRule, Title: "Title", Body: "Body",
		Status: StatusActive, Fingerprint: "old-fp",
	}}
	updated, record, err := EditRecord(records, "repo-rule-test", EditRecordInput{}, now)
	require.NoError(t, err)
	require.Equal(t, "Title", record.Title)
	require.Equal(t, "old-fp", record.Fingerprint, "fingerprint unchanged when nothing edited")
	require.Len(t, updated, 1)
}

func TestEditRecord_ClearKeywords(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	records := []MemoryRecord{{
		ID: "repo-rule-test", Kind: KindRepoRule, Title: "Title", Body: "Body",
		Status: StatusActive, Keywords: []string{"old"},
	}}
	empty := []string{}
	_, record, err := EditRecord(records, "repo-rule-test", EditRecordInput{Keywords: &empty}, now)
	require.NoError(t, err)
	require.Empty(t, record.Keywords)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd cmd/entire/cli/memoryloop && go test -run TestEditRecord -v`
Expected: FAIL — `EditRecord` undefined

- [ ] **Step 3: Implement `EditRecordInput` and `EditRecord()`**

Add after `AddManualRecord()` in `memoryloop.go`:

```go
type EditRecordInput struct {
	Title    *string
	Body     *string
	Keywords *[]string
}

func EditRecord(records []MemoryRecord, id string, input EditRecordInput, now time.Time) ([]MemoryRecord, MemoryRecord, error) {
	updated := append([]MemoryRecord(nil), records...)
	for i := range updated {
		if updated[i].ID != id {
			continue
		}
		record := updated[i]

		if record.Status == StatusArchived {
			return records, MemoryRecord{}, fmt.Errorf("cannot edit archived memory: %s", id)
		}

		var changedFields []string
		detailMap := map[string]interface{}{}

		if input.Title != nil {
			newTitle := strings.TrimSpace(*input.Title)
			if newTitle == "" {
				return records, MemoryRecord{}, errors.New("memory title cannot be empty")
			}
			if newTitle != record.Title {
				detailMap["prev_title"] = record.Title
				changedFields = append(changedFields, "title")
				record.Title = newTitle
			}
		}

		if input.Body != nil {
			newBody := strings.TrimSpace(*input.Body)
			if newBody == "" {
				return records, MemoryRecord{}, errors.New("memory body cannot be empty")
			}
			if newBody != record.Body {
				detailMap["prev_body"] = record.Body
				changedFields = append(changedFields, "body")
				record.Body = newBody
			}
		}

		if input.Keywords != nil {
			kw := capKeywords(*input.Keywords)
			prevKW := strings.Join(record.Keywords, ",")
			newKW := strings.Join(kw, ",")
			if prevKW != newKW {
				detailMap["prev_keywords"] = prevKW
				changedFields = append(changedFields, "keywords")
				record.Keywords = kw
			}
		}

		if len(changedFields) == 0 {
			return updated, record, nil
		}

		// Recalculate fingerprint only if title or body changed (not keywords)
		for _, f := range changedFields {
			if f == "title" || f == "body" {
				record.Fingerprint = FingerprintForRecord(record.Kind, record.Title, record.Body)
				break
			}
		}

		record.UpdatedAt = now
		detailMap["fields"] = changedFields
		detailJSON, _ := json.Marshal(detailMap)
		record.History = append(record.History, HistoryEvent{
			Type:   "edited",
			At:     now,
			Detail: string(detailJSON),
		})

		updated[i] = record
		return updated, record, nil
	}

	return records, MemoryRecord{}, fmt.Errorf("memory not found: %s", id)
}
```

Ensure `"encoding/json"` is in the imports.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd cmd/entire/cli/memoryloop && go test -run TestEditRecord -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/entire/cli/memoryloop/memoryloop.go cmd/entire/cli/memoryloop/memoryloop_test.go
git commit -m "feat(memoryloop): implement EditRecord with keyword support and history capture"
```

---

### Task 5: Preserve keywords in `ReconcileGeneratedRecords()`

**Files:**
- Modify: `cmd/entire/cli/memoryloop/memoryloop.go:568-591` (reconcile loop)
- Test: `cmd/entire/cli/memoryloop/memoryloop_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestReconcileGeneratedRecords_PreservesKeywords(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	fp := FingerprintForRecord(KindRepoRule, "Run lint", "Always run lint")
	existing := []MemoryRecord{{
		ID: "repo-rule-run-lint", Kind: KindRepoRule, Title: "Run lint", Body: "Always run lint",
		Status: StatusActive, Fingerprint: fp, Keywords: []string{"lint", "go test"},
	}}
	generated := []MemoryRecord{{
		Kind: KindRepoRule, Title: "Run lint", Body: "Always run lint before commit",
		Fingerprint: fp, Confidence: "high", Strength: 4,
	}}
	result := ReconcileGeneratedRecords(existing, generated, ScopeKindRepo, "", ActivationPolicyReview, now)
	require.Len(t, result.Records, 1)
	require.Equal(t, []string{"lint", "go test"}, result.Records[0].Keywords, "user keywords must be preserved")
	require.Equal(t, "Always run lint before commit", result.Records[0].Body, "body should be updated by generator")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd cmd/entire/cli/memoryloop && go test -run TestReconcileGeneratedRecords_PreservesKeywords -v`
Expected: FAIL — keywords get overwritten (since the reconcile path copies all fields but not Keywords)

- [ ] **Step 3: Preserve Keywords in reconcile path**

In `ReconcileGeneratedRecords()`, after the field copy block (around line 569-582), add the keyword preservation. The reconcile block currently copies `Kind`, `Title`, `Body`, `Why`, `Evidence`, etc. After `reconciled.Origin = record.Origin` add:

```go
		// Preserve user-set keywords — they are not part of the generated data
		// and should not be overwritten by reconciliation.
		// (Keywords field is intentionally NOT copied from the generated record)
```

This is actually already correct by omission — the reconcile block only copies the fields it explicitly lists, and `Keywords` is not in that list. But to be safe and explicit, verify that no `reconciled.Keywords = record.Keywords` line exists in the reconcile block. If the reconcile block copies `Keywords`, remove that line.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd cmd/entire/cli/memoryloop && go test -run TestReconcileGeneratedRecords_PreservesKeywords -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/entire/cli/memoryloop/memoryloop.go cmd/entire/cli/memoryloop/memoryloop_test.go
git commit -m "feat(memoryloop): preserve user keywords during reconciliation"
```

---

### Task 6: Generation threshold types and `ResolveGenerationThreshold()`

**Files:**
- Modify: `cmd/entire/cli/memoryloop/generator.go` (add types and resolver after `GenerationStats`)
- Create: `cmd/entire/cli/memoryloop/generator_test.go` (new test file for threshold tests)

- [ ] **Step 1: Write failing tests**

Create `cmd/entire/cli/memoryloop/generator_test.go`:

```go
package memoryloop

import (
	"testing"

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
	// Verify behavioral equivalence with current hardcoded values:
	// - confidence == confidenceLow is rejected → MinConfidence "medium" with confidenceRank ordering
	require.True(t, confidenceRank(confidenceLow) < confidenceRank(cfg.MinConfidence),
		"balanced must filter low confidence via rank comparison")
	// - strength < 3 is rejected
	require.Equal(t, 3, cfg.MinStrength)
	// - evidence gate requires 2 sessions
	require.Equal(t, 2, cfg.EvidenceSessions)
	// - generic filter is on
	require.True(t, cfg.GenericFilter)
	// - singleton allowed for review-rules only
	require.Equal(t, "review-rules", cfg.SingletonPolicy)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd cmd/entire/cli/memoryloop && go test -run TestResolveGenerationThreshold -v`
Expected: FAIL — types/functions undefined

- [ ] **Step 3: Implement types and `ResolveGenerationThreshold()`**

Add in `generator.go` after `GenerationStats` (~line 132):

```go
type GenerationThresholdConfig struct {
	MinStrength      int    `json:"min_strength"`
	MinConfidence    string `json:"min_confidence"`
	EvidenceSessions int    `json:"evidence_sessions"`
	GenericFilter    bool   `json:"generic_filter"`
	SingletonPolicy  string `json:"singleton_policy"`
}

type GenerationThresholdOverrides struct {
	MinStrength      *int    `json:"min_strength,omitempty"`
	MinConfidence    *string `json:"min_confidence,omitempty"`
	EvidenceSessions *int    `json:"evidence_sessions,omitempty"`
	GenericFilter    *bool   `json:"generic_filter,omitempty"`
	SingletonPolicy  *string `json:"singleton_policy,omitempty"`
}

var thresholdPresets = map[string]GenerationThresholdConfig{
	"relaxed": {MinStrength: 2, MinConfidence: "low", EvidenceSessions: 1, GenericFilter: false, SingletonPolicy: "all"},
	"balanced": {MinStrength: 3, MinConfidence: "medium", EvidenceSessions: 2, GenericFilter: true, SingletonPolicy: "review-rules"},
	"strict": {MinStrength: 4, MinConfidence: "high", EvidenceSessions: 3, GenericFilter: true, SingletonPolicy: "none"},
}

func ResolveGenerationThreshold(preset string, overrides *GenerationThresholdOverrides) GenerationThresholdConfig {
	cfg, ok := thresholdPresets[preset]
	if !ok {
		cfg = thresholdPresets["balanced"]
	}
	if overrides == nil {
		return cfg
	}
	if overrides.MinStrength != nil {
		cfg.MinStrength = clamp(*overrides.MinStrength, 1, 5)
	}
	if overrides.MinConfidence != nil {
		switch *overrides.MinConfidence {
		case "low", "medium", "high":
			cfg.MinConfidence = *overrides.MinConfidence
		}
	}
	if overrides.EvidenceSessions != nil {
		cfg.EvidenceSessions = clamp(*overrides.EvidenceSessions, 1, 10)
	}
	if overrides.GenericFilter != nil {
		cfg.GenericFilter = *overrides.GenericFilter
	}
	if overrides.SingletonPolicy != nil {
		switch *overrides.SingletonPolicy {
		case "all", "review-rules", "none":
			cfg.SingletonPolicy = *overrides.SingletonPolicy
		}
	}
	return cfg
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd cmd/entire/cli/memoryloop && go test -run TestResolveGenerationThreshold -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/entire/cli/memoryloop/generator.go cmd/entire/cli/memoryloop/generator_test.go
git commit -m "feat(memoryloop): add GenerationThresholdConfig and ResolveGenerationThreshold"
```

---

### Task 7: Wire threshold config into generator filtering

**Files:**
- Modify: `cmd/entire/cli/memoryloop/generator.go:75-80` (GenerateInput), `169-220` (buildGeneratedRecordsDetailed), `409-441` (passesEvidenceGate), `189-205` (RefreshHistory)
- Test: `cmd/entire/cli/memoryloop/generator_test.go`

- [ ] **Step 1: Write failing tests**

Add to `generator_test.go`:

```go
func TestPassesEvidenceGate_ConfigurableSessions(t *testing.T) {
	t.Parallel()
	// Build a minimal analysis with a repeated instruction appearing in 1 session
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

	// With requiredSessions=1 and singleton "all", should pass
	require.True(t, passesEvidenceGate(record, signal, analysis, validIDs, 1, "all"))

	// With requiredSessions=2, should fail (only 1 session)
	require.False(t, passesEvidenceGate(record, signal, analysis, validIDs, 2, "review-rules"))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd cmd/entire/cli/memoryloop && go test -run TestPassesEvidenceGate -v`
Expected: FAIL — wrong number of args

- [ ] **Step 3: Update `GenerateInput` to include threshold config**

In `generator.go`, update `GenerateInput` at line 75:

```go
type GenerateInput struct {
	Analysis        improve.PatternAnalysis
	Sessions        []insightsdb.SessionRow
	SourceWindow    int
	MaxRecords      int
	ThresholdConfig GenerationThresholdConfig
}
```

- [ ] **Step 4: Update `passesEvidenceGate()` signature**

Change signature at line 412:

```go
func passesEvidenceGate(record MemoryRecord, signal *sourceSignal, analysis improve.PatternAnalysis, validSourceIDs map[string]bool, requiredSessions int, singletonPolicy string) bool {
	if signal == nil || signal.Key == "" {
		return false
	}

	matched, found := lookupSignal(signal, analysis)
	if !found {
		return false
	}

	allowSingleton := false
	switch singletonPolicy {
	case "all":
		allowSingleton = true
	case "review-rules":
		allowSingleton = matched.AllowsSingleton
		if matched.Kind == "skill_opportunity" && record.Kind != KindSkillPatch {
			allowSingleton = false
		}
	case "none":
		allowSingleton = false
	}
	if !allowSingleton && matched.Count < requiredSessions {
		return false
	}

	validCount := 0
	affected := matched.AffectedSessionSet()
	for _, id := range record.SourceSessionIDs {
		if validSourceIDs[id] && affected[id] {
			validCount++
		}
	}
	if allowSingleton {
		return validCount >= 1
	}
	return validCount >= requiredSessions
}
```

- [ ] **Step 5: Update `buildGeneratedRecordsDetailed()` to use threshold config**

Replace hardcoded filters at lines 186-220:

```go
func buildGeneratedRecordsDetailed(resp generateResponse, input GenerateInput, now time.Time) ([]MemoryRecord, GenerationStats) {
	cfg := input.ThresholdConfig
	if cfg.MinStrength == 0 {
		cfg = ResolveGenerationThreshold("balanced", nil)
	}
	validSourceIDs := buildValidSourceIDs(input.Sessions)
	// ... existing orderedKeys, bestByKey setup ...

	for _, item := range resp.Records {
		// ... existing title/body/kind cleanup ...

		confidence := normalizeConfidence(item.Confidence)
		strength := clamp(item.Strength, 1, 5)
		if confidenceRank(confidence) < confidenceRank(cfg.MinConfidence) || strength < cfg.MinStrength {
			stats.FilteredWeakCount++
			continue
		}
		if cfg.GenericFilter && isGenericGeneratedCandidate(title, body) {
			stats.FilteredGenericCount++
			continue
		}

		// ... existing record construction ...

		if isLiteralReviewEvidence(record, input.Analysis.ReviewDerivedRules) {
			stats.FilteredGenericCount++
			continue
		}
		if !passesEvidenceGate(record, item.SourceSignal, input.Analysis, validSourceIDs, cfg.EvidenceSessions, cfg.SingletonPolicy) {
			stats.FilteredNoEvidenceCount++
			continue
		}

		// ... rest of dedup/ranking logic unchanged ...
	}
	// ... rest unchanged ...
}
```

- [ ] **Step 6: Update `RefreshHistory` to include threshold info**

In `memoryloop.go`, update `RefreshHistory` struct (line 189):

```go
type RefreshHistory struct {
	// ... existing fields ...
	PrunedCount             int                      `json:"pruned_count,omitempty"`
	InputTokens             int                      `json:"input_tokens,omitempty"`
	OutputTokens            int                      `json:"output_tokens,omitempty"`
	TotalCostUSD            float64                  `json:"total_cost_usd,omitempty"`
	Threshold               string                   `json:"threshold,omitempty"`
	ThresholdOverride       bool                     `json:"threshold_override,omitempty"`
	ResolvedConfig          *GenerationThresholdConfig `json:"resolved_config,omitempty"`
}
```

- [ ] **Step 7: Run tests**

Run: `cd cmd/entire/cli/memoryloop && go test -run TestPassesEvidenceGate -v`
Expected: PASS

Run: `mise run test`
Expected: PASS (fix any callers of `passesEvidenceGate` or `buildGeneratedRecordsDetailed` that pass wrong args)

- [ ] **Step 8: Commit**

```bash
git add cmd/entire/cli/memoryloop/generator.go cmd/entire/cli/memoryloop/generator_test.go cmd/entire/cli/memoryloop/memoryloop.go
git commit -m "feat(memoryloop): wire threshold config into generator filtering and evidence gate"
```

---

### Task 8: Settings types for threshold

**Files:**
- Modify: `cmd/entire/cli/settings/settings.go:120-130` (MemoryLoopSettings), `132-140` (MemoryLoopConfig), `154-198` (GetMemoryLoopConfig)

- [ ] **Step 1: Update `MemoryLoopSettings`**

Add to `MemoryLoopSettings` at line 129:

```go
type MemoryLoopSettings struct {
	// ... existing fields ...
	DefaultRefreshWindow   int                                    `json:"default_refresh_window,omitempty"`
	GenerationThreshold    string                                 `json:"generation_threshold,omitempty"`
	GenerationOverrides    *memoryloop.GenerationThresholdOverrides `json:"generation_overrides,omitempty"`
}
```

Note: This creates an import of the `memoryloop` package from `settings`. Check for import cycles. If a cycle exists, define `GenerationThresholdOverrides` in the `settings` package instead and have the `memoryloop` package accept it as a parameter. The spec's types use pointer fields which are identical — just duplicate the struct in settings if needed to avoid the cycle.

If import cycle exists, define in `settings.go`:

```go
type GenerationThresholdOverrides struct {
	MinStrength      *int    `json:"min_strength,omitempty"`
	MinConfidence    *string `json:"min_confidence,omitempty"`
	EvidenceSessions *int    `json:"evidence_sessions,omitempty"`
	GenericFilter    *bool   `json:"generic_filter,omitempty"`
	SingletonPolicy  *string `json:"singleton_policy,omitempty"`
}
```

- [ ] **Step 2: Update `MemoryLoopConfig`**

```go
type MemoryLoopConfig struct {
	Enabled              bool
	Mode                 string
	Debug                bool
	ActivationPolicy     string
	MaxInjected          int
	DefaultRefreshWindow int
	GenerationThreshold  string
	GenerationOverrides  *GenerationThresholdOverrides
}
```

- [ ] **Step 3: Update `GetMemoryLoopConfig()` to propagate threshold fields**

Add after the existing field propagation in `GetMemoryLoopConfig()`:

```go
	if s.MemoryLoopConfig.GenerationThreshold != "" {
		cfg.GenerationThreshold = s.MemoryLoopConfig.GenerationThreshold
	}
	if s.MemoryLoopConfig.GenerationOverrides != nil {
		cfg.GenerationOverrides = s.MemoryLoopConfig.GenerationOverrides
	}
```

- [ ] **Step 4: Run tests**

Run: `mise run test`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/entire/cli/settings/settings.go
git commit -m "feat(settings): add GenerationThreshold and GenerationOverrides to MemoryLoopSettings"
```

---

### Task 9: CLI `edit` and `threshold` subcommands

**Files:**
- Modify: `cmd/entire/cli/memory_loop_cmd.go`

- [ ] **Step 1: Add `edit` subcommand**

Add `newMemoryLoopEditCmd()` and register it in `newMemoryLoopCmd()`:

```go
cmd.AddCommand(newMemoryLoopEditCmd())
cmd.AddCommand(newMemoryLoopThresholdCmd())
```

```go
func newMemoryLoopEditCmd() *cobra.Command {
	var title, body, keywords string

	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit a memory's title, body, or keywords",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMemoryLoopEdit(cmd, args[0], title, body, keywords)
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "New title")
	cmd.Flags().StringVar(&body, "body", "", "New body")
	cmd.Flags().StringVar(&keywords, "keywords", "", "Comma-separated keywords")
	return cmd
}

func runMemoryLoopEdit(cmd *cobra.Command, id, title, body, keywords string) error {
	ctx := cmd.Context()
	state, err := memoryloop.LoadState(ctx)
	if err != nil {
		return err
	}

	var input memoryloop.EditRecordInput
	hasFlag := false
	if cmd.Flags().Changed("title") {
		input.Title = &title
		hasFlag = true
	}
	if cmd.Flags().Changed("body") {
		input.Body = &body
		hasFlag = true
	}
	if cmd.Flags().Changed("keywords") {
		kw := memoryloop.ParseKeywords(keywords)
		input.Keywords = &kw
		hasFlag = true
	}

	if !hasFlag {
		// Interactive mode: standalone huh forms
		// (Implementation deferred to TUI task — for now, require flags)
		fmt.Fprintln(cmd.ErrOrStderr(), "Interactive edit mode not yet implemented. Use --title, --body, or --keywords flags.")
		return NewSilentError(errors.New("no flags provided"))
	}

	now := time.Now().UTC()
	records, record, err := memoryloop.EditRecord(state.Store.Records, id, input, now)
	if err != nil {
		return err
	}
	state.Store.Records = records
	if err := memoryloop.SaveState(ctx, state); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Updated memory: %s\n", record.Title)
	return nil
}
```

- [ ] **Step 2: Add `threshold` subcommand**

```go
func newMemoryLoopThresholdCmd() *cobra.Command {
	var clearOverrides bool
	var minStrength, evidenceSessions int
	var minConfidence, singletonPolicy string
	var genericFilter string

	cmd := &cobra.Command{
		Use:   "threshold [relaxed|balanced|strict]",
		Short: "Set generation threshold preset",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMemoryLoopThreshold(cmd, args, clearOverrides, minStrength, evidenceSessions, minConfidence, singletonPolicy, genericFilter)
		},
	}
	cmd.Flags().BoolVar(&clearOverrides, "clear-overrides", false, "Clear all threshold overrides")
	cmd.Flags().IntVar(&minStrength, "min-strength", 0, "Override minimum strength (1-5)")
	cmd.Flags().IntVar(&evidenceSessions, "evidence-sessions", 0, "Override required evidence sessions (1-10)")
	cmd.Flags().StringVar(&minConfidence, "min-confidence", "", "Override minimum confidence (low|medium|high)")
	cmd.Flags().StringVar(&singletonPolicy, "singleton-policy", "", "Override singleton policy (all|review-rules|none)")
	cmd.Flags().StringVar(&genericFilter, "generic-filter", "", "Override generic filter (true|false)")
	return cmd
}

func runMemoryLoopThreshold(cmd *cobra.Command, args []string, clearOverrides bool, minStrength, evidenceSessions int, minConfidence, singletonPolicy, genericFilter string) error {
	s, err := settings.Load()
	if err != nil {
		return err
	}
	if s.MemoryLoopConfig == nil {
		s.MemoryLoopConfig = &settings.MemoryLoopSettings{}
	}

	if len(args) > 0 {
		preset := args[0]
		switch preset {
		case "relaxed", "balanced", "strict":
			s.MemoryLoopConfig.GenerationThreshold = preset
		default:
			return fmt.Errorf("unknown threshold preset: %s (expected relaxed, balanced, or strict)", preset)
		}
	}

	if clearOverrides {
		s.MemoryLoopConfig.GenerationOverrides = nil
	} else {
		// Apply individual overrides if any flags were set
		if cmd.Flags().Changed("min-strength") || cmd.Flags().Changed("evidence-sessions") ||
			cmd.Flags().Changed("min-confidence") || cmd.Flags().Changed("singleton-policy") ||
			cmd.Flags().Changed("generic-filter") {
			if s.MemoryLoopConfig.GenerationOverrides == nil {
				s.MemoryLoopConfig.GenerationOverrides = &settings.GenerationThresholdOverrides{}
			}
			ov := s.MemoryLoopConfig.GenerationOverrides
			if cmd.Flags().Changed("min-strength") {
				ov.MinStrength = &minStrength
			}
			if cmd.Flags().Changed("evidence-sessions") {
				ov.EvidenceSessions = &evidenceSessions
			}
			if cmd.Flags().Changed("min-confidence") {
				ov.MinConfidence = &minConfidence
			}
			if cmd.Flags().Changed("singleton-policy") {
				ov.SingletonPolicy = &singletonPolicy
			}
			if cmd.Flags().Changed("generic-filter") {
				val := genericFilter == "true"
				ov.GenericFilter = &val
			}
		}
	}

	if err := settings.Save(s); err != nil {
		return err
	}

	preset := s.MemoryLoopConfig.GenerationThreshold
	if preset == "" {
		preset = "balanced"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Generation threshold set to: %s\n", preset)
	if s.MemoryLoopConfig.GenerationOverrides != nil {
		fmt.Fprintln(cmd.OutOrStdout(), "Overrides are active.")
	}
	return nil
}
```

- [ ] **Step 3: Run tests**

Run: `mise run test`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add cmd/entire/cli/memory_loop_cmd.go
git commit -m "feat(cli): add 'memory-loop edit' and 'memory-loop threshold' subcommands"
```

---

### Task 10: TUI — Keywords display on detail page

**Files:**
- Modify: `cmd/entire/cli/memorylooptui/detail_page.go:105-136` (renderContentCard)
- Test: `cmd/entire/cli/memorylooptui/detail_page_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestRenderContentCard_ShowsKeywords(t *testing.T) {
	t.Parallel()
	styles := defaultStyles(80)
	record := memoryloop.MemoryRecord{
		Title:    "Test",
		Body:     "Test body",
		Keywords: []string{"go test", "deploy"},
	}
	m := newMemoryDetailModel(styles, record, nil)
	m.setSize(80, 40)
	view := m.view()
	require.Contains(t, view, "go test")
	require.Contains(t, view, "deploy")
	require.Contains(t, view, "KEYWORDS")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd cmd/entire/cli/memorylooptui && go test -run TestRenderContentCard_ShowsKeywords -v`
Expected: FAIL — no KEYWORDS section in output

- [ ] **Step 3: Add keywords section to `renderContentCard()`**

In `detail_page.go`, in `renderContentCard()` after the body section (line 108-112) and before the WHY section:

```go
func (m *memoryDetailModel) renderContentCard() string {
	var body strings.Builder

	if m.record.Body != "" {
		body.WriteString(m.record.Body)
	} else {
		body.WriteString(m.styles.render(m.styles.dim, "No memory body recorded."))
	}

	if len(m.record.Keywords) > 0 {
		body.WriteString("\n\n")
		body.WriteString(m.styles.render(m.styles.sectionHeader, "KEYWORDS"))
		body.WriteString("\n")
		body.WriteString(strings.Join(m.record.Keywords, ", "))
	}

	body.WriteString("\n\n")
	body.WriteString(m.styles.render(m.styles.sectionHeader, "WHY"))
	// ... rest unchanged ...
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd cmd/entire/cli/memorylooptui && go test -run TestRenderContentCard_ShowsKeywords -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/entire/cli/memorylooptui/detail_page.go cmd/entire/cli/memorylooptui/detail_page_test.go
git commit -m "feat(tui): display keywords section on memory detail page"
```

---

### Task 11: TUI — Edit wizard action

**Files:**
- Modify: `cmd/entire/cli/memorylooptui/wizard.go` (add Edit intent and stages)
- Modify: `cmd/entire/cli/memorylooptui/messages.go` (add WizardIntentEdit, editMemoryMsg)

- [ ] **Step 1: Add `WizardIntentEdit` to messages.go**

```go
const (
	WizardIntentEdit     WizardIntent = "edit"
	WizardIntentAdopt    WizardIntent = "adopt"
	// ... existing ...
)

type editMemoryMsg struct {
	input memoryloop.EditRecordInput
}
```

- [ ] **Step 2: Add Edit to wizard action options in wizard.go**

Update `wizardActionOptions`:

```go
var wizardActionOptions = []struct {
	intent WizardIntent
	label  string
}{
	{intent: WizardIntentEdit, label: "Edit"},
	{intent: WizardIntentAdopt, label: "Adopt to scope"},
	{intent: WizardIntentApply, label: "Apply to files"},
	{intent: WizardIntentSuppress, label: "Suppress"},
	{intent: WizardIntentArchive, label: "Archive"},
}
```

- [ ] **Step 3: Add edit stages to wizard**

Add new wizard stages:

```go
const (
	wizardStageAction wizardStage = iota
	wizardStageScope
	wizardStageLocation
	wizardStagePreview
	wizardStageEditTitle    // NEW
	wizardStageEditKeywords // NEW
	wizardStageEditBody     // NEW
	wizardStageEditPreview  // NEW
)
```

Add edit state fields to `wizardModel`:

```go
type wizardModel struct {
	// ... existing fields ...
	editTitle    string
	editKeywords string
	editBody     string
	editChanged  bool
}
```

Update `advanceFromAction()` to handle the edit intent:

```go
case WizardIntentEdit:
	m.editTitle = m.record.Title
	m.editKeywords = strings.Join(m.record.Keywords, ", ")
	m.editBody = m.record.Body
	m.stage = wizardStageEditTitle
	m.request.Intent = selected.intent
	m.request.RecordID = m.record.ID
```

The edit stages use simple text input. Since the TUI runs in Bubble Tea (not huh forms inline), the edit flow will use tea.Msg-based text editing. For the initial implementation, the Edit action emits a `wizardResultMsg` with intent `edit` that the root model handles by launching a standalone `huh` form outside the TUI (same as the add flow). The `$EDITOR` handoff for body is handled by the `WizardActionHandler` callback.

- [ ] **Step 4: Update `WizardActionHandler` in root.go to handle edit**

The handler receives `WizardIntentEdit` and collects the edit input. The handler calls `editInEditor()` for the body field, then calls `EditRecord()` and saves state.

Add `editInEditor()` to `wizard.go`:

```go
var errNoEditor = errors.New("no editor configured")

func editInEditor(current string) (string, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		return "", errNoEditor
	}

	tmpFile, err := os.CreateTemp("", "entire-memory-*.md")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(current); err != nil {
		tmpFile.Close()
		return "", fmt.Errorf("write temp file: %w", err)
	}
	tmpFile.Close()

	parts := strings.Fields(editor)
	cmdArgs := append(parts[1:], tmpFile.Name())
	editCmd := exec.Command(parts[0], cmdArgs...)
	editCmd.Stdin = os.Stdin
	editCmd.Stdout = os.Stdout
	editCmd.Stderr = os.Stderr
	if err := editCmd.Run(); err != nil {
		return "", fmt.Errorf("editor exited with error: %w", err)
	}

	data, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		return "", fmt.Errorf("read temp file: %w", err)
	}

	result := strings.TrimSpace(string(data))
	if result == "" {
		return "", errors.New("empty body — edit cancelled")
	}
	return result, nil
}
```

- [ ] **Step 5: Run tests**

Run: `mise run test`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add cmd/entire/cli/memorylooptui/wizard.go cmd/entire/cli/memorylooptui/messages.go
git commit -m "feat(tui): add Edit action to memory wizard with editor handoff"
```

---

### Task 12: TUI — Threshold card in settings tab

**Files:**
- Modify: `cmd/entire/cli/memorylooptui/tab_settings.go` (add threshold card)
- Modify: `cmd/entire/cli/memorylooptui/keys.go` (add threshold keybinding)
- Modify: `cmd/entire/cli/memorylooptui/messages.go` (add threshold field to settingsChangedMsg)

- [ ] **Step 1: Add threshold field to `settingsChangedMsg`**

In `messages.go`, update:

```go
type settingsChangedMsg struct {
	mode                 *memoryloop.Mode
	activationPolicy     *memoryloop.ActivationPolicy
	maxInjected          *int
	injectionScopes      *[]memoryloop.ScopeKind
	generationThreshold  *string // NEW
}
```

- [ ] **Step 2: Add keybinding for threshold cycling**

In `keys.go`, add to the settings key map:

```go
Threshold: key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "cycle threshold")),
```

- [ ] **Step 3: Add threshold card to settings tab**

In `tab_settings.go`, add a handler in `update()`:

```go
case key.Matches(keyMsg, settingsKeyMap.Threshold):
	next := cycleThreshold(m.state.Store.GenerationThreshold)
	nextStr := string(next)
	changed.generationThreshold = &nextStr
	hasChange = true
```

Add `cycleThreshold()`:

```go
var thresholdOrder = []string{"relaxed", "balanced", "strict"}

func cycleThreshold(current string) string {
	if current == "" {
		current = "balanced"
	}
	for i, t := range thresholdOrder {
		if t == current {
			return thresholdOrder[(i+1)%len(thresholdOrder)]
		}
	}
	return "balanced"
}
```

Add the threshold card in `view()` after the Injection Scopes card:

```go
// Generation Threshold card
{
	var c strings.Builder
	c.WriteString(m.styles.render(m.styles.bold, "Generation Threshold"))
	c.WriteString("  ")
	c.WriteString(m.styles.render(m.styles.dim, "Controls how aggressively memories are filtered during refresh"))
	c.WriteString("\n")
	current := store.GenerationThreshold
	if current == "" {
		current = "balanced"
	}
	hasOverrides := store.GenerationOverrides != nil
	for _, preset := range thresholdOrder {
		label := preset
		if preset == current {
			if hasOverrides {
				label += "*"
			}
			c.WriteString(selectedChip.Render(label))
		} else {
			c.WriteString(unselectedChip.Render(label))
		}
		c.WriteString(" ")
	}
	if hasOverrides {
		c.WriteString("\n")
		c.WriteString(m.styles.render(m.styles.dim, "Overrides active — run 'entire memory-loop threshold --clear-overrides' to reset"))
	}
	b.WriteString(cardStyle.Render(c.String()))
	b.WriteString("\n")
}
```

Note: The `Store` struct needs `GenerationThreshold` and `GenerationOverrides` fields. These are already on the settings — check if the `Store` type in `memoryloop.go` needs them too, or if the settings tab reads from settings directly.

- [ ] **Step 4: Run tests**

Run: `mise run test`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/entire/cli/memorylooptui/tab_settings.go cmd/entire/cli/memorylooptui/keys.go cmd/entire/cli/memorylooptui/messages.go
git commit -m "feat(tui): add generation threshold card to settings tab"
```

---

### Task 13: Wire threshold from settings into refresh command

**Files:**
- Modify: `cmd/entire/cli/memory_loop_cmd.go` (refresh command to pass threshold config)

- [ ] **Step 1: Update the refresh command to pass threshold config to the generator**

Find `newMemoryLoopRefreshCmd()` / `runMemoryLoopRefresh()` in `memory_loop_cmd.go`. Where it constructs `memoryloop.GenerateInput`, add the threshold config:

```go
s, err := settings.Load()
if err != nil {
	return err
}
mlConfig := s.GetMemoryLoopConfig()

// Convert settings overrides to memoryloop overrides
var overrides *memoryloop.GenerationThresholdOverrides
if mlConfig.GenerationOverrides != nil {
	overrides = &memoryloop.GenerationThresholdOverrides{
		MinStrength:      mlConfig.GenerationOverrides.MinStrength,
		MinConfidence:    mlConfig.GenerationOverrides.MinConfidence,
		EvidenceSessions: mlConfig.GenerationOverrides.EvidenceSessions,
		GenericFilter:    mlConfig.GenerationOverrides.GenericFilter,
		SingletonPolicy:  mlConfig.GenerationOverrides.SingletonPolicy,
	}
}

preset := mlConfig.GenerationThreshold
if preset == "" {
	preset = "balanced"
}
thresholdCfg := memoryloop.ResolveGenerationThreshold(preset, overrides)

input := memoryloop.GenerateInput{
	// ... existing fields ...
	ThresholdConfig: thresholdCfg,
}
```

Also update the `RefreshHistory` entry to include threshold info:

```go
history.Threshold = preset
history.ThresholdOverride = overrides != nil
history.ResolvedConfig = &thresholdCfg
```

- [ ] **Step 2: Run tests**

Run: `mise run test`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add cmd/entire/cli/memory_loop_cmd.go
git commit -m "feat(cli): pass threshold config from settings to generator during refresh"
```

---

### Task 14: Final integration — format, lint, full test

- [ ] **Step 1: Format and lint**

Run: `mise run fmt && mise run lint`
Fix any issues.

- [ ] **Step 2: Run full test suite**

Run: `mise run test:ci`
Expected: PASS

- [ ] **Step 3: Run canary E2E tests**

Run: `mise run test:e2e:canary`
Expected: PASS

- [ ] **Step 4: Final commit if any fixes were needed**

```bash
git add -A
git commit -m "chore: fix lint and formatting for memory-loop edit/keywords/threshold"
```
