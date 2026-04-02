// Package improve provides context file detection, friction analysis, and
// AI-powered improvement suggestions for project context files.
package improve

import (
	"time"

	"github.com/entireio/cli/cmd/entire/cli/facets"
)

// ContextFileType identifies the type of context file.
type ContextFileType string

const (
	// ContextFileCLAUDEMD represents a CLAUDE.md context file.
	ContextFileCLAUDEMD ContextFileType = "CLAUDE.md"
	// ContextFileAGENTSMD represents an AGENTS.md context file.
	ContextFileAGENTSMD ContextFileType = "AGENTS.md"
	// ContextFileCursorRules represents a .cursorrules context file.
	ContextFileCursorRules ContextFileType = ".cursorrules"
	// ContextFileGemini represents a .gemini/settings.json context file.
	ContextFileGemini ContextFileType = ".gemini/settings.json"
)

// ContextFile represents a detected context file in the project.
type ContextFile struct {
	Type      ContextFileType `json:"type"`
	Path      string          `json:"path"`
	Exists    bool            `json:"exists"`
	Content   string          `json:"content,omitempty"`
	SizeBytes int             `json:"size_bytes"`
}

// Suggestion represents a proposed change to a context file.
type Suggestion struct {
	ID                   string          `json:"id"`
	TargetKind           string          `json:"target_kind"`
	FileType             ContextFileType `json:"file_type,omitempty"`
	FilePath             string          `json:"file_path,omitempty"`
	SkillName            string          `json:"skill_name,omitempty"`
	Category             string          `json:"category"`
	Title                string          `json:"title"`
	Description          string          `json:"description"`
	Evidence             []string        `json:"evidence"`
	Priority             string          `json:"priority"` // "high", "medium", "low"
	CopyablePrompt       string          `json:"copyable_prompt,omitempty"`
	SuggestedInstruction string          `json:"suggested_instruction,omitempty"`
	Diff                 string          `json:"diff,omitempty"`
	CreatedAt            time.Time       `json:"created_at"`
	Status               string          `json:"status"` // "pending", "accepted", "rejected"
}

// ImprovementReport is the output of `entire improve`.
type ImprovementReport struct {
	ContextFiles  []ContextFile `json:"context_files"`
	Suggestions   []Suggestion  `json:"suggestions"`
	Facets        FacetSummary  `json:"facets"`
	FacetCounts   FacetCounts   `json:"facet_counts"`
	SessionsUsed  int           `json:"sessions_used"`
	FrictionTotal int           `json:"friction_total"`
	PatternsFound int           `json:"patterns_found"`
}

// FrictionPattern represents a recurring friction theme with evidence.
type FrictionPattern struct {
	Theme             string   // Normalized theme
	Count             int      // Occurrences across sessions
	Examples          []string // Raw friction text from summaries
	AffectedSessions  []string // Checkpoint IDs
	TranscriptExcerpt string   // Condensed transcript excerpt around friction (from deep-read)
}

// RecurringSignal represents a repeated structured signal across sessions.
type RecurringSignal struct {
	Value            string   `json:"value"`
	Count            int      `json:"count"`
	Evidence         []string `json:"evidence,omitempty"`
	AffectedSessions []string `json:"affected_sessions,omitempty"`
}

// SkillOpportunity identifies a skill that should likely be tightened.
type SkillOpportunity struct {
	SkillName          string   `json:"skill_name"`
	SkillPath          string   `json:"skill_path,omitempty"`
	Count              int      `json:"count"`
	Friction           []string `json:"friction,omitempty"`
	MissingInstruction string   `json:"missing_instruction,omitempty"`
	AffectedSessions   []string `json:"affected_sessions,omitempty"`
}

// ReviewDerivedRuleSignal captures a stable rule inferred from review feedback.
type ReviewDerivedRuleSignal struct {
	Rule             string   `json:"rule"`
	Count            int      `json:"count"`
	Strong           bool     `json:"strong,omitempty"`
	Evidence         []string `json:"evidence,omitempty"`
	SourceKinds      []string `json:"source_kinds,omitempty"`
	WhyReusable      string   `json:"why_reusable,omitempty"`
	AffectedSessions []string `json:"affected_sessions,omitempty"`
}

// PatternAnalysis contains extracted patterns from multiple sessions.
type PatternAnalysis struct {
	RepeatedFriction      []FrictionPattern         `json:"repeated_friction,omitempty"`
	RepeatedInstructions  []RecurringSignal         `json:"repeated_instructions,omitempty"`
	MissingContextSignals []RecurringSignal         `json:"missing_context_signals,omitempty"`
	FailureLoops          []RecurringSignal         `json:"failure_loops,omitempty"`
	SkillOpportunities    []SkillOpportunity        `json:"skill_opportunities,omitempty"`
	ReviewDerivedRules    []ReviewDerivedRuleSignal `json:"review_derived_rules,omitempty"`
	RepoLearnings         []string                  `json:"repo_learnings,omitempty"`
	WorkflowLearnings     []string                  `json:"workflow_learnings,omitempty"`
	OpenItems             []string                  `json:"open_items,omitempty"`
	SessionCount          int                       `json:"session_count"`
}

// FacetCounts summarizes how many structured signals were found.
type FacetCounts struct {
	RepeatedInstructions int `json:"repeated_instructions"`
	MissingContext       int `json:"missing_context"`
	FailureLoops         int `json:"failure_loops"`
	SkillSignals         int `json:"skill_signals"`
}

// FacetSummary exposes the raw structured signals that powered analysis.
type FacetSummary struct {
	RepeatedInstructions []RecurringSignal  `json:"repeated_instructions,omitempty"`
	MissingContext       []RecurringSignal  `json:"missing_context,omitempty"`
	FailureLoops         []RecurringSignal  `json:"failure_loops,omitempty"`
	SkillOpportunities   []SkillOpportunity `json:"skill_opportunities,omitempty"`
}

// SessionFacetsAlias keeps the public improve package tied to the extracted facet model.
type SessionFacetsAlias = facets.SessionFacets
