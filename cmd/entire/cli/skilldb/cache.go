package skilldb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/transcript"

	"github.com/go-git/go-git/v6"
)

// cacheVersion is bumped when matching logic changes to force cache invalidation.
// v2: added ResolveSkillName fuzzy matching for LLM-extracted signal names.
// v3: attribute Skill tool sessions by reading committed transcripts when skill_signals are absent.
// v4: carry friction from skill_signals into transcript-attributed sessions (Step 2).
// v5: discover plugin skills from ~/.claude/plugins/cache/.
// v6: improved name resolution (space/paren boundaries) + content fingerprint cache key.
const cacheVersion = "v6"

// resolveSkillName matches an LLM-extracted skill signal name to a discovered skill.
// LLM-extracted names often differ from canonical names (e.g. "e2e:triage" for skill "e2e",
// "superpowers:writing-plans" for "writing-plans", "cli-e2e-failure-fix" for "e2e").
//
// Match priority:
//  1. Exact match
//  2. Signal is a sub-skill: "parent:child" where parent matches a discovered skill
//  3. Last segment match: "prefix:name" where name matches a discovered skill
//  4. Discovered skill name is contained in the signal name as a word boundary
func ResolveSkillName(signalName string, skillMap map[string]SkillRow) (SkillRow, bool) {
	// 1. Exact match.
	if skill, ok := skillMap[signalName]; ok {
		return skill, true
	}

	// 2. Sub-skill: signal "e2e:triage" matches discovered "e2e".
	if idx := strings.IndexByte(signalName, ':'); idx > 0 {
		parent := signalName[:idx]
		if skill, ok := skillMap[parent]; ok {
			return skill, true
		}
	}

	// 3. Last segment: signal "superpowers:writing-plans" matches discovered "writing-plans".
	if idx := strings.LastIndexByte(signalName, ':'); idx >= 0 && idx < len(signalName)-1 {
		lastSeg := signalName[idx+1:]
		if skill, ok := skillMap[lastSeg]; ok {
			return skill, true
		}

		// 3b. Strip parenthetical suffix: "review (branch)" → "review".
		if parenIdx := strings.IndexByte(lastSeg, '('); parenIdx > 0 {
			trimmed := strings.TrimSpace(lastSeg[:parenIdx])
			if skill, ok := skillMap[trimmed]; ok {
				return skill, true
			}
		}
	}

	// 4. Discovered skill name appears as a delimited segment in the signal.
	// E.g. signal "cli-e2e-failure-fix" contains discovered skill "e2e".
	// We check word boundaries using "-" and ":" as delimiters.
	for name, skill := range skillMap {
		if len(name) < 2 {
			continue // skip very short names to avoid false positives
		}
		// Check if name appears bounded by delimiters or string edges.
		searchIn := signalName
		for {
			idx := strings.Index(searchIn, name)
			if idx < 0 {
				break
			}
			leftOK := idx == 0 || searchIn[idx-1] == '-' || searchIn[idx-1] == ':' || searchIn[idx-1] == ' '
			rightEnd := idx + len(name)
			rightOK := rightEnd == len(searchIn) || searchIn[rightEnd] == '-' || searchIn[rightEnd] == ':' || searchIn[rightEnd] == ' ' || searchIn[rightEnd] == '('
			if leftOK && rightOK {
				return skill, true
			}
			searchIn = searchIn[idx+1:]
		}
	}

	return SkillRow{}, false
}

const (
	outcomeSuccess  = "success"
	outcomeFriction = "friction"
)

// PopulateResult captures pipeline metrics from PopulateFromInsightsDB
// for diagnostics and TUI display.
type PopulateResult struct {
	Step1SignalCount     int      // skill_signals rows queried
	Step1Resolved        int      // signals matched to discovered skills
	Step1Inserted        int      // sessions inserted from Step 1
	Step2ToolCallCount   int      // tool_call sessions queried
	Step2TranscriptsRead int      // transcripts successfully read
	Step2SkillsExtracted int      // skill invocations extracted from transcripts
	Step2Resolved        int      // extracted skills matched to discovered skills
	Step2Inserted        int      // sessions inserted from Step 2
	UnresolvedNames      []string // signal/skill names that failed resolution
}

// PopulateFromInsightsDB populates the skill analytics DB from insightsdb data.
// It queries skill_signals and tool_calls to find sessions that used discovered skills.
// Returns a PopulateResult with pipeline metrics for diagnostics.
func (sdb *SkillDB) PopulateFromInsightsDB(ctx context.Context, idb *insightsdb.InsightsDB, discoveredSkills []SkillRow, repoRoot string) (*PopulateResult, error) {
	result := &PopulateResult{}
	if len(discoveredSkills) == 0 {
		return result, nil
	}

	// Collect skill names for querying.
	skillNames := make([]string, len(discoveredSkills))
	skillMap := make(map[string]SkillRow, len(discoveredSkills))
	for i, s := range discoveredSkills {
		skillNames[i] = s.Name
		skillMap[s.Name] = s
	}

	tx, err := sdb.BeginTx(ctx)
	if err != nil {
		return result, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback() //nolint:errcheck // Rollback after failed tx; error is irrelevant
		}
	}()

	// Track which sessions we've already inserted to avoid duplicates.
	type sessionKey struct {
		skillName    string
		checkpointID string
		sessionIndex int
	}
	inserted := make(map[sessionKey]bool)

	// Step 1: From skill_signals — sessions with friction for specific skills.
	// Fetch all signals and resolve names in Go, since LLM-extracted names
	// often differ from canonical discovered names.
	signals, err := idb.QueryAllSkillSignals(ctx)
	if err != nil {
		return result, fmt.Errorf("query skill signals: %w", err)
	}
	result.Step1SignalCount = len(signals)

	unresolvedSet := make(map[string]bool)
	for _, sig := range signals {
		skill, ok := ResolveSkillName(sig.SkillName, skillMap)
		if !ok {
			logging.Debug(ctx, "skill signal name not matched to any discovered skill",
				"signal_name", sig.SkillName)
			if !unresolvedSet[sig.SkillName] {
				unresolvedSet[sig.SkillName] = true
				result.UnresolvedNames = append(result.UnresolvedNames, sig.SkillName)
			}
			continue
		}
		result.Step1Resolved++

		key := sessionKey{skill.Name, sig.CheckpointID, sig.SessionIndex}
		if inserted[key] {
			continue
		}
		inserted[key] = true
		result.Step1Inserted++

		frictionCount := len(sig.Friction)
		outcome := outcomeSuccess
		if frictionCount > 0 {
			outcome = outcomeFriction
		}

		if err = sdb.InsertSessionTx(ctx, tx, SkillSessionRow{
			SkillName:     skill.Name,
			SourceAgent:   skill.SourceAgent,
			CheckpointID:  sig.CheckpointID,
			SessionIndex:  sig.SessionIndex,
			SessionID:     sig.SessionID,
			Agent:         sig.Agent,
			Model:         sig.Model,
			Branch:        sig.Branch,
			CreatedAt:     sig.CreatedAt,
			TotalTokens:   sig.TotalTokens,
			TurnCount:     sig.TurnCount,
			OverallScore:  sig.OverallScore,
			FrictionCount: frictionCount,
			Outcome:       outcome,
		}); err != nil {
			return result, fmt.Errorf("insert skill session: %w", err)
		}

		// Insert friction items.
		for _, f := range sig.Friction {
			if err = sdb.InsertFrictionTx(ctx, tx,
				skill.Name, skill.SourceAgent,
				sig.CheckpointID, sig.SessionIndex,
				f, "",
			); err != nil {
				return result, fmt.Errorf("insert skill friction: %w", err)
			}
		}

		// Insert missing instruction if present.
		if sig.MissingInstruction != "" {
			evidence := strings.Join(sig.Friction, "\n")
			if err = sdb.InsertMissingInstructionTx(ctx, tx,
				skill.Name, skill.SourceAgent,
				sig.CheckpointID, sig.SessionIndex,
				sig.MissingInstruction, evidence,
			); err != nil {
				return result, fmt.Errorf("insert missing instruction: %w", err)
			}
		}
	}

	// Build a friction lookup from skill_signals, keyed by (checkpoint_id, session_index).
	// Step 2 uses this to carry friction into transcript-attributed sessions whose
	// LLM-extracted skill names didn't resolve in Step 1.
	type signalKey struct {
		checkpointID string
		sessionIndex int
	}
	signalsBySession := make(map[signalKey]insightsdb.SkillSignalRow, len(signals))
	for _, sig := range signals {
		sk := signalKey{sig.CheckpointID, sig.SessionIndex}
		// Prefer the signal with friction data if multiple exist for the same session.
		if existing, ok := signalsBySession[sk]; !ok || len(existing.Friction) == 0 {
			signalsBySession[sk] = sig
		}
	}

	// Step 2: From tool_calls — sessions that used the Skill tool (friction-free uses).
	toolSessions, err := idb.QuerySkillToolCallSessions(ctx)
	if err != nil {
		return result, fmt.Errorf("query skill tool call sessions: %w", err)
	}
	result.Step2ToolCallCount = len(toolSessions)

	var store *checkpoint.GitStore
	getStore := func() (*checkpoint.GitStore, error) {
		if store != nil {
			return store, nil
		}
		repo, openErr := git.PlainOpenWithOptions(repoRoot, &git.PlainOpenOptions{DetectDotGit: true})
		if openErr != nil {
			return nil, fmt.Errorf("open repository for transcript fallback: %w", openErr)
		}
		store = checkpoint.NewGitStore(repo)
		return store, nil
	}

	for _, ts := range toolSessions {
		var matchedTranscriptSkill bool

		cpID, cpErr := id.NewCheckpointID(ts.CheckpointID)
		if cpErr == nil {
			store, storeErr := getStore()
			if storeErr != nil {
				return result, storeErr
			}

			content, readErr := store.ReadSessionContent(ctx, cpID, ts.SessionIndex)
			if readErr == nil && content != nil && len(content.Transcript) > 0 {
				result.Step2TranscriptsRead++
				invokedSkills, extractErr := extractSkillInvocationsFromTranscript(content.Transcript)
				if extractErr != nil {
					return result, fmt.Errorf("extract skill invocations from transcript for %s/%d: %w",
						ts.CheckpointID, ts.SessionIndex, extractErr)
				}
				result.Step2SkillsExtracted += len(invokedSkills)

				for _, invokedSkill := range invokedSkills {
					skill, ok := ResolveSkillName(invokedSkill, skillMap)
					if !ok {
						if !unresolvedSet[invokedSkill] {
							unresolvedSet[invokedSkill] = true
							result.UnresolvedNames = append(result.UnresolvedNames, invokedSkill)
						}
						continue
					}
					result.Step2Resolved++

					key := sessionKey{skill.Name, ts.CheckpointID, ts.SessionIndex}
					if inserted[key] {
						continue
					}
					inserted[key] = true
					matchedTranscriptSkill = true
					result.Step2Inserted++

					// Look up friction from skill_signals for this session.
					sk := signalKey{ts.CheckpointID, ts.SessionIndex}
					sig, hasFriction := signalsBySession[sk]
					frictionCount := 0
					outcome := outcomeSuccess
					if hasFriction && len(sig.Friction) > 0 {
						frictionCount = len(sig.Friction)
						outcome = outcomeFriction
					}

					if err = sdb.InsertSessionTx(ctx, tx, SkillSessionRow{
						SkillName:     skill.Name,
						SourceAgent:   skill.SourceAgent,
						CheckpointID:  ts.CheckpointID,
						SessionIndex:  ts.SessionIndex,
						SessionID:     ts.SessionID,
						Agent:         ts.Agent,
						Model:         ts.Model,
						Branch:        ts.Branch,
						CreatedAt:     ts.CreatedAt,
						TotalTokens:   ts.TotalTokens,
						TurnCount:     ts.TurnCount,
						OverallScore:  ts.OverallScore,
						FrictionCount: frictionCount,
						Outcome:       outcome,
					}); err != nil {
						return result, fmt.Errorf("insert transcript-attributed skill session: %w", err)
					}

					if hasFriction {
						for _, f := range sig.Friction {
							if err = sdb.InsertFrictionTx(ctx, tx,
								skill.Name, skill.SourceAgent,
								ts.CheckpointID, ts.SessionIndex,
								f, "",
							); err != nil {
								return result, fmt.Errorf("insert transcript-attributed skill friction: %w", err)
							}
						}
						if sig.MissingInstruction != "" {
							evidence := strings.Join(sig.Friction, "\n")
							if err = sdb.InsertMissingInstructionTx(ctx, tx,
								skill.Name, skill.SourceAgent,
								ts.CheckpointID, ts.SessionIndex,
								sig.MissingInstruction, evidence,
							); err != nil {
								return result, fmt.Errorf("insert transcript-attributed missing instruction: %w", err)
							}
						}
					}
				}
			}
		}

		if matchedTranscriptSkill {
			continue
		}

		// We can't determine which specific skill was used from tool_calls alone,
		// so we skip sessions already covered by skill_signals.
		// These sessions indicate the Skill tool was invoked but we only record
		// them if there's exactly one discovered skill (unambiguous attribution).
		if len(discoveredSkills) == 1 {
			skill := discoveredSkills[0]
			key := sessionKey{skill.Name, ts.CheckpointID, ts.SessionIndex}
			if inserted[key] {
				continue
			}
			inserted[key] = true
			result.Step2Inserted++

			// Look up friction from skill_signals for this session.
			sk := signalKey{ts.CheckpointID, ts.SessionIndex}
			sig, hasFriction := signalsBySession[sk]
			frictionCount := 0
			outcome := outcomeSuccess
			if hasFriction && len(sig.Friction) > 0 {
				frictionCount = len(sig.Friction)
				outcome = outcomeFriction
			}

			if err = sdb.InsertSessionTx(ctx, tx, SkillSessionRow{
				SkillName:     skill.Name,
				SourceAgent:   skill.SourceAgent,
				CheckpointID:  ts.CheckpointID,
				SessionIndex:  ts.SessionIndex,
				SessionID:     ts.SessionID,
				Agent:         ts.Agent,
				Model:         ts.Model,
				Branch:        ts.Branch,
				CreatedAt:     ts.CreatedAt,
				TotalTokens:   ts.TotalTokens,
				TurnCount:     ts.TurnCount,
				OverallScore:  ts.OverallScore,
				FrictionCount: frictionCount,
				Outcome:       outcome,
			}); err != nil {
				return result, fmt.Errorf("insert tool call session: %w", err)
			}

			if hasFriction {
				for _, f := range sig.Friction {
					if err = sdb.InsertFrictionTx(ctx, tx,
						skill.Name, skill.SourceAgent,
						ts.CheckpointID, ts.SessionIndex,
						f, "",
					); err != nil {
						return result, fmt.Errorf("insert tool-call-attributed skill friction: %w", err)
					}
				}
				if sig.MissingInstruction != "" {
					evidence := strings.Join(sig.Friction, "\n")
					if err = sdb.InsertMissingInstructionTx(ctx, tx,
						skill.Name, skill.SourceAgent,
						ts.CheckpointID, ts.SessionIndex,
						sig.MissingInstruction, evidence,
					); err != nil {
						return result, fmt.Errorf("insert tool-call-attributed missing instruction: %w", err)
					}
				}
			}
		}
	}

	logging.Info(ctx, "skill cache populated",
		"step1_signals", result.Step1SignalCount,
		"step1_resolved", result.Step1Resolved,
		"step1_inserted", result.Step1Inserted,
		"step2_tool_calls", result.Step2ToolCallCount,
		"step2_transcripts_read", result.Step2TranscriptsRead,
		"step2_skills_extracted", result.Step2SkillsExtracted,
		"step2_resolved", result.Step2Resolved,
		"step2_inserted", result.Step2Inserted,
		"unresolved_count", len(result.UnresolvedNames),
	)

	if err = tx.Commit(); err != nil {
		return result, fmt.Errorf("commit transaction: %w", err)
	}
	return result, nil
}

// RefreshFromInsightsDB checks if the cache is stale and repopulates if needed.
// Returns the populate result (nil if cache was fresh) and whether a refresh occurred.
func (sdb *SkillDB) RefreshFromInsightsDB(ctx context.Context, idb *insightsdb.InsightsDB, discoveredSkills []SkillRow, repoRoot string) (*PopulateResult, bool, error) {
	// Check insightsdb branch tip with cache version prefix.
	// Bumping cacheVersion forces re-population even if the branch tip hasn't changed,
	// e.g. after fixing the skill name matching logic.
	currentTip, err := idb.GetBranchTip(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("get insightsdb branch tip: %w", err)
	}

	// Include a content fingerprint so the cache refreshes when facets are
	// backfilled (adding skill_signals) or new tool_calls are ingested,
	// even if the branch tip hasn't changed.
	fingerprint, err := idb.GetContentFingerprint(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("get insightsdb content fingerprint: %w", err)
	}

	versionedTip := cacheVersion + ":" + currentTip + ":" + fingerprint

	cachedTip, err := sdb.GetCacheTip(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("get skilldb cache tip: %w", err)
	}

	if versionedTip != "" && versionedTip == cachedTip {
		return nil, false, nil
	}

	// Upsert discovered skills.
	now := time.Now().UTC()
	for _, skill := range discoveredSkills {
		if err = sdb.UpsertSkill(ctx, SkillRow{
			Name:         skill.Name,
			SourceAgent:  skill.SourceAgent,
			Path:         skill.Path,
			Kind:         skill.Kind,
			DiscoveredAt: now,
			LastSeenAt:   now,
		}); err != nil {
			return nil, false, fmt.Errorf("upsert skill %q: %w", skill.Name, err)
		}
	}

	// Clear existing session data before repopulating.
	if err = sdb.clearSessionData(ctx); err != nil {
		return nil, false, fmt.Errorf("clear session data: %w", err)
	}

	// Populate from insightsdb.
	populateResult, err := sdb.PopulateFromInsightsDB(ctx, idb, discoveredSkills, repoRoot)
	if err != nil {
		return populateResult, false, fmt.Errorf("populate from insightsdb: %w", err)
	}

	// Update cache tip with version prefix.
	if versionedTip != "" {
		if err = sdb.SetCacheTip(ctx, versionedTip); err != nil {
			return populateResult, false, fmt.Errorf("set cache tip: %w", err)
		}
	}

	return populateResult, true, nil
}

// clearSessionData removes all rows from session-related tables.
func (sdb *SkillDB) clearSessionData(ctx context.Context) error {
	tables := []string{"skill_sessions", "skill_friction", "skill_missing_instructions"}
	for _, table := range tables {
		if _, err := sdb.db.ExecContext(ctx, "DELETE FROM "+table); err != nil { //nolint:gosec // table names are hardcoded
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	return nil
}

func extractSkillInvocationsFromTranscript(content []byte) ([]string, error) {
	lines, err := transcript.ParseFromBytes(content)
	if err != nil {
		return nil, fmt.Errorf("parse transcript: %w", err)
	}

	seen := make(map[string]bool)
	var skills []string

	for _, line := range lines {
		if line.Type != transcript.TypeAssistant {
			continue
		}

		var msg transcript.AssistantMessage
		if err := json.Unmarshal(line.Message, &msg); err != nil {
			continue
		}

		for _, block := range msg.Content {
			if block.Type != transcript.ContentTypeToolUse || block.Name != "Skill" {
				continue
			}

			var input transcript.ToolInput
			if err := json.Unmarshal(block.Input, &input); err != nil {
				continue
			}

			skill := strings.TrimSpace(input.Skill)
			if skill == "" || seen[skill] {
				continue
			}

			seen[skill] = true
			skills = append(skills, skill)
		}
	}

	return skills, nil
}
