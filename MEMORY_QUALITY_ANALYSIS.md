# Memory Loop Quality Analysis

## Problem

The memory-loop pipeline lets too many one-off, episodic memories through. "Stability" (whether a memory reflects a real recurring pattern vs. a one-off incident) is enforced via prompt instruction to the LLM, not in code.

## Current Filtering Pipeline

### What the post-filter catches (generator.go)

1. **Low confidence / low strength** (line 155-161): `confidence == "low"` or `strength < 3` → filtered
2. **Generic advice** (line 159): matches against `genericAdvicePhrases` list → filtered
3. **Review-derived gate** (line 182-185): review-derived rules require `count >= 2 || strong` → filtered if singleton and not strong
4. **Deduplication** (line 187-203): normalized key dedup across kind+title+body

### What it does NOT catch

- **One-off memories with high confidence**: A record with `confidence: "high"`, `strength: 4`, backed by a single session sails through untouched.
- **No session count validation**: `SourceSessionIDs` is captured (line 173) but never validated for length. A record backed by 1 session is treated identically to one backed by 5.
- **No cross-reference with analysis signals**: The upstream `PatternAnalysis` already tracks `Count` on `RecurringSignal`, `SkillOpportunity`, and `ReviewDerivedRuleSignal`. The generator prompt includes these counts. But the post-filter only cross-references for review-derived rules (`passesReviewDerivedGate`), not for other signal types.

### Root cause

The prompt (line 319) says "Avoid duplicates and avoid one-off incidents without repeated evidence" — but that's a suggestion to the LLM, not enforced in code. The `passesReviewDerivedGate` pattern (line 360-375) enforces `count >= 2 || strong` but **only** for review-derived rules, not for `repo_rule`, `workflow_rule`, `agent_instruction`, or `anti_pattern` kinds.

## Proposed Fixes (Options Analysis)

### Option 1: Hard gate on `len(SourceSessionIDs) >= 2`

Reject any generated record where `SourceSessionIDs` has fewer than 2 entries, unless the kind is allowlisted (e.g., strong review-derived rules).

**Pros:**
- Simple, deterministic, no LLM judgment needed
- Directly addresses the root cause
- Easy to test and reason about

**Cons:**
- The LLM fills `source_session_ids` — it can hallucinate or pad the list, so you'd be enforcing a hard gate on a soft signal
- Cold-start problem: new repos or new patterns won't have 2 sessions yet; first discovery of a real footgun gets dropped
- `SourceWindow` sessions are already in the prompt — if 3 sessions saw a pattern the LLM should already cite them; the real question is whether the *analysis* surfaced it as recurring

### Option 2: Allowlist singleton exceptions

Extend the allowlist pattern from `passesReviewDerivedGate` to permit specific kinds of singletons (e.g., strong org rules, critical anti-patterns).

**Pros:**
- Already have the pattern in `passesReviewDerivedGate`
- Lets genuinely critical one-off discoveries through

**Cons:**
- More allowlist entries = more maintenance; edge cases multiply
- Deciding what qualifies as "genuinely critical" is another judgment call

### Option 3: Down-rank singleton records in scoring

Instead of hard-filtering, penalize records with `len(SourceSessionIDs) == 1` in the scoring/selection phase so they only win if nothing better exists.

**Pros:**
- Softer than a hard gate — singletons can still win if nothing better exists
- Preserves cold-start memories as low-priority fallbacks

**Cons:**
- Still relying on LLM-provided session IDs being accurate
- More scoring complexity; harder to explain why a memory was/wasn't selected

### Option 4: Show evidence count in TUI before promotion

Display session count / evidence strength in the TUI so users can make informed promote/reject decisions.

**Pros:**
- Zero risk — purely informational
- Lets users make informed decisions

**Cons:**
- Doesn't fix the problem, just makes it visible
- Users may not check
- **Does not protect the auto-activation path**: Personal-scope records under `auto` activation policy become active immediately without TUI review (`memoryloop.go:623`). The most problematic one-off memories can inject without any human gate at all.

---

## Recommended Approach: Explicit provenance + hard gate on deterministic inputs

### Why the original cross-reference proposal is insufficient

The original recommendation was to cross-reference generated records against upstream `PatternAnalysis` signals. Codex review identified three critical problems:

1. **No provenance link exists.** Generated records carry freeform `title`/`body` plus optional `source_session_ids`, but there is no `source_signal_type` or `source_signal_key` field. Mapping a generated memory back to the analysis signal it came from requires fuzzy text matching — which produces false positives (blocking good memories) and false negatives (passing bad ones). The existing `passesReviewDerivedGate` only works because it compares against a dedicated review-rule field, not because the system generally tracks provenance.

2. **The "allow if unmatched" fallback nullifies the gate.** The bad cases we're trying to eliminate are precisely the ones that won't map cleanly to a repeated signal. If unmatched records pass through, the one-off memory problem remains for the hardest-to-catch cases.

3. **Auto-activation bypasses any TUI-based safety net.** Personal-scope records under `auto` activation policy inject without human review, so "show evidence count in TUI" doesn't protect the most problematic path.

### Revised recommendation: Two-layer approach

#### Layer 1: Add explicit provenance to generated records (structural fix)

Add a `source_signal` field to the generation schema so the LLM must declare which analysis signal each record derives from. This makes the cross-reference deterministic instead of fuzzy.

**Schema change in the prompt:**

```json
{
  "records": [{
    "kind": "repo_rule|workflow_rule|agent_instruction|skill_patch|anti_pattern",
    "title": "short title",
    "body": "one actionable sentence",
    "source_signal": {
      "type": "repeated_instruction|missing_context|failure_loop|skill_opportunity|review_derived_rule",
      "key": "the signal value or rule text this record derives from"
    },
    "source_session_ids": ["checkpoint-id"],
    "confidence": "high|medium",
    "strength": 3
  }]
}
```

In the preferred implementation, `source_session_ids` remains the field name for compatibility, but its values should be checkpoint IDs so they align with `PatternAnalysis.AffectedSessions`.

**Post-filter logic:** Look up `source_signal.key` in the corresponding `PatternAnalysis` signal list (by type). If the signal exists and has `Count >= 2` (or is `Strong` for review-derived rules), the record is eligible to continue. If the signal doesn't exist in the analysis or has `Count < 2`, reject.

This eliminates fuzzy matching — the LLM provides an explicit key, and we do a constrained normalized lookup against the analysis we already computed.

**Lookup policy:** Use normalized exact match first. If that fails, allow a conservative normalized-contains fallback only when one string fully contains the other and the match is unique within that signal category. Do not use general semantic/fuzzy similarity. The goal is to tolerate small LLM paraphrases without reopening the false-positive problem.

**Risk:** The LLM can still hallucinate a `source_signal.key` that doesn't exist. But unlike the fuzzy approach, a non-matching key results in a **rejection** (not a pass-through), which is the correct failure mode.

**Important constraint:** `RepoLearnings` and `WorkflowLearnings` in today's `PatternAnalysis` are plain `[]string`, not counted signals with `Count`/`AffectedSessions`. They should therefore **not** appear in `source_signal.type` unless the analyzer is upgraded to emit structured counted variants. Until then, memories derived only from those unstructured learning lists must either:
- be rejected by the hard gate, or
- be converted into a counted signal upstream before generation.

**Prompt hygiene follow-on:** The current generator prompt still includes `repo_learning:` and `workflow_learning:` lines in the `<analysis>` block. If this design is implemented as written, those lines should either be removed from the generation prompt or explicitly marked as ineligible for `source_signal` attribution, otherwise the model will keep trying to synthesize memories from inputs that cannot survive the evidence gate.

#### Layer 2: Hard gate on `len(SourceSessionIDs) >= 2` as backup (deterministic floor)

For any record that passes Layer 1, require `len(SourceSessionIDs) >= 2` as a second gate. This is not an alternative to provenance matching; it is an additional floor.

**Exceptions (allowlisted singletons):**
- `ReviewDerivedRuleSignal` with `Strong == true` (existing behavior)
- `skill_patch` kind where `SkillOpportunity.Count >= 2` (the skill was flagged in multiple sessions even if this specific patch suggestion is new)

**Identifier alignment requirement:** This gate only works if `source_session_ids` and `AffectedSessions` use the same identifier type. Today the prompt exposes recent sessions by `SessionID`, while `PatternAnalysis.AffectedSessions` is populated from `CheckpointID`. Before implementing this validation, we must choose one canonical identifier and use it end-to-end:
- either change the prompt/schema so `source_session_ids` contains checkpoint IDs, or
- change analysis to track session IDs instead of checkpoint IDs.

Without that alignment, the validation step becomes a false-negative machine.

**Why this is acceptable despite LLM-provided session IDs:** The LLM can pad the list, but:
- Once identifiers are aligned, we can validate that cited IDs both:
  - exist in `input.Sessions`, and
  - belong to the matched upstream signal's `AffectedSessions`
- A record citing 2+ valid session IDs is more likely to represent a real pattern than one citing 1
- Combined with Layer 1 provenance, this is a belt-and-suspenders check, not the sole gate

#### Fallback behavior: reject, not allow

The key difference from the original proposal: **if a record can't be traced to a repeated signal, it is rejected.** If it can be traced to a repeated signal but does not cite 2+ valid aligned IDs from that signal's `AffectedSessions`, it is also rejected. No "allow if unmatched" escape hatch.

This is a stricter filter that will drop some good memories during cold-start. That's an acceptable tradeoff — a memory system that's too quiet is better than one that's noisy, because users lose trust in noisy systems and disable them entirely.

### Key files to modify

- `cmd/entire/cli/memoryloop/generator.go`:
  - Update `generateRecord` struct to include `SourceSignal` field
  - Update `BuildPrompt` to request `source_signal` in the JSON schema
  - Change the prompt/schema so `source_session_ids` uses the same identifier type as `PatternAnalysis.AffectedSessions` (prefer checkpoint IDs unless analysis is changed)
  - Remove or explicitly mark `repo_learning` / `workflow_learning` prompt inputs as ineligible for provenance-backed generation
  - **Remove `passesReviewDerivedGate()`** and its helpers (`bestMatchingReviewDerivedRule`, `generatedMatchesReviewEvidence`) — `passesEvidenceGate()` subsumes this logic via `lookupSignal` + `AllowsSingleton` for review-derived rules. Keeping both would double-filter and risk conflicting decisions.
  - Add `passesEvidenceGate()` function in `buildGeneratedRecordsDetailed` (replaces the `passesReviewDerivedGate` call site at line 182)
  - Add source-ID validation (check cited IDs exist in `input.Sessions` and in the matched signal's `AffectedSessions`)
  - Extend `GenerationStats` with a dedicated counter such as `FilteredNoEvidenceCount` and increment it when the new gate rejects a record, instead of folding those drops into `FilteredGenericCount`
- `cmd/entire/cli/improve/improve.go`: No structural changes needed for counted signal types, but the implementation must respect that `AffectedSessions` currently carries checkpoint IDs
- `cmd/entire/cli/memoryloop/generator_test.go`: Test cases for provenance matching, session ID validation, rejection of unmatched records

### Implementation sketch

```go
type sourceSignal struct {
    Type string `json:"type"`
    Key  string `json:"key"`
}

type generateRecord struct {
    // ... existing fields ...
    SourceSignal *sourceSignal `json:"source_signal,omitempty"`
}

type matchedSignal struct {
    Kind             string
    Count            int
    AllowsSingleton  bool
    AffectedSessions []string
}

func (m matchedSignal) AffectedSessionSet() map[string]bool {
    out := make(map[string]bool, len(m.AffectedSessions))
    for _, id := range m.AffectedSessions {
        out[id] = true
    }
    return out
}

func passesEvidenceGate(record MemoryRecord, signal *sourceSignal, analysis improve.PatternAnalysis, validSourceIDs map[string]bool) bool {
    // Layer 1: provenance check (required)
    if signal != nil && signal.Key != "" {
        if matched, found := lookupSignal(signal, analysis); found {
            allowSingleton := matched.AllowsSingleton
            if matched.Kind == "skill_opportunity" && record.Kind != KindSkillPatch {
                allowSingleton = false
            }
            if !allowSingleton && matched.Count < 2 {
                return false
            }
            // Layer 2: cited source IDs must be valid AND belong to this signal.
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
            return validCount >= 2
        }
        // Signal key doesn't match anything in analysis → reject
        return false
    }

    // No provenance available → reject
    return false
}

func lookupSignal(signal *sourceSignal, analysis improve.PatternAnalysis) (matchedSignal, bool) {
    normalized := normalizeGeneratedText(signal.Key)
    switch signal.Type {
    case "repeated_instruction":
        return findSignal(normalized, analysis.RepeatedInstructions)
    case "missing_context":
        return findSignal(normalized, analysis.MissingContextSignals)
    case "failure_loop":
        return findSignal(normalized, analysis.FailureLoops)
    case "skill_opportunity":
        return findSkill(normalized, analysis.SkillOpportunities)
    case "review_derived_rule":
        return findReviewRule(normalized, analysis.ReviewDerivedRules)
    default:
        return matchedSignal{}, false
    }
}
```

Where:
- `lookupSignal` uses normalized exact match first, then a conservative unique normalized-contains fallback within the same signal category.
- `matchedSignal.Kind` distinguishes cases like `skill_opportunity` from `review_derived_rule`, so singleton eligibility can stay narrowly scoped instead of applying to every record that happened to match the same upstream signal.
- the call site should increment `stats.FilteredNoEvidenceCount++` when `passesEvidenceGate` returns false, so this new filter is observable separately from weak/generic filtering.
