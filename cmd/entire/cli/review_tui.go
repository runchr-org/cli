package cli

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/entireio/cli/cmd/entire/cli/stringutil"
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
	termHeight int
	allDone    bool
	cancelling bool
	// onCancel, if non-nil, is invoked the first time the user triggers
	// cancellation from within the TUI (Ctrl+C or 'q'). The orchestrator
	// wires this to its runCtx cancel function so raw-mode-captured Ctrl+C
	// still tears down subprocesses — bubbletea intercepts byte 0x03 before
	// it can reach the orchestrator's signal.Notify handler.
	onCancel func()
	// buffers holds one shared tee-buffer per task, aligned with tasks by
	// index. The TUI snapshots the active agent's buffer on every repaint
	// while detailMode is on. nil when cancellation/test harnesses don't
	// need drill-in.
	buffers []*agentBuffer
	// detailMode toggles the full-screen transcript view entered via
	// Ctrl+O. While on, arrow keys navigate between agents and scroll the
	// buffer; Esc returns to the dashboard.
	detailMode   bool
	detailIdx    int
	detailScroll int
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
	// runStart is set when the row transitions to AgentRunRunning and
	// drives the live duration counter on tickMsg. Using m.startTime
	// (TUI start) instead inflated durations for agents that started
	// later — e.g., a second-spawned agent showed time-since-TUI rather
	// than time-since-its-own-start.
	runStart time.Time
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

// tickMsg fires every 100ms; drives per-row duration counters for rows
// still in AgentRunRunning. Avoids the need for the orchestrator to push
// periodic state updates just for the timer.
type tickMsg time.Time

// ansiRe matches standard CSI escape sequences, including private-mode
// forms like `\x1b[?25l` (cursor hide) that codex uses. Agent stdout is
// rarely colored but users can pipe through `--color=always` wrappers —
// strip them so the TUI's own styling remains consistent.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// stripANSI removes CSI color codes from s so preview lines don't inject
// escape sequences into the TUI render.
func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// newReviewTUIModel constructs a model pre-populated with one queued row
// per task. Callers pass the same task list they'll hand the orchestrator,
// so row names line up with the agentStateMsg.Name values posted later.
// onCancel, if non-nil, is invoked the first time the user presses Ctrl+C
// or 'q' inside the TUI so the orchestrator can tear down subprocesses.
// Pass nil when cancellation wiring isn't needed (e.g. in unit tests).
// buffers, when non-nil, enables the Ctrl+O drill-in: one entry per task,
// aligned by index, sharing the same tees the orchestrator feeds.
func newReviewTUIModel(tasks []MultiAgentTask, onCancel func(), buffers []*agentBuffer) reviewTUIModel {
	rows := make([]rowState, len(tasks))
	for i, t := range tasks {
		rows[i] = rowState{name: t.Name, status: AgentRunQueued}
	}
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	return reviewTUIModel{
		tasks:      tasks,
		rows:       rows,
		spinner:    sp,
		startTime:  time.Now(),
		termWidth:  80,
		termHeight: 24,
		onCancel:   onCancel,
		buffers:    buffers,
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
		m.termHeight = msg.Height
	case tea.KeyMsg:
		// Detail-mode key handling is a separate state: arrow keys
		// navigate + scroll, Esc exits, Ctrl+O cycles agents. Ctrl+C is
		// deliberately ignored here — the user must Esc back to the
		// dashboard before cancelling so they don't accidentally kill a
		// run while scrolling. Guarded on len(m.tasks) > 0 so an empty
		// task list (defensive — shouldn't occur in practice) can't hit
		// a modulo-by-zero panic.
		if m.detailMode && len(m.tasks) > 0 {
			switch msg.Type { //nolint:exhaustive // only navigation keys matter in detail mode; every other key is a no-op
			case tea.KeyEsc:
				m.detailMode = false
				// Exit alt-screen so the dashboard re-renders over the
				// primary screen buffer, leaving the drill-in history out
				// of terminal scrollback.
				return m, tea.ExitAltScreen
			case tea.KeyCtrlO, tea.KeyRight:
				m.detailIdx = (m.detailIdx + 1) % len(m.tasks)
				m.detailScroll = 0
			case tea.KeyLeft:
				m.detailIdx = (m.detailIdx - 1 + len(m.tasks)) % len(m.tasks)
				m.detailScroll = 0
			case tea.KeyUp:
				m.detailScroll++
			case tea.KeyDown:
				if m.detailScroll > 0 {
					m.detailScroll--
				}
			}
			return m, nil
		}
		// Dashboard: Ctrl+O enters the drill-in view. Requires buffers
		// to be wired — without them the detail view has nothing to
		// render. Enter alt-screen so the full-screen transcript view
		// doesn't leave scrollback artifacts when the buffer grows mid-
		// view; dashboard stays on the primary screen.
		if msg.Type == tea.KeyCtrlO && len(m.buffers) > 0 {
			m.detailMode = true
			m.detailIdx = 0
			m.detailScroll = 0
			return m, tea.EnterAltScreen
		}
		// Ctrl+C (or 'q') flips the cancelling banner immediately for
		// user feedback and fires the orchestrator-supplied cancel hook.
		// The hook must be called from here — bubbletea puts the terminal
		// in raw mode, so byte 0x03 is captured as a KeyMsg and never
		// reaches the OS as SIGINT. The orchestrator's signal.Notify
		// handler therefore never fires for in-TUI Ctrl+C; we have to
		// cancel runCtx directly via m.onCancel. Guarded on !m.cancelling
		// so repeat key presses don't re-fire the hook.
		if msg.Type == tea.KeyCtrlC || msg.String() == "q" {
			if !m.cancelling && m.onCancel != nil {
				m.onCancel()
			}
			m.cancelling = true
		}
	case agentStateMsg:
		for i, r := range m.rows {
			if r.name == msg.Name {
				// Stamp runStart on the queued→running transition so the
				// live duration counter measures from this agent's actual
				// launch, not the TUI's startTime.
				if msg.Status == AgentRunRunning && r.status != AgentRunRunning {
					m.rows[i].runStart = time.Now()
				}
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
	case tickMsg:
		now := time.Now()
		for i, r := range m.rows {
			if r.status == AgentRunRunning && !r.runStart.IsZero() {
				m.rows[i].duration = now.Sub(r.runStart)
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
// terminals where trimming would leave nothing meaningful. Rune-based
// truncation via stringutil.TruncateRunes keeps multi-byte UTF-8 (e.g.
// non-ASCII narrative output from codex/gemini) from being split mid-
// rune, which would otherwise emit invalid bytes into the TUI render.
func truncatePreview(line string, termWidth int) string {
	stripped := stripANSI(line)
	maxLine := termWidth - 18
	if maxLine < 20 {
		maxLine = 20
	}
	return stringutil.TruncateRunes(stripped, maxLine, "…")
}

// View renders the full status table. Called on every Update cycle.
// Dispatches to detailView when the Ctrl+O drill-in is active.
func (m reviewTUIModel) View() string {
	if m.detailMode {
		return m.detailView()
	}
	var sb strings.Builder
	sb.WriteString("Running ")
	fmt.Fprintf(&sb, "%d agents — Ctrl+C to cancel · Ctrl+O to view agent\n\n", len(m.rows))
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

// detailView renders a full-screen, scrollable slice of the active
// agent's stdout buffer. The header shows the agent name, position in
// the list, and available keybindings. Scroll offset counts lines back
// from the newest: detailScroll=0 pins the tail (the most recent output),
// positive values page upward through history.
func (m reviewTUIModel) detailView() string {
	task := m.tasks[m.detailIdx]
	var data []byte
	if m.detailIdx < len(m.buffers) && m.buffers[m.detailIdx] != nil {
		data = m.buffers[m.detailIdx].Snapshot()
	}
	lines := strings.Split(stripANSI(string(data)), "\n")

	viewport := m.termHeight - 3
	if viewport < 5 {
		viewport = 5
	}

	// Clamp scroll so pressing Up past the top of the buffer doesn't
	// leave start/end inverted.
	maxScroll := len(lines) - viewport
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.detailScroll > maxScroll {
		m.detailScroll = maxScroll
	}

	end := len(lines) - m.detailScroll
	if end > len(lines) {
		end = len(lines)
	}
	start := end - viewport
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}

	maxLineWidth := m.termWidth
	if maxLineWidth < 20 {
		maxLineWidth = 20
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "< %s > (agent %d of %d) — Esc: back | ←/→: switch | ↑/↓: scroll\n",
		task.Name, m.detailIdx+1, len(m.tasks))
	sb.WriteString(strings.Repeat("─", min(60, maxLineWidth)) + "\n")
	rendered := 0
	for _, line := range lines[start:end] {
		// Truncate wide lines so they can't wrap and throw off bubbletea's
		// frame-line count. Scroll with ↑/↓ if you need to see the tail.
		// Rune-based truncation matches the preview path (truncatePreview)
		// so multi-byte UTF-8 in agent narrative doesn't get split mid-rune.
		line = stringutil.TruncateRunes(line, maxLineWidth, "…")
		sb.WriteString(line + "\n")
		rendered++
	}
	// Pad to fixed viewport height so every frame returns the same line
	// count — required for bubbletea's inline frame diff (even in alt-
	// screen, a stable line count avoids repaint flicker).
	for i := rendered; i < viewport; i++ {
		sb.WriteByte('\n')
	}
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
