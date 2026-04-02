package insightsdb

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/facets"
)

// SessionRow represents a single session for insertion into the cache.
// This is the denormalized view used by the CLI — callers populate it
// from checkpoint.CommittedMetadata.
type SessionRow struct {
	CheckpointID string
	SessionID    string
	SessionIndex int
	Agent        string
	Model        string
	Branch       string
	OwnerName    string
	OwnerID      string
	OwnerEmail   string
	CreatedAt    time.Time
	InputTokens  int
	CacheTokens  int
	OutputTokens int
	TotalTokens  int
	APICallCount int
	DurationMs   int64
	TurnCount    int
	Intent       string
	Outcome      string
	AgentPct     float64
	// Score fields
	OverallScore   float64
	ScoreTokenEff  float64
	ScoreFirstPass float64
	ScoreFriction  float64
	ScoreFocus     float64
	HasSummary     bool
	HasFacets      bool
	// Denormalized arrays
	FilesTouched             []string
	Friction                 []string
	Learnings                []LearningRow
	ImplementationRationale  []string
	Tradeoffs                []string
	CodebasePatterns         []string
	ToolCounts               map[string]int // tool name → invocation count
	Facets                   facets.SessionFacets
}

// LearningRow represents a single learning entry within a session.
type LearningRow struct {
	Scope   string // "repo", "workflow", "code"
	Finding string
	Path    string // only meaningful when Scope is "code"
}

// GetBranchTip returns the stored branch tip hash from cache_meta,
// or an empty string if it has not been set yet.
func (idb *InsightsDB) GetBranchTip(ctx context.Context) (string, error) {
	var tip string
	err := idb.db.QueryRowContext(ctx,
		"SELECT value FROM cache_meta WHERE key = ?",
		"branch_tip",
	).Scan(&tip)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get branch tip: %w", err)
	}
	return tip, nil
}

// SetBranchTip stores the branch tip hash in cache_meta.
// Overwrites any previously stored value.
func (idb *InsightsDB) SetBranchTip(ctx context.Context, tip string) error {
	_, err := idb.db.ExecContext(ctx,
		"INSERT OR REPLACE INTO cache_meta (key, value) VALUES (?, ?)",
		"branch_tip",
		tip,
	)
	if err != nil {
		return fmt.Errorf("set branch tip: %w", err)
	}
	return nil
}

// GetContentFingerprint returns a lightweight fingerprint of the insightsdb
// data state, based on row counts of key tables. This changes when facets
// are backfilled or new tool_calls are ingested, even if the branch tip
// hasn't changed.
func (idb *InsightsDB) GetContentFingerprint(ctx context.Context) (string, error) {
	var fingerprint string
	err := idb.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM sessions) || ':' ||
			(SELECT COUNT(*) FROM skill_signals) || ':' ||
			(SELECT COUNT(*) FROM review_rule_signals) || ':' ||
			(SELECT COUNT(*) FROM tool_calls WHERE tool_name = 'Skill') || ':' ||
			(SELECT COUNT(*) FROM sessions WHERE has_facets = 1)
	`).Scan(&fingerprint)
	if err != nil {
		return "", fmt.Errorf("get content fingerprint: %w", err)
	}
	return fingerprint, nil
}

// HasCheckpoint returns true if any session for the given checkpoint ID
// is already present in the sessions table.
func (idb *InsightsDB) HasCheckpoint(ctx context.Context, checkpointID string) (bool, error) {
	var count int
	err := idb.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sessions WHERE checkpoint_id = ?",
		checkpointID,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check checkpoint existence: %w", err)
	}
	return count > 0, nil
}

// InsertSession inserts a session and all its denormalized data into the cache.
// The insert is performed inside a single transaction so the cache remains
// consistent even if the caller is interrupted mid-insert.
func (idb *InsightsDB) InsertSession(ctx context.Context, row SessionRow) error {
	tx, err := idb.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback() //nolint:errcheck // Rollback after failed tx; error is irrelevant
		}
	}()

	if err = insertSessionRow(ctx, tx, row); err != nil {
		return err
	}
	if err = insertFilesTouched(ctx, tx, row); err != nil {
		return err
	}
	if err = insertFriction(ctx, tx, row); err != nil {
		return err
	}
	if err = insertLearnings(ctx, tx, row); err != nil {
		return err
	}
	if err = insertSummarySignals(ctx, tx, row); err != nil {
		return err
	}
	if err = insertToolCalls(ctx, tx, row); err != nil {
		return err
	}
	if err = insertFacets(ctx, tx, row); err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func insertSessionRow(ctx context.Context, tx *sql.Tx, row SessionRow) error {
	hasSummary := 0
	if row.HasSummary {
		hasSummary = 1
	}
	hasFacets := 0
	if row.HasFacets || !isEmptyFacets(row.Facets) {
		hasFacets = 1
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO sessions (
			checkpoint_id, session_id, session_index,
			agent, model, branch, owner_name, owner_id, owner_email, created_at,
			input_tokens, cache_tokens, output_tokens, total_tokens,
			api_call_count, duration_ms, turn_count,
			intent, outcome, agent_percentage,
			overall_score, score_token_efficiency, score_first_pass,
			score_friction, score_focus, has_summary, has_facets
		) VALUES (
			?, ?, ?,
			?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?,
			?, ?, ?,
			?, ?, ?,
			?, ?, ?, ?
		)`,
		row.CheckpointID, row.SessionID, row.SessionIndex,
		nullableString(row.Agent), nullableString(row.Model), nullableString(row.Branch),
		nullableString(row.OwnerName), nullableString(row.OwnerID), nullableString(row.OwnerEmail),
		row.CreatedAt.UTC().Format(time.RFC3339),
		row.InputTokens, row.CacheTokens, row.OutputTokens, row.TotalTokens,
		row.APICallCount, row.DurationMs, row.TurnCount,
		nullableString(row.Intent), nullableString(row.Outcome), row.AgentPct,
		row.OverallScore, row.ScoreTokenEff,
		row.ScoreFirstPass, row.ScoreFriction,
		row.ScoreFocus, hasSummary, hasFacets,
	)
	if err != nil {
		return fmt.Errorf("insert session row: %w", err)
	}
	return nil
}

func insertFilesTouched(ctx context.Context, tx *sql.Tx, row SessionRow) error {
	for _, f := range row.FilesTouched {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO files_touched (checkpoint_id, session_index, file_path) VALUES (?, ?, ?)",
			row.CheckpointID, row.SessionIndex, f,
		); err != nil {
			return fmt.Errorf("insert files_touched: %w", err)
		}
	}
	return nil
}

func insertFriction(ctx context.Context, tx *sql.Tx, row SessionRow) error {
	for _, f := range row.Friction {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO friction (checkpoint_id, session_index, text) VALUES (?, ?, ?)",
			row.CheckpointID, row.SessionIndex, f,
		); err != nil {
			return fmt.Errorf("insert friction: %w", err)
		}
	}
	return nil
}

func insertLearnings(ctx context.Context, tx *sql.Tx, row SessionRow) error {
	for _, l := range row.Learnings {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO learnings (checkpoint_id, session_index, scope, finding, path) VALUES (?, ?, ?, ?, ?)",
			row.CheckpointID, row.SessionIndex, l.Scope, l.Finding, nullableString(l.Path),
		); err != nil {
			return fmt.Errorf("insert learnings: %w", err)
		}
	}
	return nil
}

func insertSummarySignals(ctx context.Context, tx *sql.Tx, row SessionRow) error {
	for _, value := range row.ImplementationRationale {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO implementation_rationale (checkpoint_id, session_index, text) VALUES (?, ?, ?)",
			row.CheckpointID, row.SessionIndex, value,
		); err != nil {
			return fmt.Errorf("insert implementation_rationale: %w", err)
		}
	}
	for _, value := range row.Tradeoffs {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO tradeoffs (checkpoint_id, session_index, text) VALUES (?, ?, ?)",
			row.CheckpointID, row.SessionIndex, value,
		); err != nil {
			return fmt.Errorf("insert tradeoffs: %w", err)
		}
	}
	for _, value := range row.CodebasePatterns {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO codebase_patterns (checkpoint_id, session_index, text) VALUES (?, ?, ?)",
			row.CheckpointID, row.SessionIndex, value,
		); err != nil {
			return fmt.Errorf("insert codebase_patterns: %w", err)
		}
	}
	return nil
}

func insertToolCalls(ctx context.Context, tx *sql.Tx, row SessionRow) error {
	for tool, count := range row.ToolCounts {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO tool_calls (checkpoint_id, session_index, tool_name, count) VALUES (?, ?, ?, ?)",
			row.CheckpointID, row.SessionIndex, tool, count,
		); err != nil {
			return fmt.Errorf("insert tool_calls: %w", err)
		}
	}
	return nil
}

func insertFacets(ctx context.Context, tx *sql.Tx, row SessionRow) error {
	for _, instruction := range row.Facets.RepeatedUserInstructions {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO repeated_user_instructions (checkpoint_id, session_index, instruction, evidence)
			 VALUES (?, ?, ?, ?)`,
			row.CheckpointID, row.SessionIndex, instruction.Instruction, joinEvidence(instruction.Evidence),
		); err != nil {
			return fmt.Errorf("insert repeated_user_instructions: %w", err)
		}
	}
	for _, signal := range row.Facets.MissingContext {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO missing_context_signals (checkpoint_id, session_index, item, evidence)
			 VALUES (?, ?, ?, ?)`,
			row.CheckpointID, row.SessionIndex, signal.Item, joinEvidence(signal.Evidence),
		); err != nil {
			return fmt.Errorf("insert missing_context_signals: %w", err)
		}
	}
	for _, loop := range row.Facets.FailureLoops {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO failure_loops (checkpoint_id, session_index, description, count, evidence)
			 VALUES (?, ?, ?, ?, ?)`,
			row.CheckpointID, row.SessionIndex, loop.Description, loop.Count, joinEvidence(loop.Evidence),
		); err != nil {
			return fmt.Errorf("insert failure_loops: %w", err)
		}
	}
	for _, signal := range row.Facets.SkillSignals {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO skill_signals (checkpoint_id, session_index, skill_name, skill_path, friction, missing_instruction)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			row.CheckpointID, row.SessionIndex, signal.SkillName, nullableString(signal.SkillPath),
			joinEvidence(signal.Friction), nullableString(signal.MissingInstruction),
		); err != nil {
			return fmt.Errorf("insert skill_signals: %w", err)
		}
	}
	for _, rule := range row.Facets.ReviewDerivedRules {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO review_rule_signals (checkpoint_id, session_index, rule, evidence, source_kind, strength, why_reusable)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			row.CheckpointID, row.SessionIndex, rule.Rule, joinEvidence(rule.Evidence),
			nullableString(rule.SourceKind), nullableString(rule.Strength), nullableString(rule.WhyReusable),
		); err != nil {
			return fmt.Errorf("insert review_rule_signals: %w", err)
		}
	}
	return nil
}

// UpdateSessionSummary updates an existing session row with summary-derived data
// (intent, outcome, scores, friction, learnings, explanatory insight arrays)
// and sets has_summary = 1.
func (idb *InsightsDB) UpdateSessionSummary(ctx context.Context, row SessionRow) error {
	tx, err := idb.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback() //nolint:errcheck // Rollback after failed tx; error is irrelevant
		}
	}()

	_, err = tx.ExecContext(ctx, `
		UPDATE sessions SET
			intent = ?, outcome = ?,
			overall_score = ?, score_token_efficiency = ?, score_first_pass = ?,
			score_friction = ?, score_focus = ?, has_summary = 1
		WHERE checkpoint_id = ? AND session_index = ?`,
		nullableString(row.Intent), nullableString(row.Outcome),
		row.OverallScore, row.ScoreTokenEff, row.ScoreFirstPass,
		row.ScoreFriction, row.ScoreFocus,
		row.CheckpointID, row.SessionIndex,
	)
	if err != nil {
		return fmt.Errorf("update session summary: %w", err)
	}

	// Delete old summary-derived rows and re-insert.
	for _, table := range []string{
		"friction",
		"learnings",
		"implementation_rationale",
		"tradeoffs",
		"codebase_patterns",
	} {
		if _, err = tx.ExecContext(ctx,
			"DELETE FROM "+table+" WHERE checkpoint_id = ? AND session_index = ?", //nolint:gosec // table name is hardcoded
			row.CheckpointID, row.SessionIndex,
		); err != nil {
			return fmt.Errorf("delete old %s: %w", table, err)
		}
	}

	if err = insertFriction(ctx, tx, row); err != nil {
		return err
	}
	if err = insertLearnings(ctx, tx, row); err != nil {
		return err
	}
	if err = insertSummarySignals(ctx, tx, row); err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// UpdateSessionFacets updates an existing session row with extracted structured facets.
func (idb *InsightsDB) UpdateSessionFacets(ctx context.Context, row SessionRow) error {
	tx, err := idb.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback() //nolint:errcheck // Rollback after failed tx; error is irrelevant
		}
	}()

	if _, err = tx.ExecContext(ctx, `
		UPDATE sessions SET has_facets = 1
		WHERE checkpoint_id = ? AND session_index = ?`,
		row.CheckpointID, row.SessionIndex,
	); err != nil {
		return fmt.Errorf("update session facets: %w", err)
	}

	for _, table := range []string{
		"repeated_user_instructions",
		"missing_context_signals",
		"failure_loops",
		"skill_signals",
		"review_rule_signals",
	} {
		if _, err = tx.ExecContext(ctx,
			"DELETE FROM "+table+" WHERE checkpoint_id = ? AND session_index = ?", //nolint:gosec // table name is hardcoded
			row.CheckpointID, row.SessionIndex,
		); err != nil {
			return fmt.Errorf("delete old %s: %w", table, err)
		}
	}

	if err = insertFacets(ctx, tx, row); err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// nullableString converts an empty string to a SQL NULL value.
// Non-empty strings are passed through as-is.
func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func joinEvidence(values []string) interface{} {
	if len(values) == 0 {
		return nil
	}
	return strings.Join(values, "\n")
}

func isEmptyFacets(v facets.SessionFacets) bool {
	return len(v.RepeatedUserInstructions) == 0 &&
		len(v.MissingContext) == 0 &&
		len(v.FailureLoops) == 0 &&
		len(v.SkillSignals) == 0 &&
		len(v.ReviewDerivedRules) == 0 &&
		len(v.RepoGotchas) == 0 &&
		len(v.WorkflowGaps) == 0
}
