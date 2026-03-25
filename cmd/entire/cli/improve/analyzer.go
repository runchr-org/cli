package improve

import "strings"

// maxFrictionExamples caps the number of raw examples stored per friction theme
// to prevent unbounded growth in LLM prompts.
const maxFrictionExamples = 10

// SessionSummaryData pairs a session identifier with its friction and learnings.
// This is populated from the insightsdb cache.
type SessionSummaryData struct {
	CheckpointID string
	Friction     []string
	Learnings    []LearningEntry
	OpenItems    []string
}

// LearningEntry represents a single learning from a session.
type LearningEntry struct {
	Scope   string // "repo", "workflow", "code"
	Finding string
	Path    string
}

// frictionThemeKeywords maps theme names to their detection keywords.
// Order matters: first match wins.
var frictionThemeKeywords = []struct {
	theme    string
	keywords []string
}{
	{"lint", []string{"lint", "golangci", "linter"}},
	{"import", []string{"import"}},
	{"compile", []string{"compile", "build error", "compilation"}},
	{"format", []string{"format", "fmt", "gofmt"}},
	{"test", []string{"test", "testing"}},
	{"type", []string{"type assertion", "type error", "type mismatch", "type check"}},
	{"permission", []string{"permission", "denied", "unauthorized"}},
	{"timeout", []string{"timeout", "timed out"}},
	{"retry", []string{"retry", "retrying"}},
}

// classifyFriction returns the theme keyword for a friction string,
// or "other" if no known keyword matches.
func classifyFriction(text string) string {
	lower := strings.ToLower(text)
	for _, entry := range frictionThemeKeywords {
		for _, kw := range entry.keywords {
			if strings.Contains(lower, kw) {
				return entry.theme
			}
		}
	}
	return "other"
}

// frictionAccumulator accumulates friction examples and affected sessions per theme.
type frictionAccumulator struct {
	count    int
	examples []string
	sessions map[string]struct{} // deduplicated session IDs
}

// AnalyzePatterns extracts recurring patterns from session summary data.
// This is the "index phase" — it works on data already in memory from SQLite.
func AnalyzePatterns(summaries []SessionSummaryData) PatternAnalysis {
	if len(summaries) == 0 {
		return PatternAnalysis{}
	}

	// Accumulate friction by theme
	byTheme := make(map[string]*frictionAccumulator)

	for _, s := range summaries {
		for _, f := range s.Friction {
			theme := classifyFriction(f)
			acc, ok := byTheme[theme]
			if !ok {
				acc = &frictionAccumulator{
					sessions: make(map[string]struct{}),
				}
				byTheme[theme] = acc
			}
			acc.count++
			if len(acc.examples) < maxFrictionExamples {
				acc.examples = append(acc.examples, f)
			}
			if s.CheckpointID != "" {
				acc.sessions[s.CheckpointID] = struct{}{}
			}
		}
	}

	// Build repeated friction list (threshold: 2+ occurrences)
	var repeated []FrictionPattern
	for theme, acc := range byTheme {
		if acc.count < 2 {
			continue
		}
		sessions := make([]string, 0, len(acc.sessions))
		for id := range acc.sessions {
			sessions = append(sessions, id)
		}
		repeated = append(repeated, FrictionPattern{
			Theme:            theme,
			Count:            acc.count,
			Examples:         acc.examples,
			AffectedSessions: sessions,
		})
	}

	// Deduplicate learnings by scope
	repoSeen := make(map[string]struct{})
	workflowSeen := make(map[string]struct{})
	var repoLearnings, workflowLearnings []string

	for _, s := range summaries {
		for _, l := range s.Learnings {
			switch l.Scope {
			case "repo":
				if _, seen := repoSeen[l.Finding]; !seen {
					repoSeen[l.Finding] = struct{}{}
					repoLearnings = append(repoLearnings, l.Finding)
				}
			case "workflow":
				if _, seen := workflowSeen[l.Finding]; !seen {
					workflowSeen[l.Finding] = struct{}{}
					workflowLearnings = append(workflowLearnings, l.Finding)
				}
			}
		}
	}

	// Deduplicate open items
	openSeen := make(map[string]struct{})
	var openItems []string
	for _, s := range summaries {
		for _, item := range s.OpenItems {
			if _, seen := openSeen[item]; !seen {
				openSeen[item] = struct{}{}
				openItems = append(openItems, item)
			}
		}
	}

	return PatternAnalysis{
		RepeatedFriction:  repeated,
		RepoLearnings:     repoLearnings,
		WorkflowLearnings: workflowLearnings,
		OpenItems:         openItems,
		SessionCount:      len(summaries),
	}
}
