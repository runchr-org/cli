package permissions

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// TaskDefinition captures repo-local task source so Claude can judge safety from real task content.
type TaskDefinition struct {
	Name       string `json:"name"`
	SourcePath string `json:"source_path"`
	Content    string `json:"content,omitempty"`
}

// RepoFacts captures repo-local command/task definitions used during review.
type RepoFacts struct {
	MiseTasks map[string]TaskDefinition `json:"mise_tasks,omitempty"`
}

// ObservedCommand is a shell command observed in a recent session transcript.
type ObservedCommand struct {
	Agent        string `json:"agent"`
	CheckpointID string `json:"checkpoint_id"`
	SessionID    string `json:"session_id"`
	Command      string `json:"command"`
}

// TranscriptToolUse is the tool data needed to discover shell commands in a transcript.
type TranscriptToolUse struct {
	ToolName string
	Detail   string
}

// Candidate is a recurring candidate permission prefix for Claude to review.
type Candidate struct {
	Rule       string   `json:"rule"`
	Count      int      `json:"count"`
	SessionIDs []string `json:"session_ids,omitempty"`
	Examples   []string `json:"examples,omitempty"`
}

// AgentReview contains all recurring shell command candidates for one agent.
type AgentReview struct {
	Agent            string      `json:"agent"`
	SessionsAnalyzed int         `json:"sessions_analyzed"`
	Candidates       []Candidate `json:"candidates,omitempty"`
}

// ReviewInput is the transcript-derived evidence sent to Claude.
type ReviewInput struct {
	Agents           []AgentReview `json:"agents"`
	RepoFacts        RepoFacts     `json:"repo_facts,omitempty"`
	SessionsAnalyzed int           `json:"sessions_analyzed"`
	CommandsObserved int           `json:"commands_observed"`
}

// Suggestion is a conservative permission-list recommendation chosen by Claude.
type Suggestion struct {
	Rule         string   `json:"rule"`
	Reason       string   `json:"reason"`
	Count        int      `json:"count"`
	SessionIDs   []string `json:"session_ids,omitempty"`
	Examples     []string `json:"examples,omitempty"`
	Conservative bool     `json:"conservative"`
}

// AgentReport contains Claude-reviewed recommendations for one agent.
type AgentReport struct {
	Agent            string       `json:"agent"`
	SessionsAnalyzed int          `json:"sessions_analyzed"`
	Suggestions      []Suggestion `json:"suggestions,omitempty"`
}

// Report is the full advisory output for `entire permissions`.
type Report struct {
	Agents           []AgentReport `json:"agents"`
	SessionsAnalyzed int           `json:"sessions_analyzed"`
	CommandsObserved int           `json:"commands_observed"`
	SuggestionsFound int           `json:"suggestions_found"`
}

type candidateAggregate struct {
	Rule          string
	CheckpointIDs map[string]struct{}
	SessionIDs    map[string]struct{}
	Examples      []string
}

// AnalyzeCommands turns observed shell commands into recurring candidate prefixes for Claude review.
func AnalyzeCommands(observations []ObservedCommand, facts RepoFacts, knownAgents []string) ReviewInput {
	agents := normalizeAgents(knownAgents, observations)
	byAgent := make(map[string][]ObservedCommand, len(agents))
	sessionKeys := make(map[string]struct{})
	for _, obs := range observations {
		agentName := strings.TrimSpace(obs.Agent)
		if agentName == "" {
			agentName = "Unknown Agent"
		}
		byAgent[agentName] = append(byAgent[agentName], obs)

		key := agentName + "|" + firstNonEmpty(obs.SessionID, obs.CheckpointID)
		if key != "|" {
			sessionKeys[key] = struct{}{}
		}
	}

	input := ReviewInput{
		RepoFacts:        facts,
		SessionsAnalyzed: len(sessionKeys),
		CommandsObserved: len(observations),
	}

	for _, agentName := range agents {
		agentObservations := byAgent[agentName]
		aggregates := make(map[string]*candidateAggregate)
		for _, obs := range agentObservations {
			rule, ok := normalizeCommand(obs.Command)
			if !ok {
				continue
			}
			agg := aggregates[rule]
			if agg == nil {
				agg = &candidateAggregate{
					Rule:          rule,
					CheckpointIDs: make(map[string]struct{}),
					SessionIDs:    make(map[string]struct{}),
				}
				aggregates[rule] = agg
			}
			if obs.CheckpointID != "" {
				agg.CheckpointIDs[obs.CheckpointID] = struct{}{}
			}
			if obs.SessionID != "" {
				agg.SessionIDs[obs.SessionID] = struct{}{}
			}
			if obs.Command != "" && !slices.Contains(agg.Examples, obs.Command) && len(agg.Examples) < 5 {
				agg.Examples = append(agg.Examples, obs.Command)
			}
		}

		review := AgentReview{
			Agent:            agentName,
			SessionsAnalyzed: uniqueSessionCount(agentObservations),
		}
		for _, agg := range aggregates {
			if len(agg.CheckpointIDs) < 2 {
				continue
			}
			review.Candidates = append(review.Candidates, Candidate{
				Rule:       agg.Rule,
				Count:      len(agg.CheckpointIDs),
				SessionIDs: sortedKeys(agg.SessionIDs),
				Examples:   append([]string(nil), agg.Examples...),
			})
		}
		sort.Slice(review.Candidates, func(i, j int) bool {
			if review.Candidates[i].Count != review.Candidates[j].Count {
				return review.Candidates[i].Count > review.Candidates[j].Count
			}
			return review.Candidates[i].Rule < review.Candidates[j].Rule
		})

		input.Agents = append(input.Agents, review)
	}

	return input
}

// ExtractShellCommands returns shell commands from tool entries that represent shell execution.
func ExtractShellCommands(entries []TranscriptToolUse) []string {
	commands := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !isShellTool(entry.ToolName) {
			continue
		}
		detail := strings.TrimSpace(entry.Detail)
		if detail == "" {
			continue
		}
		commands = append(commands, detail)
	}
	return commands
}

// DiscoverRepoFacts inspects repo-local task definitions so Claude can judge command safety from real task content.
func DiscoverRepoFacts(repoRoot string) RepoFacts {
	facts := RepoFacts{MiseTasks: make(map[string]TaskDefinition)}
	if repoRoot == "" {
		return facts
	}

	loadInlineMiseTasks(repoRoot, facts.MiseTasks)
	loadScriptMiseTasks(repoRoot, facts.MiseTasks)

	if len(facts.MiseTasks) == 0 {
		facts.MiseTasks = nil
	}
	return facts
}

func normalizeAgents(knownAgents []string, observations []ObservedCommand) []string {
	seen := make(map[string]struct{}, len(knownAgents))
	var agents []string
	for _, agentName := range knownAgents {
		trimmed := strings.TrimSpace(agentName)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		agents = append(agents, trimmed)
	}
	for _, obs := range observations {
		trimmed := strings.TrimSpace(obs.Agent)
		if trimmed == "" {
			trimmed = "Unknown Agent"
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		agents = append(agents, trimmed)
	}
	sort.Strings(agents)
	return agents
}

func uniqueSessionCount(observations []ObservedCommand) int {
	keys := make(map[string]struct{}, len(observations))
	for _, obs := range observations {
		key := firstNonEmpty(obs.SessionID, obs.CheckpointID)
		if key == "" {
			continue
		}
		keys[key] = struct{}{}
	}
	return len(keys)
}

func normalizeCommand(command string) (string, bool) {
	command = strings.TrimSpace(command)
	if command == "" || hasUnsafeShellSyntax(command) {
		return "", false
	}

	fields := strings.Fields(command)
	if len(fields) < 2 {
		return "", false
	}

	switch fields[0] {
	case "mise":
		if len(fields) >= 3 && fields[1] == "run" {
			return strings.Join(fields[:3], " "), true
		}
	case "npm":
		if len(fields) >= 3 && fields[1] == "run" {
			return strings.Join(fields[:3], " "), true
		}
	case "pnpm":
		if len(fields) >= 2 {
			if fields[1] == "run" && len(fields) >= 3 {
				return strings.Join(fields[:3], " "), true
			}
			return strings.Join(fields[:2], " "), true
		}
	case "go":
		if fields[1] == "test" {
			return normalizeGoTest(fields), true
		}
	case "cargo":
		if fields[1] == "test" {
			return "cargo test", true
		}
	case "git":
		if len(fields) >= 2 {
			return strings.Join(fields[:2], " "), true
		}
	}

	if len(fields) >= 2 {
		return strings.Join(fields[:2], " "), true
	}
	return fields[0], true
}

func normalizeGoTest(fields []string) string {
	normalized := []string{"go", "test"}
	for i := 2; i < len(fields); i++ {
		switch fields[i] {
		case "-run":
			i++
			continue
		}
		if strings.HasPrefix(fields[i], "-run=") {
			continue
		}
		normalized = append(normalized, fields[i])
	}
	return strings.Join(normalized, " ")
}

func hasUnsafeShellSyntax(command string) bool {
	for _, fragment := range []string{
		"&&", "||", "|", ";", "\n", "\r", "`", "$(", ">", "<",
	} {
		if strings.Contains(command, fragment) {
			return true
		}
	}
	return false
}

func isShellTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "Bash", "bash", "run_command":
		return true
	default:
		return false
	}
}

func loadInlineMiseTasks(repoRoot string, tasks map[string]TaskDefinition) {
	content, err := os.ReadFile(filepath.Join(repoRoot, "mise.toml")) //nolint:gosec // fixed repo-local path
	if err != nil {
		return
	}

	lines := strings.Split(string(content), "\n")
	var current string
	var block []string
	flush := func() {
		if current == "" {
			return
		}
		tasks[current] = TaskDefinition{
			Name:       current,
			SourcePath: "mise.toml",
			Content:    strings.TrimSpace(strings.Join(block, "\n")),
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[tasks.") && strings.HasSuffix(trimmed, "]") {
			flush()
			current = parseTaskHeader(trimmed)
			block = block[:0]
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			flush()
			current = ""
			block = nil
			continue
		}
		if current != "" {
			block = append(block, line)
		}
	}
	flush()
}

func loadScriptMiseTasks(repoRoot string, tasks map[string]TaskDefinition) {
	root := filepath.Join(repoRoot, "mise-tasks")
	if _, err := os.Stat(root); err != nil {
		return
	}

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}

		taskName := strings.ReplaceAll(rel, string(filepath.Separator), ":")
		taskName = strings.TrimSuffix(taskName, ":_default")
		content, readErr := os.ReadFile(path) //nolint:gosec // path comes from walking repo-local tree
		if readErr != nil {
			return nil
		}
		tasks[taskName] = TaskDefinition{
			Name:       taskName,
			SourcePath: filepath.ToSlash(filepath.Join("mise-tasks", rel)),
			Content:    truncateTaskContent(string(content)),
		}
		return nil
	})
}

func parseTaskHeader(header string) string {
	trimmed := strings.TrimPrefix(header, "[tasks.")
	trimmed = strings.TrimSuffix(trimmed, "]")
	trimmed = strings.Trim(trimmed, `"`)
	return trimmed
}

func truncateTaskContent(content string) string {
	const maxLines = 40

	scanner := bufio.NewScanner(strings.NewReader(content))
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) >= maxLines {
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func sortedKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
