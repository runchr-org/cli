package insightsdb

import (
	"database/sql"
	"fmt"
	"time"
)

// FrictionTheme groups recurring friction entries by their text content.
type FrictionTheme struct {
	Text     string   `json:"text"`
	Count    int      `json:"count"`
	Sessions []string `json:"sessions"` // checkpoint IDs where this friction occurred
}

// QueryLastNSessions returns the most recent N sessions ordered by created_at DESC.
// Denormalized fields (FilesTouched, Friction, Learnings) are populated.
func (idb *InsightsDB) QueryLastNSessions(n int) ([]SessionRow, error) {
	return idb.querySessions(
		"SELECT "+sessionColumns+" FROM sessions ORDER BY created_at DESC LIMIT ?",
		n,
	)
}

// QueryByAgent returns sessions filtered by agent name, most recent first.
func (idb *InsightsDB) QueryByAgent(agent string, limit int) ([]SessionRow, error) {
	return idb.querySessions(
		"SELECT "+sessionColumns+" FROM sessions WHERE agent = ? ORDER BY created_at DESC LIMIT ?",
		agent, limit,
	)
}

// SessionCount returns the total number of cached sessions.
func (idb *InsightsDB) SessionCount() (int, error) {
	var count int
	if err := idb.db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count); err != nil {
		return 0, fmt.Errorf("session count: %w", err)
	}
	return count, nil
}

// QueryRecurringFriction returns friction themes occurring at least minCount times,
// ordered by count descending.
func (idb *InsightsDB) QueryRecurringFriction(minCount int) ([]FrictionTheme, error) {
	rows, err := idb.db.Query(`
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
	return themes, rows.Err()
}

// QuerySessionsWithFriction returns checkpoint IDs of sessions containing
// friction matching the given SQL LIKE pattern (e.g., "%tool call failed%").
func (idb *InsightsDB) QuerySessionsWithFriction(pattern string) ([]string, error) {
	rows, err := idb.db.Query(
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
	return ids, rows.Err()
}

// sessionColumns is the ordered column list for SELECT queries on the sessions table.
const sessionColumns = `
	checkpoint_id, session_id, session_index,
	agent, model, branch, created_at,
	input_tokens, cache_tokens, output_tokens, total_tokens,
	api_call_count, duration_ms, turn_count,
	intent, outcome, agent_percentage,
	overall_score, score_token_efficiency, score_first_pass,
	score_friction, score_focus`

// querySessions executes a SELECT on sessions with the given args,
// then populates denormalized fields for each row.
func (idb *InsightsDB) querySessions(query string, args ...interface{}) ([]SessionRow, error) {
	rows, err := idb.db.Query(query, args...)
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
		if err = idb.populateDenormalized(&sessions[i]); err != nil {
			return nil, err
		}
	}
	return sessions, nil
}

// scanSession reads one row from the sessions table into a SessionRow.
func scanSession(rows *sql.Rows) (SessionRow, error) {
	var row SessionRow
	var createdAt string
	var agent, model, branch, intent, outcome sql.NullString
	var overallScore, tokenEff, firstPass, friction, focus sql.NullFloat64

	err := rows.Scan(
		&row.CheckpointID, &row.SessionID, &row.SessionIndex,
		&agent, &model, &branch, &createdAt,
		&row.InputTokens, &row.CacheTokens, &row.OutputTokens, &row.TotalTokens,
		&row.APICallCount, &row.DurationMs, &row.TurnCount,
		&intent, &outcome, &row.AgentPct,
		&overallScore, &tokenEff, &firstPass,
		&friction, &focus,
	)
	if err != nil {
		return row, fmt.Errorf("scan session row: %w", err)
	}

	row.Agent = agent.String
	row.Model = model.String
	row.Branch = branch.String
	row.Intent = intent.String
	row.Outcome = outcome.String
	row.OverallScore = overallScore.Float64
	row.ScoreTokenEff = tokenEff.Float64
	row.ScoreFirstPass = firstPass.Float64
	row.ScoreFriction = friction.Float64
	row.ScoreFocus = focus.Float64

	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return row, fmt.Errorf("parse created_at %q: %w", createdAt, err)
	}
	row.CreatedAt = t
	return row, nil
}

// populateDenormalized loads files_touched, friction, and learnings for the session.
func (idb *InsightsDB) populateDenormalized(row *SessionRow) error {
	var err error
	row.FilesTouched, err = idb.loadFilesTouched(row.CheckpointID, row.SessionIndex)
	if err != nil {
		return err
	}
	row.Friction, err = idb.loadFriction(row.CheckpointID, row.SessionIndex)
	if err != nil {
		return err
	}
	row.Learnings, err = idb.loadLearnings(row.CheckpointID, row.SessionIndex)
	return err
}

func (idb *InsightsDB) loadFilesTouched(checkpointID string, sessionIndex int) ([]string, error) {
	rows, err := idb.db.Query(
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
	return files, rows.Err()
}

func (idb *InsightsDB) loadFriction(checkpointID string, sessionIndex int) ([]string, error) {
	rows, err := idb.db.Query(
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
	return friction, rows.Err()
}

func (idb *InsightsDB) loadLearnings(checkpointID string, sessionIndex int) ([]LearningRow, error) {
	rows, err := idb.db.Query(
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
	return learnings, rows.Err()
}

// splitCSV splits a GROUP_CONCAT result on commas.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}
