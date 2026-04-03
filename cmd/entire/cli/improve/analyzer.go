package improve

import (
	"slices"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/facets"
)

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
	Facets       facets.SessionFacets
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
	{"test", []string{"test", "testing", "test failure"}},
	{"type", []string{"type assertion", "type error", "type mismatch", "type check"}},
	{"permission", []string{"permission", "denied", "unauthorized"}},
	{"timeout", []string{"timeout", "timed out"}},
	{"retry", []string{"retry", "retrying"}},
	{"api", []string{"api", "500", "http error", "rate limit"}},
	{"ci", []string{"ci failure", "ci ", "pipeline"}},
	{"conflict", []string{"conflict", "merge", "concurrent edit", "rebase"}},
	{"review", []string{"review", "pr review", "copilot review"}},
	{"scope", []string{"scope", "out of scope", "unrelated"}},
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

type recurringSignalAccumulator struct {
	count    int
	evidence []string
	sessions map[string]struct{}
}

type skillOpportunityAccumulator struct {
	skillPath          string
	count              int
	friction           []string
	missingInstruction string
	sessions           map[string]struct{}
}

type reviewRuleAccumulator struct {
	count       int
	strong      bool
	evidence    []string
	sourceKinds []string
	whyReusable string
	sessions    map[string]struct{}
}

// AnalyzePatterns extracts recurring patterns from session summary data.
// This is the "index phase" — it works on data already in memory from SQLite.
func AnalyzePatterns(summaries []SessionSummaryData) PatternAnalysis {
	if len(summaries) == 0 {
		return PatternAnalysis{}
	}

	// Accumulate friction by theme
	byTheme := make(map[string]*frictionAccumulator)
	instructionSignals := make(map[string]*recurringSignalAccumulator)
	missingContextSignals := make(map[string]*recurringSignalAccumulator)
	failureLoopSignals := make(map[string]*recurringSignalAccumulator)
	skillSignals := make(map[string]*skillOpportunityAccumulator)
	reviewRules := make(map[string]*reviewRuleAccumulator)

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

		for _, instruction := range s.Facets.RepeatedUserInstructions {
			accumulateRecurringSignal(instructionSignals, instruction.Instruction, instruction.Evidence, s.CheckpointID)
		}
		for _, signal := range s.Facets.MissingContext {
			accumulateRecurringSignal(missingContextSignals, signal.Item, signal.Evidence, s.CheckpointID)
		}
		for _, loop := range s.Facets.FailureLoops {
			key := loop.Description
			acc := failureLoopSignals[key]
			if acc == nil {
				acc = &recurringSignalAccumulator{sessions: make(map[string]struct{})}
				failureLoopSignals[key] = acc
			}
			acc.count += max(loop.Count, 1)
			accumulateEvidence(acc, loop.Evidence)
			if s.CheckpointID != "" {
				acc.sessions[s.CheckpointID] = struct{}{}
			}
		}
		for _, signal := range s.Facets.SkillSignals {
			key := signal.SkillName
			if key == "" {
				continue
			}
			acc := skillSignals[key]
			if acc == nil {
				acc = &skillOpportunityAccumulator{sessions: make(map[string]struct{})}
				skillSignals[key] = acc
			}
			acc.count++
			if acc.skillPath == "" {
				acc.skillPath = signal.SkillPath
			}
			if acc.missingInstruction == "" {
				acc.missingInstruction = signal.MissingInstruction
			}
			acc.friction = appendLimited(acc.friction, signal.Friction, maxFrictionExamples)
			if s.CheckpointID != "" {
				acc.sessions[s.CheckpointID] = struct{}{}
			}
		}
		for _, rule := range s.Facets.ReviewDerivedRules {
			key := rule.Rule
			if key == "" {
				continue
			}
			acc := reviewRules[key]
			if acc == nil {
				acc = &reviewRuleAccumulator{sessions: make(map[string]struct{})}
				reviewRules[key] = acc
			}
			acc.count++
			if strings.EqualFold(rule.Strength, "strong") {
				acc.strong = true
			}
			acc.evidence = appendLimited(acc.evidence, rule.Evidence, maxFrictionExamples)
			if rule.SourceKind != "" && !slices.Contains(acc.sourceKinds, rule.SourceKind) {
				acc.sourceKinds = append(acc.sourceKinds, rule.SourceKind)
			}
			if acc.whyReusable == "" {
				acc.whyReusable = rule.WhyReusable
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

	repeatedInstructions := buildRecurringSignals(instructionSignals)
	missingSignals := buildRecurringSignals(missingContextSignals)
	failureLoops := buildRecurringSignals(failureLoopSignals)
	skillOpportunities := buildSkillOpportunities(skillSignals)
	reviewDerivedRules := buildReviewDerivedRules(reviewRules)

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
		RepeatedFriction:      repeated,
		RepeatedInstructions:  repeatedInstructions,
		MissingContextSignals: missingSignals,
		FailureLoops:          failureLoops,
		SkillOpportunities:    skillOpportunities,
		ReviewDerivedRules:    reviewDerivedRules,
		RepoLearnings:         repoLearnings,
		WorkflowLearnings:     workflowLearnings,
		OpenItems:             openItems,
		SessionCount:          len(summaries),
	}
}

func accumulateRecurringSignal(target map[string]*recurringSignalAccumulator, value string, evidence []string, checkpointID string) {
	if value == "" {
		return
	}
	acc := target[value]
	if acc == nil {
		acc = &recurringSignalAccumulator{sessions: make(map[string]struct{})}
		target[value] = acc
	}
	acc.count++
	accumulateEvidence(acc, evidence)
	if checkpointID != "" {
		acc.sessions[checkpointID] = struct{}{}
	}
}

func accumulateEvidence(acc *recurringSignalAccumulator, evidence []string) {
	acc.evidence = appendLimited(acc.evidence, evidence, maxFrictionExamples)
}

func appendLimited(dst, src []string, limit int) []string {
	for _, item := range src {
		if item == "" || len(dst) >= limit {
			break
		}
		dst = append(dst, item)
	}
	return dst
}

func buildRecurringSignals(byValue map[string]*recurringSignalAccumulator) []RecurringSignal {
	signals := make([]RecurringSignal, 0, len(byValue))
	for value, acc := range byValue {
		if acc.count < 2 {
			continue
		}
		signals = append(signals, RecurringSignal{
			Value:            value,
			Count:            acc.count,
			Evidence:         acc.evidence,
			AffectedSessions: sessionIDs(acc.sessions),
		})
	}
	return signals
}

func buildSkillOpportunities(bySkill map[string]*skillOpportunityAccumulator) []SkillOpportunity {
	opportunities := make([]SkillOpportunity, 0, len(bySkill))
	for skillName, acc := range bySkill {
		if acc.count < 1 {
			continue
		}
		opportunities = append(opportunities, SkillOpportunity{
			SkillName:          skillName,
			SkillPath:          acc.skillPath,
			Count:              acc.count,
			Friction:           acc.friction,
			MissingInstruction: acc.missingInstruction,
			AffectedSessions:   sessionIDs(acc.sessions),
		})
	}
	return opportunities
}

func buildReviewDerivedRules(byRule map[string]*reviewRuleAccumulator) []ReviewDerivedRuleSignal {
	rules := make([]ReviewDerivedRuleSignal, 0, len(byRule))
	for rule, acc := range byRule {
		rules = append(rules, ReviewDerivedRuleSignal{
			Rule:             rule,
			Count:            acc.count,
			Strong:           acc.strong,
			Evidence:         acc.evidence,
			SourceKinds:      acc.sourceKinds,
			WhyReusable:      acc.whyReusable,
			AffectedSessions: sessionIDs(acc.sessions),
		})
	}
	return rules
}

func sessionIDs(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for id := range values {
		out = append(out, id)
	}
	return out
}
