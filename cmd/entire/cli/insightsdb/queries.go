package insightsdb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/facets"
)

// FrictionTheme groups recurring friction entries by their text content.
type FrictionTheme struct {
	Text     string   `json:"text"`
	Count    int      `json:"count"`
	Sessions []string `json:"sessions"` // checkpoint IDs where this friction occurred
}

// QueryLastNSessions returns the most recent N sessions ordered by created_at DESC.
// Denormalized fields (FilesTouched, Friction, Learnings) are populated.
func (idb *InsightsDB) QueryLastNSessions(ctx context.Context, n int) ([]SessionRow, error) {
	return idb.querySessions(ctx,
		"SELECT "+sessionColumns+" FROM sessions ORDER BY created_at DESC LIMIT ?",
		n,
	)
}

// QueryByAgent returns sessions filtered by agent name, most recent first.
func (idb *InsightsDB) QueryByAgent(ctx context.Context, agent string, limit int) ([]SessionRow, error) {
	return idb.querySessions(ctx,
		"SELECT "+sessionColumns+" FROM sessions WHERE agent = ? ORDER BY created_at DESC LIMIT ?",
		agent, limit,
	)
}

// QueryByBranch returns sessions filtered by branch, most recent first.
func (idb *InsightsDB) QueryByBranch(ctx context.Context, branch string, limit int) ([]SessionRow, error) {
	return idb.querySessions(ctx,
		"SELECT "+sessionColumns+" FROM sessions WHERE branch = ? ORDER BY created_at DESC LIMIT ?",
		branch, limit,
	)
}

// QueryByOwnerID returns sessions filtered by owner ID, most recent first.
func (idb *InsightsDB) QueryByOwnerID(ctx context.Context, ownerID string, limit int) ([]SessionRow, error) {
	return idb.querySessions(ctx,
		"SELECT "+sessionColumns+" FROM sessions WHERE owner_id = ? ORDER BY created_at DESC LIMIT ?",
		ownerID, limit,
	)
}

// QueryByCheckpointPrefix returns sessions whose checkpoint ID starts with the given prefix, most recent first.
func (idb *InsightsDB) QueryByCheckpointPrefix(ctx context.Context, prefix string) ([]SessionRow, error) {
	return idb.querySessions(ctx,
		"SELECT "+sessionColumns+" FROM sessions WHERE checkpoint_id LIKE ? ORDER BY created_at DESC",
		prefix+"%",
	)
}

// SessionCount returns the total number of cached sessions.
func (idb *InsightsDB) SessionCount(ctx context.Context) (int, error) {
	var count int
	if err := idb.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&count); err != nil {
		return 0, fmt.Errorf("session count: %w", err)
	}
	return count, nil
}

// QueryRecurringFriction returns friction themes occurring at least minCount times,
// ordered by count descending.
func (idb *InsightsDB) QueryRecurringFriction(ctx context.Context, minCount int) ([]FrictionTheme, error) {
	rows, err := idb.db.QueryContext(ctx, `
		SELECT text, COUNT(*) AS cnt, GROUP_CONCAT(DISTINCT checkpoint_id) AS sessions
		FROM friction
		GROUP BY text
		HAVING cnt >= ?
		ORDER BY cnt DESC
	`, minCount)
	if err != nil {
		return nil, fmt.Errorf("query recurring friction: %w", err)
	}
	defer rows.Close()

	var themes []FrictionTheme
	for rows.Next() {
		var theme FrictionTheme
		var sessionsCSV string
		if err = rows.Scan(&theme.Text, &theme.Count, &sessionsCSV); err != nil {
			return nil, fmt.Errorf("scan friction theme: %w", err)
		}
		theme.Sessions = splitCSV(sessionsCSV)
		themes = append(themes, theme)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate friction themes: %w", err)
	}
	return themes, nil
}

// QuerySessionsWithFriction returns checkpoint IDs of sessions containing
// friction matching the given SQL LIKE pattern (e.g., "%tool call failed%").
func (idb *InsightsDB) QuerySessionsWithFriction(ctx context.Context, pattern string) ([]string, error) {
	rows, err := idb.db.QueryContext(ctx,
		"SELECT DISTINCT checkpoint_id FROM friction WHERE text LIKE ?",
		pattern,
	)
	if err != nil {
		return nil, fmt.Errorf("query sessions with friction: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan checkpoint id: %w", err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate session friction: %w", err)
	}
	return ids, nil
}

// sessionColumns is the ordered column list for SELECT queries on the sessions table.
const sessionColumns = `
	checkpoint_id, session_id, session_index,
	agent, model, branch, owner_name, owner_id, owner_email, created_at,
	input_tokens, cache_tokens, output_tokens, total_tokens,
	api_call_count, duration_ms, turn_count,
	intent, outcome, agent_percentage,
	overall_score, score_token_efficiency, score_first_pass,
	score_friction, score_focus, has_summary, has_facets`

// querySessions executes a SELECT on sessions with the given args,
// then populates denormalized fields for each row.
func (idb *InsightsDB) querySessions(ctx context.Context, query string, args ...interface{}) ([]SessionRow, error) {
	rows, err := idb.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}
	defer rows.Close()

	var sessions []SessionRow
	for rows.Next() {
		row, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, row)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}

	for i := range sessions {
		if err = idb.populateDenormalized(ctx, &sessions[i]); err != nil {
			return nil, err
		}
	}
	return sessions, nil
}

// scanSession reads one row from the sessions table into a SessionRow.
func scanSession(rows *sql.Rows) (SessionRow, error) {
	var row SessionRow
	var createdAt string
	var agent, model, branch, ownerName, ownerID, ownerEmail, intent, outcome sql.NullString
	var hasSummary, hasFacets int

	err := rows.Scan(
		&row.CheckpointID, &row.SessionID, &row.SessionIndex,
		&agent, &model, &branch, &ownerName, &ownerID, &ownerEmail, &createdAt,
		&row.InputTokens, &row.CacheTokens, &row.OutputTokens, &row.TotalTokens,
		&row.APICallCount, &row.DurationMs, &row.TurnCount,
		&intent, &outcome, &row.AgentPct,
		&row.OverallScore, &row.ScoreTokenEff, &row.ScoreFirstPass,
		&row.ScoreFriction, &row.ScoreFocus, &hasSummary, &hasFacets,
	)
	if err != nil {
		return row, fmt.Errorf("scan session row: %w", err)
	}

	row.Agent = agent.String
	row.Model = model.String
	row.Branch = branch.String
	row.OwnerName = ownerName.String
	row.OwnerID = ownerID.String
	row.OwnerEmail = ownerEmail.String
	row.Intent = intent.String
	row.Outcome = outcome.String
	row.HasSummary = hasSummary == 1
	row.HasFacets = hasFacets == 1

	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return row, fmt.Errorf("parse created_at %q: %w", createdAt, err)
	}
	row.CreatedAt = t
	return row, nil
}

// populateDenormalized loads denormalized fields for the session.
func (idb *InsightsDB) populateDenormalized(ctx context.Context, row *SessionRow) error {
	var err error
	row.FilesTouched, err = idb.loadFilesTouched(ctx, row.CheckpointID, row.SessionIndex)
	if err != nil {
		return err
	}
	row.Friction, err = idb.loadFriction(ctx, row.CheckpointID, row.SessionIndex)
	if err != nil {
		return err
	}
	row.Learnings, err = idb.loadLearnings(ctx, row.CheckpointID, row.SessionIndex)
	if err != nil {
		return err
	}
	row.ImplementationRationale, err = idb.loadStringSlice(
		ctx,
		"implementation_rationale",
		row.CheckpointID,
		row.SessionIndex,
	)
	if err != nil {
		return err
	}
	row.Tradeoffs, err = idb.loadStringSlice(ctx, "tradeoffs", row.CheckpointID, row.SessionIndex)
	if err != nil {
		return err
	}
	row.CodebasePatterns, err = idb.loadStringSlice(ctx, "codebase_patterns", row.CheckpointID, row.SessionIndex)
	if err != nil {
		return err
	}
	row.ToolCounts, err = idb.loadToolCalls(ctx, row.CheckpointID, row.SessionIndex)
	if err != nil {
		return err
	}
	row.Facets, err = idb.loadFacets(ctx, row.CheckpointID, row.SessionIndex)
	return err
}

func (idb *InsightsDB) loadFilesTouched(ctx context.Context, checkpointID string, sessionIndex int) ([]string, error) {
	rows, err := idb.db.QueryContext(ctx,
		"SELECT file_path FROM files_touched WHERE checkpoint_id = ? AND session_index = ?",
		checkpointID, sessionIndex,
	)
	if err != nil {
		return nil, fmt.Errorf("load files_touched: %w", err)
	}
	defer rows.Close()

	var files []string
	for rows.Next() {
		var f string
		if err = rows.Scan(&f); err != nil {
			return nil, fmt.Errorf("scan file_path: %w", err)
		}
		files = append(files, f)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate files_touched: %w", err)
	}
	return files, nil
}

func (idb *InsightsDB) loadFriction(ctx context.Context, checkpointID string, sessionIndex int) ([]string, error) {
	rows, err := idb.db.QueryContext(ctx,
		"SELECT text FROM friction WHERE checkpoint_id = ? AND session_index = ?",
		checkpointID, sessionIndex,
	)
	if err != nil {
		return nil, fmt.Errorf("load friction: %w", err)
	}
	defer rows.Close()

	var friction []string
	for rows.Next() {
		var f string
		if err = rows.Scan(&f); err != nil {
			return nil, fmt.Errorf("scan friction text: %w", err)
		}
		friction = append(friction, f)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate friction: %w", err)
	}
	return friction, nil
}

func (idb *InsightsDB) loadLearnings(ctx context.Context, checkpointID string, sessionIndex int) ([]LearningRow, error) {
	rows, err := idb.db.QueryContext(ctx,
		"SELECT scope, finding, path FROM learnings WHERE checkpoint_id = ? AND session_index = ?",
		checkpointID, sessionIndex,
	)
	if err != nil {
		return nil, fmt.Errorf("load learnings: %w", err)
	}
	defer rows.Close()

	var learnings []LearningRow
	for rows.Next() {
		var l LearningRow
		var path sql.NullString
		if err = rows.Scan(&l.Scope, &l.Finding, &path); err != nil {
			return nil, fmt.Errorf("scan learning: %w", err)
		}
		l.Path = path.String
		learnings = append(learnings, l)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate learnings: %w", err)
	}
	return learnings, nil
}

func (idb *InsightsDB) loadStringSlice(ctx context.Context, table, checkpointID string, sessionIndex int) ([]string, error) {
	rows, err := idb.db.QueryContext(ctx,
		"SELECT text FROM "+table+" WHERE checkpoint_id = ? AND session_index = ?", //nolint:gosec // table name is hardcoded
		checkpointID, sessionIndex,
	)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", table, err)
	}
	defer rows.Close()

	var values []string
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("scan %s: %w", table, err)
		}
		values = append(values, value)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s: %w", table, err)
	}
	return values, nil
}

func (idb *InsightsDB) loadToolCalls(ctx context.Context, checkpointID string, sessionIndex int) (map[string]int, error) {
	rows, err := idb.db.QueryContext(ctx,
		"SELECT tool_name, count FROM tool_calls WHERE checkpoint_id = ? AND session_index = ?",
		checkpointID, sessionIndex,
	)
	if err != nil {
		return nil, fmt.Errorf("load tool_calls: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var name string
		var count int
		if err = rows.Scan(&name, &count); err != nil {
			return nil, fmt.Errorf("scan tool_call: %w", err)
		}
		counts[name] = count
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tool_calls: %w", err)
	}
	return counts, nil
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func (idb *InsightsDB) loadFacets(ctx context.Context, checkpointID string, sessionIndex int) (facets.SessionFacets, error) {
	var result facets.SessionFacets
	var err error

	result.RepeatedUserInstructions, err = idb.loadRepeatedInstructions(ctx, checkpointID, sessionIndex)
	if err != nil {
		return result, err
	}
	result.MissingContext, err = idb.loadMissingContext(ctx, checkpointID, sessionIndex)
	if err != nil {
		return result, err
	}
	result.FailureLoops, err = idb.loadFailureLoops(ctx, checkpointID, sessionIndex)
	if err != nil {
		return result, err
	}
	result.SkillSignals, err = idb.loadSkillSignals(ctx, checkpointID, sessionIndex)
	if err != nil {
		return result, err
	}
	result.ReviewDerivedRules, err = idb.loadReviewDerivedRules(ctx, checkpointID, sessionIndex)
	if err != nil {
		return result, err
	}
	return result, nil
}

func (idb *InsightsDB) loadRepeatedInstructions(ctx context.Context, checkpointID string, sessionIndex int) ([]facets.RepeatedInstruction, error) {
	rows, err := idb.db.QueryContext(ctx,
		`SELECT instruction, evidence FROM repeated_user_instructions
		 WHERE checkpoint_id = ? AND session_index = ?`,
		checkpointID, sessionIndex,
	)
	if err != nil {
		return nil, fmt.Errorf("load repeated_user_instructions: %w", err)
	}
	defer rows.Close()

	var values []facets.RepeatedInstruction
	for rows.Next() {
		var instruction string
		var evidence sql.NullString
		if err = rows.Scan(&instruction, &evidence); err != nil {
			return nil, fmt.Errorf("scan repeated_user_instruction: %w", err)
		}
		values = append(values, facets.RepeatedInstruction{
			Instruction: instruction,
			Evidence:    splitEvidence(evidence.String),
		})
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate repeated_user_instructions: %w", err)
	}
	return values, nil
}

func (idb *InsightsDB) loadMissingContext(ctx context.Context, checkpointID string, sessionIndex int) ([]facets.MissingContextSignal, error) {
	rows, err := idb.db.QueryContext(ctx,
		`SELECT item, evidence FROM missing_context_signals
		 WHERE checkpoint_id = ? AND session_index = ?`,
		checkpointID, sessionIndex,
	)
	if err != nil {
		return nil, fmt.Errorf("load missing_context_signals: %w", err)
	}
	defer rows.Close()

	var values []facets.MissingContextSignal
	for rows.Next() {
		var item string
		var evidence sql.NullString
		if err = rows.Scan(&item, &evidence); err != nil {
			return nil, fmt.Errorf("scan missing_context_signal: %w", err)
		}
		values = append(values, facets.MissingContextSignal{
			Item:     item,
			Evidence: splitEvidence(evidence.String),
		})
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate missing_context_signals: %w", err)
	}
	return values, nil
}

func (idb *InsightsDB) loadFailureLoops(ctx context.Context, checkpointID string, sessionIndex int) ([]facets.FailureLoop, error) {
	rows, err := idb.db.QueryContext(ctx,
		`SELECT description, count, evidence FROM failure_loops
		 WHERE checkpoint_id = ? AND session_index = ?`,
		checkpointID, sessionIndex,
	)
	if err != nil {
		return nil, fmt.Errorf("load failure_loops: %w", err)
	}
	defer rows.Close()

	var values []facets.FailureLoop
	for rows.Next() {
		var description string
		var count int
		var evidence sql.NullString
		if err = rows.Scan(&description, &count, &evidence); err != nil {
			return nil, fmt.Errorf("scan failure_loop: %w", err)
		}
		values = append(values, facets.FailureLoop{
			Description: description,
			Count:       count,
			Evidence:    splitEvidence(evidence.String),
		})
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate failure_loops: %w", err)
	}
	return values, nil
}

func (idb *InsightsDB) loadSkillSignals(ctx context.Context, checkpointID string, sessionIndex int) ([]facets.SkillSignal, error) {
	rows, err := idb.db.QueryContext(ctx,
		`SELECT skill_name, skill_path, friction, missing_instruction FROM skill_signals
		 WHERE checkpoint_id = ? AND session_index = ?`,
		checkpointID, sessionIndex,
	)
	if err != nil {
		return nil, fmt.Errorf("load skill_signals: %w", err)
	}
	defer rows.Close()

	var values []facets.SkillSignal
	for rows.Next() {
		var skillName string
		var skillPath, frictionText, missingInstruction sql.NullString
		if err = rows.Scan(&skillName, &skillPath, &frictionText, &missingInstruction); err != nil {
			return nil, fmt.Errorf("scan skill_signal: %w", err)
		}
		values = append(values, facets.SkillSignal{
			SkillName:          skillName,
			SkillPath:          skillPath.String,
			Friction:           splitEvidence(frictionText.String),
			MissingInstruction: missingInstruction.String,
		})
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate skill_signals: %w", err)
	}
	return values, nil
}

func (idb *InsightsDB) loadReviewDerivedRules(ctx context.Context, checkpointID string, sessionIndex int) ([]facets.ReviewDerivedRule, error) {
	rows, err := idb.db.QueryContext(ctx,
		`SELECT rule, evidence, source_kind, strength, why_reusable FROM review_rule_signals
		 WHERE checkpoint_id = ? AND session_index = ?`,
		checkpointID, sessionIndex,
	)
	if err != nil {
		return nil, fmt.Errorf("load review_rule_signals: %w", err)
	}
	defer rows.Close()

	var values []facets.ReviewDerivedRule
	for rows.Next() {
		var rule string
		var evidence, sourceKind, strength, whyReusable sql.NullString
		if err = rows.Scan(&rule, &evidence, &sourceKind, &strength, &whyReusable); err != nil {
			return nil, fmt.Errorf("scan review_rule_signal: %w", err)
		}
		values = append(values, facets.ReviewDerivedRule{
			Rule:        rule,
			Evidence:    splitEvidence(evidence.String),
			SourceKind:  sourceKind.String,
			Strength:    strength.String,
			WhyReusable: whyReusable.String,
		})
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate review_rule_signals: %w", err)
	}
	return values, nil
}

func splitEvidence(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// SkillSignalRow represents a skill signal joined with its session metadata.
type SkillSignalRow struct {
	CheckpointID       string
	SessionIndex       int
	SessionID          string
	Agent              string
	Model              string
	Branch             string
	CreatedAt          time.Time
	TotalTokens        int
	TurnCount          int
	OverallScore       float64
	SkillName          string
	SkillPath          string
	Friction           []string
	MissingInstruction string
}

// QuerySkillSignalsForSkills returns skill signals joined with session metadata
// for any skill whose name is in the given list.
func (idb *InsightsDB) QuerySkillSignalsForSkills(ctx context.Context, skillNames []string) ([]SkillSignalRow, error) {
	if len(skillNames) == 0 {
		return nil, nil
	}
	// Fetch all signals and let the caller do fuzzy matching, since
	// LLM-extracted skill names often differ from canonical discovered names
	// (e.g. "e2e:triage" vs "e2e", "superpowers:writing-plans" vs "writing-plans").
	return idb.QueryAllSkillSignals(ctx)
}

// QueryAllSkillSignals returns all skill signals joined with session metadata.
func (idb *InsightsDB) QueryAllSkillSignals(ctx context.Context) ([]SkillSignalRow, error) {
	rows, err := idb.db.QueryContext(ctx, `
		SELECT s.checkpoint_id, s.session_index, s.session_id,
		       s.agent, s.model, s.branch, s.created_at,
		       s.total_tokens, s.turn_count, s.overall_score,
		       ss.skill_name, ss.skill_path, ss.friction, ss.missing_instruction
		FROM skill_signals ss
		JOIN sessions s ON s.checkpoint_id = ss.checkpoint_id AND s.session_index = ss.session_index
		ORDER BY s.created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("query all skill signals: %w", err)
	}
	defer rows.Close()

	var results []SkillSignalRow
	for rows.Next() {
		var row SkillSignalRow
		var sessionID, agent, model, branch, skillPath, frictionText, missingInstruction sql.NullString
		var createdAt string
		var overallScore sql.NullFloat64

		if err = rows.Scan(
			&row.CheckpointID, &row.SessionIndex, &sessionID,
			&agent, &model, &branch, &createdAt,
			&row.TotalTokens, &row.TurnCount, &overallScore,
			&row.SkillName, &skillPath, &frictionText, &missingInstruction,
		); err != nil {
			return nil, fmt.Errorf("scan skill signal row: %w", err)
		}
		row.SessionID = sessionID.String
		row.Agent = agent.String
		row.Model = model.String
		row.Branch = branch.String
		row.SkillPath = skillPath.String
		row.Friction = splitEvidence(frictionText.String)
		row.MissingInstruction = missingInstruction.String
		row.OverallScore = overallScore.Float64
		t, parseErr := time.Parse(time.RFC3339, createdAt)
		if parseErr != nil {
			return nil, fmt.Errorf("parse created_at %q: %w", createdAt, parseErr)
		}
		row.CreatedAt = t
		results = append(results, row)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate skill signals: %w", err)
	}
	return results, nil
}

// SkillToolCallRow represents a session that has Skill tool invocations.
type SkillToolCallRow struct {
	CheckpointID string
	SessionIndex int
	SessionID    string
	Agent        string
	Model        string
	Branch       string
	CreatedAt    time.Time
	TotalTokens  int
	TurnCount    int
	OverallScore float64
	SkillCount   int
}

// QuerySkillToolCallSessions returns sessions that have Skill tool invocations,
// useful for finding sessions where a skill was used without generating friction.
func (idb *InsightsDB) QuerySkillToolCallSessions(ctx context.Context) ([]SkillToolCallRow, error) {
	rows, err := idb.db.QueryContext(ctx, `
		SELECT s.checkpoint_id, s.session_index, s.session_id,
		       s.agent, s.model, s.branch, s.created_at,
		       s.total_tokens, s.turn_count, s.overall_score,
		       tc.count
		FROM tool_calls tc
		JOIN sessions s ON s.checkpoint_id = tc.checkpoint_id AND s.session_index = tc.session_index
		WHERE tc.tool_name = 'Skill'
		ORDER BY s.created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("query skill tool call sessions: %w", err)
	}
	defer rows.Close()

	var results []SkillToolCallRow
	for rows.Next() {
		var row SkillToolCallRow
		var sessionID, agent, model, branch sql.NullString
		var createdAt string
		var overallScore sql.NullFloat64

		if err = rows.Scan(
			&row.CheckpointID, &row.SessionIndex, &sessionID,
			&agent, &model, &branch, &createdAt,
			&row.TotalTokens, &row.TurnCount, &overallScore,
			&row.SkillCount,
		); err != nil {
			return nil, fmt.Errorf("scan skill tool call row: %w", err)
		}
		row.SessionID = sessionID.String
		row.Agent = agent.String
		row.Model = model.String
		row.Branch = branch.String
		row.OverallScore = overallScore.Float64
		t, parseErr := time.Parse(time.RFC3339, createdAt)
		if parseErr != nil {
			return nil, fmt.Errorf("parse created_at %q: %w", createdAt, parseErr)
		}
		row.CreatedAt = t
		results = append(results, row)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate skill tool call sessions: %w", err)
	}
	return results, nil
}

// HasOwnerIDData returns true if any session has a non-empty owner_id.
func (idb *InsightsDB) HasOwnerIDData(ctx context.Context) (bool, error) {
	var count int
	err := idb.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM (SELECT 1 FROM sessions WHERE owner_id != '' LIMIT 1)`).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check owner ID data: %w", err)
	}
	return count > 0, nil
}
