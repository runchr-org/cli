package skilltui

import (
	"github.com/entireio/cli/cmd/entire/cli/skilldb"
	"github.com/entireio/cli/cmd/entire/cli/skillimprove"
)

// dataLoadedMsg is sent when the skill list and stats are loaded from the database.
type dataLoadedMsg struct {
	skills         []skilldb.SkillRow
	stats          map[string]*skilldb.SkillStatsResult // keyed by "name|source_agent"
	populateResult *skilldb.PopulateResult              // pipeline metrics for diagnostics
	err            error
}

// skillSelectedMsg is sent when the user picks a skill in the picker.
type skillSelectedMsg struct {
	skill skilldb.SkillRow
}

// skillDetailLoadedMsg is sent when full detail data for a skill is loaded.
type skillDetailLoadedMsg struct {
	stats        *skilldb.SkillStatsResult
	sessions     []skilldb.SkillSessionRow
	friction     []skilldb.FrictionThemeRow
	missing      []skilldb.MissingInstructionRow
	agents       []skilldb.AgentBreakdownRow
	improvements []skilldb.SkillImprovement
	err          error
}

// backToPickerMsg returns to the skill picker screen.
type backToPickerMsg struct{}

// generateStartedMsg indicates the user pressed 'g' to generate suggestions.
type generateStartedMsg struct{}

// generateDoneMsg contains the results of the LLM generation.
type generateDoneMsg struct {
	suggestions []skillimprove.SkillSuggestion
	err         error
}

// applyDiffMsg requests applying a diff for the suggestion at the given index.
type applyDiffMsg struct {
	index int
}

// applyDiffResultMsg contains the result of applying a diff.
type applyDiffResultMsg struct {
	index int
	err   error
}

// dismissSuggestionMsg removes the suggestion at the given index.
type dismissSuggestionMsg struct {
	index int
}

// refreshMsg re-runs the initial data loading.
type refreshMsg struct{}

// errorFlashMsg shows a temporary error message in the status bar.
type errorFlashMsg struct {
	text string
}

// clearErrorMsg clears the error flash after a timeout.
type clearErrorMsg struct{}
