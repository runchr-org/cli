# Memory Loop: Edit, Keywords, and Generation Threshold

**Date:** 2026-04-03
**Status:** Draft

## Overview

Three changes to the memory-loop system:

1. **Memory editing** — users can edit title, body, and keywords on any memory via the TUI wizard, with `$EDITOR` handoff for body text
2. **Keyword matching** — user-defined keywords on memories that boost injection scoring when matched against the prompt
3. **Generation threshold** — a user-configurable preset (relaxed/balanced/strict) that controls how aggressively the generation pipeline filters candidates

## 1. Keywords on MemoryRecord

### Data Model

Add a `Keywords` field to `MemoryRecord`:

```go
type MemoryRecord struct {
    // ... existing fields ...
    Keywords []string `json:"keywords,omitempty"`
}
```

- User-provided phrases (e.g., `"go test"`, `"authentication"`, `"deploy"`)
- Set during manual memory creation (`AddManualRecord`) or via the Edit flow
- Empty by default on generated memories — user can add them later via Edit
- Stored as-is (user's exact text). Matching is case-insensitive substring match against the lowercased prompt.
- **Keywords are phrases, not tokens.** `"go test"` matches the literal substring `"go test"` in the prompt — it does NOT get decomposed into `"go"` and `"test"`. This makes keywords a precision tool: `"go test"` won't match a prompt about "test coverage for Python".
- **Maximum 10 keywords per memory.** Enforced at write time (`AddManualRecord`, `EditRecord`). Prevents over-tagging that would undermine the scoring system. If more than 10 are provided, only the first 10 are kept and a warning is shown.

### Keyword Parsing Rules

Keywords are entered as comma-separated values. Parsing rules:
- Each keyword is trimmed of leading/trailing whitespace
- Empty entries (from `"foo,,bar"` or trailing commas) are silently dropped
- Duplicate keywords (case-insensitive) are deduplicated, keeping the first occurrence
- Commas are the delimiter and cannot appear inside a keyword
- Minimum keyword length: 2 characters (shorter entries are dropped with a warning)

### ManualRecordInput Change

```go
type ManualRecordInput struct {
    Kind       Kind
    Title      string
    Body       string
    Keywords   []string  // NEW
    ScopeKind  ScopeKind
    ScopeValue string
    OwnerEmail string
}
```

`AddManualRecord()` copies `input.Keywords` onto the new record. The manual-add TUI form gains a comma-separated keywords input field.

### Injection Scoring

In `buildRecordMatchSignals()`, keywords are stored as lowercased phrases alongside the existing title+body primary tokens:

```go
type recordMatchSignals struct {
    primaryTokens    map[string]struct{}
    keywordPhrases   []string // NEW: each entry is one keyword phrase, lowercased
}

func buildRecordMatchSignals(record MemoryRecord) recordMatchSignals {
    phrases := make([]string, 0, len(record.Keywords))
    for _, kw := range record.Keywords {
        kw = strings.TrimSpace(strings.ToLower(kw))
        if kw != "" {
            phrases = append(phrases, kw)
        }
    }
    return recordMatchSignals{
        primaryTokens:  tokenize(record.Title + " " + record.Body),
        keywordPhrases: phrases,
    }
}
```

**Keyword matching is case-insensitive substring matching.** Each keyword phrase is checked against the lowercased prompt using `strings.Contains`. A keyword matches if and only if the full phrase appears as a substring. The number of matched keywords is the `keywordOverlap`. Overlapping phrase matches each count independently — if a user sets keywords `"go"` and `"go test"`, and the prompt contains `"go test"`, both match and `keywordOverlap = 2`.

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

In `scoreRecord()`, keyword overlap gets a **3x multiplier** compared to regular tokens:

```
keywordOverlap = countMatchedKeywords(promptLower, keywordPhrases)
score = (primaryOverlap * 7) + (keywordOverlap * 21) + kindBonus + strength + ageBonus
```

Each matched keyword is worth 3 regular token matches. Keywords do NOT bypass any gates — the record still needs `status == active`, passes confidence/strength checks, and competes in the normal ranking. This preserves the unified scoring system while giving users meaningful control over when their memories surface.

**Score visibility:** The injection tab prompt tester already shows per-record scoring details. Keyword matches are shown as a separate line (e.g., `Keywords matched: 2/3 (go test, deploy)`) so users can see exactly which keywords fired and calibrate.

### Keyword Display

- **Detail page:** Keywords shown as a labeled row (e.g., `Keywords: go test, authentication, deploy`)
- **Memories tab list:** Keywords not shown in the compact list view (too noisy)
- **Injection tab prompt tester:** Keyword matches highlighted separately in the scoring breakdown

### Reconciliation

When the generator reconciles (`ReconcileGeneratedRecords`), user-set keywords are preserved. If a generated record matches an existing record by fingerprint, the existing `Keywords` field is not overwritten — it's user data.

**Fingerprint divergence scenario:** If a user adds keywords to a generated memory (without editing title/body), the fingerprint stays the same and reconciliation works normally. If the user later edits the title/body (changing the fingerprint), the generator may produce a new version with the old fingerprint. In this case:
- The edited record has a new fingerprint — the generator won't match it
- The generator may create a new candidate with the old fingerprint
- The user's edited version (with keywords) is preserved as a separate record
- The new candidate appears for review and can be archived if redundant

This is acceptable: editing title/body is an explicit act of divergence from the generated version. The user owns the edited record.

## 2. Memory Editing

### Edit Flow (TUI)

New `WizardIntentEdit` action added to the wizard action list:

```go
var wizardActionOptions = []struct {
    intent WizardIntent
    label  string
}{
    {intent: WizardIntentEdit, label: "Edit"},           // NEW - first position
    {intent: WizardIntentAdopt, label: "Adopt to scope"},
    {intent: WizardIntentApply, label: "Apply to files"},
    {intent: WizardIntentSuppress, label: "Suppress"},
    {intent: WizardIntentArchive, label: "Archive"},
}
```

When "Edit" is selected, the wizard transitions through these stages:

1. **Title edit** — inline `huh.Input` pre-filled with current title
2. **Keywords edit** — inline `huh.Input` pre-filled with comma-separated current keywords
3. **Body edit** — opens `$EDITOR` with current body pre-filled in a temp file. On save, reads the file back. Falls back to inline `huh.Text` if `$EDITOR` is unset.
4. **Preview** — shows changes in a simple labeled format:
   ```
   Title:    "old title" → "new title"
   Keywords: "go test, auth" → "go test, deploy"
   Body:     (changed via editor)
   ```
   Unchanged fields are omitted. Body shows `(changed via editor)` rather than inline diff since it may be multi-line.
5. **Confirm** — applies changes and returns to detail view

### Editor Handoff

```go
func editInEditor(current string) (string, error) {
    editor := os.Getenv("EDITOR")
    if editor == "" {
        editor = os.Getenv("VISUAL")
    }
    if editor == "" {
        return "", errNoEditor // signals fallback to huh.Text
    }

    tmpFile, err := os.CreateTemp("", "entire-memory-*.md")
    // write current content to tmpFile
    // exec editor on tmpFile (blocks until editor exits)
    // read tmpFile back
    // clean up tmpFile
    return result, nil
}
```

This follows the same pattern as `git commit` and `kubectl edit`. The temp file uses `.md` extension so editors apply markdown highlighting.

**Command parsing:** The `$EDITOR` value is parsed using `strings.Fields()` to support editors with arguments (e.g., `EDITOR="code --wait"`). The first element is the command, the rest are prepended arguments, and the temp file path is appended as the final argument. This matches how `git` handles `$EDITOR`.

**Editor failure modes:**

| Scenario | Behavior |
|---|---|
| `$EDITOR` / `$VISUAL` unset | Fall back to inline `huh.Text` (not an error) |
| Editor exits with non-zero code | Cancel edit, return error, show "Editor exited with error — edit cancelled" |
| User saves empty file | Treat as "cancel edit" (body cannot be empty per `EditRecord` validation). Show "Empty body — edit cancelled" |
| User saves unchanged content | No-op, skip to next field |
| Editor doesn't block (e.g., `code` without `--wait`) | Document in help text that `$EDITOR` must be a blocking command. Recommend `code --wait` for VS Code users. If the editor returns immediately, the temp file is read as-is (likely unchanged = no-op). |
| Temp file permission / write error | Cancel edit, return error |
| Process killed (SIGINT/SIGTERM) while editor is open | Temp file may be left behind in `/tmp/` — same behavior as `git commit`. Acceptable; stale files are harmless `.md` files. |

### Accessible Mode

When `ACCESSIBLE=1`, all fields use inline `huh` form inputs (no `$EDITOR` handoff). The form is wrapped with `NewAccessibleForm()` per the codebase convention.

### Core Function

New `EditRecord()` function in `memoryloop.go`:

```go
type EditRecordInput struct {
    Title    *string   // nil = no change
    Body     *string   // nil = no change
    Keywords *[]string // nil = no change
}

func EditRecord(records []MemoryRecord, id string, input EditRecordInput, now time.Time) ([]MemoryRecord, MemoryRecord, error)
```

- Only non-nil fields are updated
- **Validation:** Title and body cannot be set to empty strings (returns error). Keywords are parsed per the keyword parsing rules (trimmed, deduped, max 10). Keywords do NOT affect the fingerprint — `FingerprintForRecord(kind, title, body)` is unchanged. Setting `Keywords` to an empty slice clears all keywords.
- Updates `Fingerprint` if title or body changed (so reconciliation treats it as the new canonical version)
- Updates `UpdatedAt`
- Appends a `HistoryEvent` that captures **previous values** of changed fields for undo/audit:
  ```go
  HistoryEvent{
      Type:   "edited",
      At:     now,
      Detail: `{"fields":["title","body"],"prev_title":"old title","prev_body":"old body"}`,
  }
  ```
  The `Detail` field is intentionally a raw JSON string (not a typed sub-struct) for consistency with how other `HistoryEvent` types use `Detail`. Consumers double-parse: unmarshal the `HistoryEvent`, then `json.Unmarshal` on `Detail` if needed. This allows recovery if a user accidentally overwrites content. Keywords previous values are stored as `"prev_keywords":"kw1,kw2"`.
- Works on any status (active, candidate, suppressed) — editing doesn't change status
- Archived records cannot be edited (return error)
- Editing does NOT change `Origin` — a generated memory that's edited stays `origin: generated`. The edit history event provides the audit trail.
- **Fingerprint is only used for reconciliation matching**, not as a stable identifier elsewhere in the system. Changing it via edit is safe.

### CLI Subcommand

Also available as `entire memory-loop edit <id>` for scriptability:

```
entire memory-loop edit <id> --title "new title" --body "new body" --keywords "go test,deploy"
```

If no flags are provided, opens interactive mode — standalone `huh` form prompts in the terminal for each field (not the full TUI summary app).

### Concurrency and Persistence

The memory-loop store (`memory-loop.json`) uses the same read-modify-write pattern as all other store operations in the codebase. There is no file locking today — concurrent writes from two CLI/TUI instances result in last-writer-wins. This is acceptable for the same reason it's acceptable for all other store operations: memory-loop edits are infrequent, single-user operations. This is not a new risk introduced by the edit feature.

**Persistence failure:** The edit flow collects all changes before calling `EditRecord()` and saving the store. If the save fails, the in-memory state is not applied — the user sees an error. The temp file from `$EDITOR` is cleaned up regardless. The previous-values captured in the history event are only written on successful save, so there's no partial-write corruption risk.

## 3. Generation Threshold

### Presets

Three named presets that control the generation filtering pipeline:

| Parameter | Relaxed | Balanced (default) | Strict |
|---|---|---|---|
| Min strength | 2 | 3 | 4 |
| Min confidence | low | medium | high |
| Evidence sessions required | 1 | 2 | 3 |
| Generic filter | off | on | on |
| Singleton policy | `all` | `review-rules` | `none` |

**Relaxed:** Lets through weaker, single-session signals. Good for new repos with little history or users who want more suggestions to review.

**Balanced:** Current behavior. Requires repeated signals with evidence. Filters generic advice. The balanced preset values are an exact match for the current hardcoded values (`strength < 3`, `MinConfidence: "medium"` which filters `"low"` via `confidenceRank` ordering `low=1 < medium=2 < high=3`, 2-session evidence gate, generic filter on, singleton policy `"review-rules"`). The existing `confidenceRank()` function at `generator.go:224` already returns these ordered integers, so `confidenceRank(c) < confidenceRank("medium")` is behaviorally equivalent to the current `confidence == confidenceLow` check. This ensures upgrading to configurable thresholds is a no-op for existing users.

**Strict:** Only strong, well-evidenced memories from 3+ sessions. For mature repos where noise is the main concern. **Note:** Strict mode will generate few or zero memories for repos with limited session history. The TUI and CLI show a hint when strict is selected: "Strict mode requires 3+ sessions per signal — may produce fewer results for newer repos."

### Data Model

Settings in `.entire/settings.json`:

```json
{
  "memory_loop": {
    "generation_threshold": "balanced",
    "generation_overrides": {
      "min_strength": 2,
      "min_confidence": "low",
      "evidence_sessions": 1,
      "generic_filter": true,
      "singleton_policy": "all"
    }
  }
}
```

- `generation_threshold` — preset name (`relaxed`, `balanced`, `strict`). Default: `balanced`.
- `generation_overrides` — optional individual overrides that take precedence over the preset. Omitted fields fall back to the preset value.

### Settings Types

```go
type MemoryLoopSettings struct {
    // ... existing fields ...
    GenerationThreshold  string                       `json:"generation_threshold,omitempty"`
    GenerationOverrides  *GenerationThresholdOverrides `json:"generation_overrides,omitempty"`
}

type GenerationThresholdOverrides struct {
    MinStrength      *int    `json:"min_strength,omitempty"`
    MinConfidence    *string `json:"min_confidence,omitempty"`
    EvidenceSessions *int    `json:"evidence_sessions,omitempty"`
    GenericFilter    *bool   `json:"generic_filter,omitempty"`
    SingletonPolicy  *string `json:"singleton_policy,omitempty"` // "all", "review-rules", "none"
}
```

Resolved config type (presets + overrides merged):

```go
type GenerationThresholdConfig struct {
    MinStrength      int
    MinConfidence    string
    EvidenceSessions int
    GenericFilter    bool
    SingletonPolicy  string // "all" | "review-rules" | "none"
}
```

A `ResolveGenerationThreshold(preset string, overrides *GenerationThresholdOverrides) GenerationThresholdConfig` function merges preset defaults with any overrides.

**Validation:** `ResolveGenerationThreshold` clamps all override values to valid ranges. Out-of-range values are clamped to the nearest valid bound (not silently dropped to the preset default):
- `min_strength`: clamped to [1, 5] (e.g., 0 → 1, 7 → 5)
- `min_confidence`: must be `"low"`, `"medium"`, or `"high"` — unrecognized string → ignored (preset value used), with a logged warning
- `evidence_sessions`: clamped to [1, 10] (e.g., 0 → 1, 20 → 10)
- `singleton_policy`: must be `"all"`, `"review-rules"`, or `"none"` — unrecognized → ignored (preset value used), with a logged warning
- `generic_filter`: boolean, no validation needed
- Unknown `generation_threshold` preset name: falls back to `balanced` with a logged warning

### Generator Integration

The generator's `buildGeneratedRecordsDetailed()` currently has hardcoded filter values:

```go
// Current (hardcoded):
if confidence == confidenceLow || strength < 3 { ... }
if isGenericGeneratedCandidate(title, body) { ... }
if !passesEvidenceGate(...) { ... }
```

These change to use `GenerationThresholdConfig`:

```go
// New (configurable):
if confidenceRank(confidence) < confidenceRank(cfg.MinConfidence) || strength < cfg.MinStrength { ... }
if cfg.GenericFilter && isGenericGeneratedCandidate(title, body) { ... }
```

`passesEvidenceGate()` gains a `requiredSessions int` and `singletonPolicy string` parameter instead of the current hardcoded `2` and review-rule-only singleton logic. The singleton policy controls which signal types are allowed through with only 1 session:
- `"all"` — any signal type can pass with 1 session
- `"review-rules"` — only review-derived rules and strong skill opportunities can pass with 1 session (current behavior)
- `"none"` — all signals require the full `requiredSessions` count

The `GenerateInput` struct gets a new `ThresholdConfig GenerationThresholdConfig` field, populated by the caller from settings.

### TUI Settings Tab

New card in the Settings tab for generation threshold:

```
Generation Threshold  Controls how aggressively memories are filtered during refresh
  relaxed   balanced   strict
```

Same chip-style selector as the existing Mode and Policy cards, cycled with a keybinding (e.g., `t`).

Overrides are NOT editable in the TUI — they're a power-user escape hatch edited directly in `settings.json` or via CLI. When overrides are active, the TUI shows an indicator next to the preset chip (e.g., `balanced*`) and a dim line below: `"Overrides active — run 'entire memory-loop threshold --clear-overrides' to reset"`. This makes the effective state visible without exposing override editing in the TUI.

### CLI

```
entire memory-loop threshold relaxed|balanced|strict
```

Sets the preset. To set overrides:

```
entire memory-loop threshold balanced --min-strength 2 --evidence-sessions 1
```

To clear all overrides (revert to pure preset behavior):

```
entire memory-loop threshold balanced --clear-overrides
```

Setting a new preset without `--clear-overrides` preserves existing overrides. This is intentional — overrides are per-parameter customizations that may still be desired with a different base preset. Running `entire memory-loop threshold --clear-overrides` (without a preset argument) clears overrides while keeping the current preset.

### Refresh History

`RefreshHistory` already tracks `FilteredWeakCount`, `FilteredGenericCount`, `FilteredNoEvidenceCount`. These continue to work — they just report what the current threshold filtered. No schema change needed.

The resolved threshold config is added to `RefreshHistory`:

```go
type RefreshHistory struct {
    // ... existing fields ...
    Threshold         string `json:"threshold,omitempty"`           // NEW: preset name
    ThresholdOverride bool   `json:"threshold_override,omitempty"` // NEW: true if overrides were active
    ResolvedConfig    *GenerationThresholdConfig `json:"resolved_config,omitempty"` // NEW: actual values used
}
```

This lets users see exactly what threshold was active for past refreshes. When overrides are present, `ThresholdOverride` is `true` and `ResolvedConfig` shows the actual values used (not just the preset name), making it clear when behavior diverges from the named preset.

## Cross-Cutting: Scope and Performance

### Scope Interaction

- **Keywords:** Boost scoring equally regardless of scope. A personal-scope memory with keyword `"deploy"` gets the same keyword boost as a repo-scope memory with the same keyword. Scope bonuses (personal = +1) still apply separately on top.
- **Generation threshold:** Applies globally to all scopes during generation. The same preset controls filtering for repo-scope, personal-scope, and branch-scope memories. Per-scope thresholds would add complexity without clear user benefit — if needed later, it can be added as an override.

### Performance

Keyword matching adds one `strings.Contains` call per keyword per memory per injection decision. This is simpler than the existing token-overlap computation (which builds and intersects sets). At realistic scales (< 200 active memories, < 10 keywords each), this adds negligible cost. No optimization needed unless memory counts grow significantly.

## Files Changed

### `cmd/entire/cli/memoryloop/`

| File | Changes |
|---|---|
| `memoryloop.go` | Add `Keywords` to `MemoryRecord`, `EditRecordInput`, `EditRecord()`, `parseKeywords()`, `countMatchedKeywords()`, update `buildRecordMatchSignals()` and `scoreRecord()` for keyword boost, update `ManualRecordInput` |
| `generator.go` | Replace hardcoded filters with `GenerationThresholdConfig` params, update `passesEvidenceGate()` signature, add `GenerationThresholdConfig` and `ResolveGenerationThreshold()` |

### `cmd/entire/cli/memorylooptui/`

| File | Changes |
|---|---|
| `wizard.go` | Add `WizardIntentEdit`, edit stages (title, keywords, body via `$EDITOR`, preview, confirm) |
| `messages.go` | Add `WizardIntentEdit`, `editMemoryMsg` |
| `tab_settings.go` | Add generation threshold card with chip selector |
| `keys.go` | Add keybinding for threshold cycling |
| `detail_page.go` | Display keywords row |

### `cmd/entire/cli/settings/`

| File | Changes |
|---|---|
| `settings.go` | Add `GenerationThreshold`, `GenerationOverrides`, `GenerationThresholdOverrides` to `MemoryLoopSettings`, add to `MemoryLoopConfig` resolution |

### `cmd/entire/cli/`

| File | Changes |
|---|---|
| `memory_loop_cmd.go` | Add `edit` and `threshold` subcommands, pass threshold config to generator |

## Testing

### Unit Tests

**Keywords:**
- `countMatchedKeywords()`: verify exact phrase substring matching — `"go test"` matches `"run go test -v"` but NOT `"test the go module"` or `"pytest coverage"`
- `countMatchedKeywords()`: verify case-insensitive matching
- `scoreRecord()` with keywords: verify 3x multiplier, verify keywords don't bypass gates
- `buildRecordMatchSignals()`: verify keyword phrases stored as lowercased strings, separate from primary tokens
- `parseKeywords()`: verify trimming, empty-entry removal, dedup, max 10 cap, min 2-char filter
- `AddManualRecord()` with keywords
- `ReconcileGeneratedRecords()`: verify user keywords preserved on fingerprint match; verify fingerprint divergence scenario (edited record + new generator candidate coexist)

**Editing:**
- `EditRecord()`: verify title/body/keyword update, fingerprint recalculation, history event with previous values, archived-record rejection, nil-field no-op
- `EditRecord()`: verify empty title/body rejected, verify keywords don't affect fingerprint
- `EditRecord()` keyword limit: verify max 10 keywords enforced, verify empty slice clears keywords

**Threshold:**
- `ResolveGenerationThreshold()`: verify each preset's values, verify overrides take precedence
- `ResolveGenerationThreshold()`: verify validation — clamping for numerics, ignored for invalid strings, logged warnings
- `ResolveGenerationThreshold()` balanced preset: verify exact match with current hardcoded values (migration safety)
- `ResolveGenerationThreshold()`: verify `singleton_policy` tri-state (`"all"`, `"review-rules"`, `"none"`)
- `buildGeneratedRecordsDetailed()` with each threshold preset: verify filter behavior changes
- `passesEvidenceGate()` with configurable session count and singleton policy

### TUI Tests

- Wizard edit flow: stage transitions, `$EDITOR` fallback to `huh.Text`
- Settings tab: threshold chip cycling, override indicator (`balanced*`)
- Detail page: keywords display

### Integration Tests

- Full refresh cycle with each threshold preset
- Edit via CLI subcommand
- `--clear-overrides` CLI flag
- Manual add with keywords, then prompt test showing keyword phrase boost
