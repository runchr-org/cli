package cli

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// reviewTUIModel renders the multi-agent status table: one row per agent
// with spinner, status, duration, token usage, and a faint preview line.
// The model is a pure tea.Model — orchestration and subprocess wiring live
// in review_multi.go, which posts agentStateMsg / agentPreviewMsg as the
// underlying agents progress.
//
// On every row reaching a terminal state (Done, Failed, or Cancelled),
// Update returns tea.Quit so the program exits and the caller can dump
// full final responses.
type reviewTUIModel struct {
	tasks      []MultiAgentTask
	rows       []rowState
	spinner    spinner.Model
	startTime  time.Time
	termWidth  int
	allDone    bool
	cancelling bool
}

// rowState is the per-agent render state. Mutated only from Update via the
// incoming tea messages — never shared with orchestrator goroutines.
type rowState struct {
	name     string
	status   AgentRunStatus
	duration time.Duration
	tokens   int
	preview  string
	exitCode int
}

// agentStateMsg is posted by the orchestrator when an agent transitions
// between lifecycle states (queued → running → done/failed/cancelled) or
// when token usage becomes known after exit.
type agentStateMsg struct {
	Name     string
	Status   AgentRunStatus
	Duration time.Duration
	ExitCode int
	Tokens   int
}

// agentPreviewMsg is posted (rate-limited by the teeWriter) when an agent
// emits a new non-empty stdout line. The model ANSI-strips and width-
// truncates before rendering.
type agentPreviewMsg struct {
	Name string
	Line string
}

// cancelMsg toggles the "cancelling" banner. The orchestrator posts it
// when it starts to SIGTERM subprocesses after a user Ctrl+C.
type cancelMsg struct{}

// tickMsg fires every 100ms; drives per-row duration counters for rows
// still in AgentRunRunning. Avoids the need for the orchestrator to push
// periodic state updates just for the timer.
type tickMsg time.Time

// ansiRe matches standard CSI escape sequences. Agent stdout is rarely
// colored but users can pipe through `--color=always` wrappers — strip
// them so the TUI's own styling remains consistent.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// stripANSI removes CSI color codes from s so preview lines don't inject
// escape sequences into the TUI render.
func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// newReviewTUIModel constructs a model pre-populated with one queued row
// per task. Callers pass the same task list they'll hand the orchestrator,
// so row names line up with the agentStateMsg.Name values posted later.
func newReviewTUIModel(tasks []MultiAgentTask) reviewTUIModel {
	rows := make([]rowState, len(tasks))
	for i, t := range tasks {
		rows[i] = rowState{name: t.Name, status: AgentRunQueued}
	}
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	return reviewTUIModel{
		tasks:     tasks,
		rows:      rows,
		spinner:   sp,
		startTime: time.Now(),
		termWidth: 80,
	}
}

// Init kicks off the spinner animation and the duration-tick timer.
func (m reviewTUIModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, tickEvery())
}

// tickEvery schedules the next tickMsg 100ms in the future. Update
// re-schedules itself in response to each tickMsg it receives.
func tickEvery() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Update handles all model messages. Mutates m by value (tea.Model
// contract) and returns the updated model plus any follow-up commands.
//
//nolint:ireturn // tea.Model interface contract — bubbletea requires returning the interface.
func (m reviewTUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.termWidth = msg.Width
	case agentStateMsg:
		for i, r := range m.rows {
			if r.name == msg.Name {
				m.rows[i].status = msg.Status
				if msg.Duration > 0 {
					m.rows[i].duration = msg.Duration
				}
				m.rows[i].exitCode = msg.ExitCode
				m.rows[i].tokens = msg.Tokens
				break
			}
		}
		if m.allRowsTerminal() {
			m.allDone = true
			return m, tea.Quit
		}
	case agentPreviewMsg:
		for i, r := range m.rows {
			if r.name == msg.Name {
				m.rows[i].preview = truncatePreview(msg.Line, m.termWidth)
				break
			}
		}
	case cancelMsg:
		m.cancelling = true
	case tickMsg:
		now := time.Now()
		for i, r := range m.rows {
			if r.status == AgentRunRunning {
				m.rows[i].duration = now.Sub(m.startTime)
			}
		}
		return m, tickEvery()
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

// truncatePreview ANSI-strips line then trims to fit terminal width minus
// the row-prefix padding. A 20-char floor guards against absurdly narrow
// terminals where trimming would leave nothing meaningful.
func truncatePreview(line string, termWidth int) string {
	stripped := stripANSI(line)
	maxLine := termWidth - 18
	if maxLine < 20 {
		maxLine = 20
	}
	if len(stripped) > maxLine {
		stripped = stripped[:maxLine-1] + "…"
	}
	return stripped
}

// View renders the full status table. Called on every Update cycle.
func (m reviewTUIModel) View() string {
	var sb strings.Builder
	sb.WriteString("Running ")
	fmt.Fprintf(&sb, "%d agents — Ctrl+C to cancel all\n\n", len(m.rows))
	if m.cancelling {
		sb.WriteString("Cancelling — sending SIGTERM to subprocesses…\n\n")
	}
	sb.WriteString(strings.Repeat("─", 44) + "\n")
	for _, r := range m.rows {
		icon := rowIcon(r.status, m.spinner.View())
		status := statusString(r.status)
		dur := formatDuration(r.duration)
		tok := "—"
		if r.tokens > 0 {
			tok = fmt.Sprintf("%dk", r.tokens/1000)
		}
		line := fmt.Sprintf("  %s  %-15s %-10s %7s  %5s\n",
			icon, r.name, status, dur, tok)
		sb.WriteString(line)
		if r.preview != "" {
			sb.WriteString("       ")
			sb.WriteString(lipgloss.NewStyle().Faint(true).Render("… " + r.preview))
			sb.WriteString("\n")
		}
	}
	sb.WriteString(strings.Repeat("─", 44) + "\n")
	completed := 0
	for _, r := range m.rows {
		if isTerminalStatus(r.status) {
			completed++
		}
	}
	fmt.Fprintf(&sb, "%d of %d complete\n", completed, len(m.rows))
	return sb.String()
}

// allRowsTerminal returns true iff every row has a final status. Used to
// decide when to emit tea.Quit.
func (m reviewTUIModel) allRowsTerminal() bool {
	for _, r := range m.rows {
		if !isTerminalStatus(r.status) {
			return false
		}
	}
	return true
}

// isTerminalStatus reports whether an agent run has reached an endpoint
// (Done, Failed, or Cancelled) and will not transition further.
func isTerminalStatus(s AgentRunStatus) bool {
	switch s {
	case AgentRunDone, AgentRunFailed, AgentRunCancelled:
		return true
	case AgentRunQueued, AgentRunRunning:
		return false
	}
	return false
}

// rowIcon maps a status to its leading glyph; running rows render the
// live spinner character.
func rowIcon(status AgentRunStatus, spin string) string {
	switch status {
	case AgentRunDone:
		return "✓"
	case AgentRunFailed:
		return "✗"
	case AgentRunCancelled:
		return "⊘"
	case AgentRunRunning:
		return spin
	case AgentRunQueued:
		return " "
	}
	return " "
}

// statusString maps a status to a short lowercase label shown in the row.
func statusString(status AgentRunStatus) string {
	switch status {
	case AgentRunDone:
		return "done"
	case AgentRunFailed:
		return "failed"
	case AgentRunCancelled:
		return "cancelled"
	case AgentRunRunning:
		return "running"
	case AgentRunQueued:
		return "queued"
	}
	return "queued"
}

// formatDuration renders a duration as "Ns" under a minute, "Nm Ms"
// beyond. Zero duration renders as an em-dash so queued rows show blank-
// ish rather than "0s".
func formatDuration(d time.Duration) string {
	if d == 0 {
		return "—"
	}
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%dm %ds", s/60, s%60)
}
