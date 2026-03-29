package skilldb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/insightsdb"
	"github.com/entireio/cli/cmd/entire/cli/logging"
)

// cacheVersion is bumped when matching logic changes to force cache invalidation.
// v2: added ResolveSkillName fuzzy matching for LLM-extracted signal names.
const cacheVersion = "v2"

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
			leftOK := idx == 0 || searchIn[idx-1] == '-' || searchIn[idx-1] == ':'
			rightEnd := idx + len(name)
			rightOK := rightEnd == len(searchIn) || searchIn[rightEnd] == '-' || searchIn[rightEnd] == ':'
			if leftOK && rightOK {
				return skill, true
			}
			searchIn = searchIn[idx+1:]
		}
	}

	return SkillRow{}, false
}

// PopulateFromInsightsDB populates the skill analytics DB from insightsdb data.
// It queries skill_signals and tool_calls to find sessions that used discovered skills.
func (sdb *SkillDB) PopulateFromInsightsDB(ctx context.Context, idb *insightsdb.InsightsDB, discoveredSkills []SkillRow) error {
	if len(discoveredSkills) == 0 {
		return nil
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
		return fmt.Errorf("begin transaction: %w", err)
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
		return fmt.Errorf("query skill signals: %w", err)
	}

	for _, sig := range signals {
		skill, ok := ResolveSkillName(sig.SkillName, skillMap)
		if !ok {
			logging.Debug(ctx, "skill signal name not matched to any discovered skill",
				"signal_name", sig.SkillName)
			continue
		}

		key := sessionKey{skill.Name, sig.CheckpointID, sig.SessionIndex}
		if inserted[key] {
			continue
		}
		inserted[key] = true

		frictionCount := len(sig.Friction)
		outcome := "success"
		if frictionCount > 0 {
			outcome = "friction"
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
			return fmt.Errorf("insert skill session: %w", err)
		}

		// Insert friction items.
		for _, f := range sig.Friction {
			if err = sdb.InsertFrictionTx(ctx, tx,
				skill.Name, skill.SourceAgent,
				sig.CheckpointID, sig.SessionIndex,
				f, "",
			); err != nil {
				return fmt.Errorf("insert skill friction: %w", err)
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
				return fmt.Errorf("insert missing instruction: %w", err)
			}
		}
	}

	// Step 2: From tool_calls — sessions that used the Skill tool (friction-free uses).
	toolSessions, err := idb.QuerySkillToolCallSessions(ctx)
	if err != nil {
		return fmt.Errorf("query skill tool call sessions: %w", err)
	}

	for _, ts := range toolSessions {
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

			if err = sdb.InsertSessionTx(ctx, tx, SkillSessionRow{
				SkillName:    skill.Name,
				SourceAgent:  skill.SourceAgent,
				CheckpointID: ts.CheckpointID,
				SessionIndex: ts.SessionIndex,
				SessionID:    ts.SessionID,
				Agent:        ts.Agent,
				Model:        ts.Model,
				Branch:       ts.Branch,
				CreatedAt:    ts.CreatedAt,
				TotalTokens:  ts.TotalTokens,
				TurnCount:    ts.TurnCount,
				OverallScore: ts.OverallScore,
				Outcome:      "success",
			}); err != nil {
				return fmt.Errorf("insert tool call session: %w", err)
			}
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// RefreshFromInsightsDB checks if the cache is stale and repopulates if needed.
// Returns true if the cache was refreshed.
func (sdb *SkillDB) RefreshFromInsightsDB(ctx context.Context, idb *insightsdb.InsightsDB, discoveredSkills []SkillRow) (bool, error) {
	// Check insightsdb branch tip with cache version prefix.
	// Bumping cacheVersion forces re-population even if the branch tip hasn't changed,
	// e.g. after fixing the skill name matching logic.
	currentTip, err := idb.GetBranchTip(ctx)
	if err != nil {
		return false, fmt.Errorf("get insightsdb branch tip: %w", err)
	}

	versionedTip := cacheVersion + ":" + currentTip

	cachedTip, err := sdb.GetCacheTip(ctx)
	if err != nil {
		return false, fmt.Errorf("get skilldb cache tip: %w", err)
	}

	if versionedTip != "" && versionedTip == cachedTip {
		return false, nil
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
			return false, fmt.Errorf("upsert skill %q: %w", skill.Name, err)
		}
	}

	// Clear existing session data before repopulating.
	if err = sdb.clearSessionData(ctx); err != nil {
		return false, fmt.Errorf("clear session data: %w", err)
	}

	// Populate from insightsdb.
	if err = sdb.PopulateFromInsightsDB(ctx, idb, discoveredSkills); err != nil {
		return false, fmt.Errorf("populate from insightsdb: %w", err)
	}

	// Update cache tip with version prefix.
	if versionedTip != "" {
		if err = sdb.SetCacheTip(ctx, versionedTip); err != nil {
			return false, fmt.Errorf("set cache tip: %w", err)
		}
	}

	return true, nil
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
