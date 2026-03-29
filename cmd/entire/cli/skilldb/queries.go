package skilldb

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"
)

// SkillRow represents a skill definition stored in the skills table.
type SkillRow struct {
	Name         string
	SourceAgent  string
	Path         string
	Kind         string
	Scope        string // "repo" or "personal"
	DiscoveredAt time.Time
	LastSeenAt   time.Time
}

// SkillStatsResult contains aggregated statistics for a skill.
type SkillStatsResult struct {
	TotalSessions   int
	AvgScore        float64
	TotalFriction   int
	TotalTokens     int64
	FirstUsed       time.Time
	LastUsed        time.Time
	SessionsPerWeek float64
}

// SkillSessionRow represents a single session for a skill.
type SkillSessionRow struct {
	SkillName     string
	SourceAgent   string
	CheckpointID  string
	SessionIndex  int
	SessionID     string
	Agent         string
	Model         string
	Branch        string
	CreatedAt     time.Time
	TotalTokens   int
	TurnCount     int
	OverallScore  float64
	FrictionCount int
	Outcome       string
}

// FrictionThemeRow groups recurring friction entries by their text content.
type FrictionThemeRow struct {
	Text     string
	Category string
	Count    int
	Sessions []string
}

// MissingInstructionRow groups recurring missing instructions.
type MissingInstructionRow struct {
	Instruction string
	Count       int
	Evidence    []string
	Sessions    []string
}

// AgentBreakdownRow contains per-agent aggregated statistics.
type AgentBreakdownRow struct {
	Agent        string
	SessionCount int
	AvgScore     float64
	TotalTokens  int64
}

// SkillImprovement represents a suggested improvement for a skill.
type SkillImprovement struct {
	ID          string
	SkillName   string
	SourceAgent string
	Title       string
	Description string
	Diff        string
	Priority    string
	Status      string
	CreatedAt   time.Time
	AppliedAt   *time.Time
}

// UpsertSkill inserts or updates a skill, preserving discovered_at on update.
func (sdb *SkillDB) UpsertSkill(ctx context.Context, skill SkillRow) error {
	_, err := sdb.db.ExecContext(ctx, `
		INSERT INTO skills (name, source_agent, path, kind, discovered_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(name, source_agent) DO UPDATE SET
			path = excluded.path,
			kind = excluded.kind,
			last_seen_at = excluded.last_seen_at`,
		skill.Name, skill.SourceAgent, skill.Path, skill.Kind,
		skill.DiscoveredAt.UTC().Format(time.RFC3339),
		skill.LastSeenAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("upsert skill: %w", err)
	}
	return nil
}

// ListSkills returns all skills from the skills table.
func (sdb *SkillDB) ListSkills(ctx context.Context) ([]SkillRow, error) {
	rows, err := sdb.db.QueryContext(ctx,
		"SELECT name, source_agent, path, kind, discovered_at, last_seen_at FROM skills ORDER BY name",
	)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	defer rows.Close()

	var skills []SkillRow
	for rows.Next() {
		var s SkillRow
		var discoveredAt, lastSeenAt string
		if err = rows.Scan(&s.Name, &s.SourceAgent, &s.Path, &s.Kind, &discoveredAt, &lastSeenAt); err != nil {
			return nil, fmt.Errorf("scan skill: %w", err)
		}
		s.DiscoveredAt, err = time.Parse(time.RFC3339, discoveredAt)
		if err != nil {
			return nil, fmt.Errorf("parse discovered_at %q: %w", discoveredAt, err)
		}
		s.LastSeenAt, err = time.Parse(time.RFC3339, lastSeenAt)
		if err != nil {
			return nil, fmt.Errorf("parse last_seen_at %q: %w", lastSeenAt, err)
		}
		skills = append(skills, s)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate skills: %w", err)
	}
	return skills, nil
}

// SkillStats returns aggregated statistics for a specific skill.
func (sdb *SkillDB) SkillStats(ctx context.Context, name, sourceAgent string) (*SkillStatsResult, error) {
	var result SkillStatsResult
	var avgScore sql.NullFloat64
	var firstUsed, lastUsed sql.NullString

	err := sdb.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) AS total_sessions,
			COALESCE(AVG(overall_score), 0) AS avg_score,
			COALESCE(SUM(friction_count), 0) AS total_friction,
			COALESCE(SUM(total_tokens), 0) AS total_tokens,
			MIN(created_at) AS first_used,
			MAX(created_at) AS last_used
		FROM skill_sessions
		WHERE skill_name = ? AND source_agent = ?`,
		name, sourceAgent,
	).Scan(
		&result.TotalSessions,
		&avgScore,
		&result.TotalFriction,
		&result.TotalTokens,
		&firstUsed,
		&lastUsed,
	)
	if err != nil {
		return nil, fmt.Errorf("skill stats: %w", err)
	}

	result.AvgScore = avgScore.Float64

	if firstUsed.Valid {
		result.FirstUsed, err = time.Parse(time.RFC3339, firstUsed.String)
		if err != nil {
			return nil, fmt.Errorf("parse first_used %q: %w", firstUsed.String, err)
		}
	}
	if lastUsed.Valid {
		result.LastUsed, err = time.Parse(time.RFC3339, lastUsed.String)
		if err != nil {
			return nil, fmt.Errorf("parse last_used %q: %w", lastUsed.String, err)
		}
	}

	if result.TotalSessions > 0 && !result.FirstUsed.IsZero() {
		weeks := time.Since(result.FirstUsed).Hours() / (24 * 7)
		if weeks < 1 {
			weeks = 1
		}
		result.SessionsPerWeek = math.Round(float64(result.TotalSessions)/weeks*100) / 100
	}

	return &result, nil
}

// RecentSessions returns the last N sessions for a skill, ordered by created_at DESC.
func (sdb *SkillDB) RecentSessions(ctx context.Context, name, sourceAgent string, limit int) ([]SkillSessionRow, error) {
	rows, err := sdb.db.QueryContext(ctx, `
		SELECT skill_name, source_agent, checkpoint_id, session_index,
			session_id, agent, model, branch, created_at,
			total_tokens, turn_count, overall_score, friction_count, outcome
		FROM skill_sessions
		WHERE skill_name = ? AND source_agent = ?
		ORDER BY created_at DESC
		LIMIT ?`,
		name, sourceAgent, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("recent sessions: %w", err)
	}
	defer rows.Close()

	var sessions []SkillSessionRow
	for rows.Next() {
		s, scanErr := scanSkillSession(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		sessions = append(sessions, s)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent sessions: %w", err)
	}
	return sessions, nil
}

// SkillFrictionThemes returns friction entries grouped by text for a skill.
func (sdb *SkillDB) SkillFrictionThemes(ctx context.Context, name, sourceAgent string) ([]FrictionThemeRow, error) {
	rows, err := sdb.db.QueryContext(ctx, `
		SELECT text, category, COUNT(*) AS cnt, GROUP_CONCAT(DISTINCT checkpoint_id) AS sessions
		FROM skill_friction
		WHERE skill_name = ? AND source_agent = ?
		GROUP BY text, category
		ORDER BY cnt DESC`,
		name, sourceAgent,
	)
	if err != nil {
		return nil, fmt.Errorf("skill friction themes: %w", err)
	}
	defer rows.Close()

	var themes []FrictionThemeRow
	for rows.Next() {
		var t FrictionThemeRow
		var category sql.NullString
		var sessionsCSV string
		if err = rows.Scan(&t.Text, &category, &t.Count, &sessionsCSV); err != nil {
			return nil, fmt.Errorf("scan friction theme: %w", err)
		}
		t.Category = category.String
		t.Sessions = splitCSV(sessionsCSV)
		themes = append(themes, t)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate friction themes: %w", err)
	}
	return themes, nil
}

// SkillMissingInstructions returns missing instructions grouped by instruction for a skill.
func (sdb *SkillDB) SkillMissingInstructions(ctx context.Context, name, sourceAgent string) ([]MissingInstructionRow, error) {
	rows, err := sdb.db.QueryContext(ctx, `
		SELECT instruction, COUNT(*) AS cnt,
			GROUP_CONCAT(DISTINCT evidence) AS evidence,
			GROUP_CONCAT(DISTINCT checkpoint_id) AS sessions
		FROM skill_missing_instructions
		WHERE skill_name = ? AND source_agent = ?
		GROUP BY instruction
		ORDER BY cnt DESC`,
		name, sourceAgent,
	)
	if err != nil {
		return nil, fmt.Errorf("skill missing instructions: %w", err)
	}
	defer rows.Close()

	var instructions []MissingInstructionRow
	for rows.Next() {
		var m MissingInstructionRow
		var evidenceConcat sql.NullString
		var sessionsCSV string
		if err = rows.Scan(&m.Instruction, &m.Count, &evidenceConcat, &sessionsCSV); err != nil {
			return nil, fmt.Errorf("scan missing instruction: %w", err)
		}
		m.Evidence = splitCSV(evidenceConcat.String)
		m.Sessions = splitCSV(sessionsCSV)
		instructions = append(instructions, m)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate missing instructions: %w", err)
	}
	return instructions, nil
}

// AgentBreakdown returns per-agent aggregated statistics for a skill.
func (sdb *SkillDB) AgentBreakdown(ctx context.Context, name, sourceAgent string) ([]AgentBreakdownRow, error) {
	rows, err := sdb.db.QueryContext(ctx, `
		SELECT agent, COUNT(*) AS session_count,
			COALESCE(AVG(overall_score), 0) AS avg_score,
			COALESCE(SUM(total_tokens), 0) AS total_tokens
		FROM skill_sessions
		WHERE skill_name = ? AND source_agent = ?
		GROUP BY agent
		ORDER BY session_count DESC`,
		name, sourceAgent,
	)
	if err != nil {
		return nil, fmt.Errorf("agent breakdown: %w", err)
	}
	defer rows.Close()

	var breakdown []AgentBreakdownRow
	for rows.Next() {
		var a AgentBreakdownRow
		var agent sql.NullString
		if err = rows.Scan(&agent, &a.SessionCount, &a.AvgScore, &a.TotalTokens); err != nil {
			return nil, fmt.Errorf("scan agent breakdown: %w", err)
		}
		a.Agent = agent.String
		breakdown = append(breakdown, a)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent breakdown: %w", err)
	}
	return breakdown, nil
}

// InsertImprovement inserts a new improvement suggestion.
func (sdb *SkillDB) InsertImprovement(ctx context.Context, imp SkillImprovement) error {
	_, err := sdb.db.ExecContext(ctx, `
		INSERT INTO skill_improvements (id, skill_name, source_agent, title, description, diff, priority, status, created_at, applied_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		imp.ID, imp.SkillName, imp.SourceAgent, imp.Title,
		nullableString(imp.Description), nullableString(imp.Diff),
		imp.Priority, imp.Status,
		imp.CreatedAt.UTC().Format(time.RFC3339),
		formatOptionalTime(imp.AppliedAt),
	)
	if err != nil {
		return fmt.Errorf("insert improvement: %w", err)
	}
	return nil
}

// ListImprovements returns improvements for a skill, optionally filtered by status.
// If status is empty, all improvements are returned.
func (sdb *SkillDB) ListImprovements(ctx context.Context, name, sourceAgent, status string) ([]SkillImprovement, error) {
	var query string
	var args []interface{}

	if status != "" {
		query = `SELECT id, skill_name, source_agent, title, description, diff, priority, status, created_at, applied_at
			FROM skill_improvements
			WHERE skill_name = ? AND source_agent = ? AND status = ?
			ORDER BY created_at DESC`
		args = []interface{}{name, sourceAgent, status}
	} else {
		query = `SELECT id, skill_name, source_agent, title, description, diff, priority, status, created_at, applied_at
			FROM skill_improvements
			WHERE skill_name = ? AND source_agent = ?
			ORDER BY created_at DESC`
		args = []interface{}{name, sourceAgent}
	}

	rows, err := sdb.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list improvements: %w", err)
	}
	defer rows.Close()

	var improvements []SkillImprovement
	for rows.Next() {
		imp, scanErr := scanImprovement(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		improvements = append(improvements, imp)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate improvements: %w", err)
	}
	return improvements, nil
}

// UpdateImprovementStatus updates the status of an improvement by ID.
func (sdb *SkillDB) UpdateImprovementStatus(ctx context.Context, id, status string) error {
	var appliedAt interface{}
	if status == "applied" {
		appliedAt = time.Now().UTC().Format(time.RFC3339)
	}

	_, err := sdb.db.ExecContext(ctx, `
		UPDATE skill_improvements SET status = ?, applied_at = COALESCE(?, applied_at)
		WHERE id = ?`,
		status, appliedAt, id,
	)
	if err != nil {
		return fmt.Errorf("update improvement status: %w", err)
	}
	return nil
}

// GetCacheTip returns the stored cache tip hash from cache_meta,
// or an empty string if it has not been set yet.
func (sdb *SkillDB) GetCacheTip(ctx context.Context) (string, error) {
	var tip string
	err := sdb.db.QueryRowContext(ctx,
		"SELECT value FROM cache_meta WHERE key = ?",
		"cache_tip",
	).Scan(&tip)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get cache tip: %w", err)
	}
	return tip, nil
}

// SetCacheTip stores the cache tip hash in cache_meta.
// Overwrites any previously stored value.
func (sdb *SkillDB) SetCacheTip(ctx context.Context, tip string) error {
	_, err := sdb.db.ExecContext(ctx,
		"INSERT OR REPLACE INTO cache_meta (key, value) VALUES (?, ?)",
		"cache_tip",
		tip,
	)
	if err != nil {
		return fmt.Errorf("set cache tip: %w", err)
	}
	return nil
}

// InsertSession inserts a skill session row.
func (sdb *SkillDB) InsertSession(ctx context.Context, row SkillSessionRow) error {
	_, err := sdb.db.ExecContext(ctx, `
		INSERT INTO skill_sessions (skill_name, source_agent, checkpoint_id, session_index,
			session_id, agent, model, branch, created_at,
			total_tokens, turn_count, overall_score, friction_count, outcome)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.SkillName, row.SourceAgent, row.CheckpointID, row.SessionIndex,
		nullableString(row.SessionID), nullableString(row.Agent),
		nullableString(row.Model), nullableString(row.Branch),
		row.CreatedAt.UTC().Format(time.RFC3339),
		row.TotalTokens, row.TurnCount, row.OverallScore, row.FrictionCount,
		nullableString(row.Outcome),
	)
	if err != nil {
		return fmt.Errorf("insert skill session: %w", err)
	}
	return nil
}

// InsertFriction inserts a friction entry for a skill session.
func (sdb *SkillDB) InsertFriction(ctx context.Context, skillName, sourceAgent, checkpointID string, sessionIndex int, text, category string) error {
	_, err := sdb.db.ExecContext(ctx,
		`INSERT INTO skill_friction (skill_name, source_agent, checkpoint_id, session_index, text, category)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		skillName, sourceAgent, checkpointID, sessionIndex, text, nullableString(category),
	)
	if err != nil {
		return fmt.Errorf("insert skill friction: %w", err)
	}
	return nil
}

// InsertMissingInstruction inserts a missing instruction entry for a skill session.
func (sdb *SkillDB) InsertMissingInstruction(ctx context.Context, skillName, sourceAgent, checkpointID string, sessionIndex int, instruction, evidence string) error {
	_, err := sdb.db.ExecContext(ctx,
		`INSERT INTO skill_missing_instructions (skill_name, source_agent, checkpoint_id, session_index, instruction, evidence)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		skillName, sourceAgent, checkpointID, sessionIndex, instruction, nullableString(evidence),
	)
	if err != nil {
		return fmt.Errorf("insert skill missing instruction: %w", err)
	}
	return nil
}

// BeginTx starts a new transaction for bulk inserts.
func (sdb *SkillDB) BeginTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := sdb.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	return tx, nil
}

// InsertSessionTx inserts a skill session row within an existing transaction.
func (sdb *SkillDB) InsertSessionTx(ctx context.Context, tx *sql.Tx, row SkillSessionRow) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO skill_sessions (skill_name, source_agent, checkpoint_id, session_index,
			session_id, agent, model, branch, created_at,
			total_tokens, turn_count, overall_score, friction_count, outcome)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.SkillName, row.SourceAgent, row.CheckpointID, row.SessionIndex,
		nullableString(row.SessionID), nullableString(row.Agent),
		nullableString(row.Model), nullableString(row.Branch),
		row.CreatedAt.UTC().Format(time.RFC3339),
		row.TotalTokens, row.TurnCount, row.OverallScore, row.FrictionCount,
		nullableString(row.Outcome),
	)
	if err != nil {
		return fmt.Errorf("insert skill session (tx): %w", err)
	}
	return nil
}

// InsertFrictionTx inserts a friction entry within an existing transaction.
func (sdb *SkillDB) InsertFrictionTx(ctx context.Context, tx *sql.Tx, skillName, sourceAgent, checkpointID string, sessionIndex int, text, category string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO skill_friction (skill_name, source_agent, checkpoint_id, session_index, text, category)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		skillName, sourceAgent, checkpointID, sessionIndex, text, nullableString(category),
	)
	if err != nil {
		return fmt.Errorf("insert skill friction (tx): %w", err)
	}
	return nil
}

// InsertMissingInstructionTx inserts a missing instruction entry within an existing transaction.
func (sdb *SkillDB) InsertMissingInstructionTx(ctx context.Context, tx *sql.Tx, skillName, sourceAgent, checkpointID string, sessionIndex int, instruction, evidence string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO skill_missing_instructions (skill_name, source_agent, checkpoint_id, session_index, instruction, evidence)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		skillName, sourceAgent, checkpointID, sessionIndex, instruction, nullableString(evidence),
	)
	if err != nil {
		return fmt.Errorf("insert skill missing instruction (tx): %w", err)
	}
	return nil
}

// scanSkillSession reads one row from the skill_sessions table.
func scanSkillSession(rows *sql.Rows) (SkillSessionRow, error) {
	var s SkillSessionRow
	var sessionID, agent, model, branch, outcome sql.NullString
	var overallScore sql.NullFloat64
	var createdAt string

	err := rows.Scan(
		&s.SkillName, &s.SourceAgent, &s.CheckpointID, &s.SessionIndex,
		&sessionID, &agent, &model, &branch, &createdAt,
		&s.TotalTokens, &s.TurnCount, &overallScore, &s.FrictionCount, &outcome,
	)
	if err != nil {
		return s, fmt.Errorf("scan skill session: %w", err)
	}

	s.SessionID = sessionID.String
	s.Agent = agent.String
	s.Model = model.String
	s.Branch = branch.String
	s.Outcome = outcome.String
	s.OverallScore = overallScore.Float64

	s.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return s, fmt.Errorf("parse created_at %q: %w", createdAt, err)
	}
	return s, nil
}

// scanImprovement reads one row from the skill_improvements table.
func scanImprovement(rows *sql.Rows) (SkillImprovement, error) {
	var imp SkillImprovement
	var description, diff, appliedAt sql.NullString
	var createdAt string

	err := rows.Scan(
		&imp.ID, &imp.SkillName, &imp.SourceAgent, &imp.Title,
		&description, &diff, &imp.Priority, &imp.Status,
		&createdAt, &appliedAt,
	)
	if err != nil {
		return imp, fmt.Errorf("scan improvement: %w", err)
	}

	imp.Description = description.String
	imp.Diff = diff.String

	imp.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return imp, fmt.Errorf("parse created_at %q: %w", createdAt, err)
	}

	if appliedAt.Valid {
		t, parseErr := time.Parse(time.RFC3339, appliedAt.String)
		if parseErr != nil {
			return imp, fmt.Errorf("parse applied_at %q: %w", appliedAt.String, parseErr)
		}
		imp.AppliedAt = &t
	}

	return imp, nil
}

// nullableString converts an empty string to a SQL NULL value.
func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func formatOptionalTime(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
