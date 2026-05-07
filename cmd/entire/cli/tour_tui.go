package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/entireio/cli/cmd/entire/cli/tour"
)

// errTourCancelled is returned when the user presses ctrl+c / q during
// the spinner. Surfaced as a normal cancellation, not an error.
var errTourCancelled = errors.New("tour cancelled")

// tourGenerateResult is the tea.Msg the TUI uses to deliver Generate's
// completion back to its Update loop.
type tourGenerateResult struct {
	result *tour.Result
	err    error
}

type tourStatusModel struct {
	ctx      context.Context
	cancel   context.CancelFunc
	spinner  spinner.Model
	styles   tourStatusStyles
	title    string
	subtitle string
	run      func(context.Context) (*tour.Result, error)
	out      tourGenerateResult
}

type tourStatusStyles struct {
	title    lipgloss.Style
	subtitle lipgloss.Style
	footer   lipgloss.Style
	spinner  lipgloss.Style
}

// runTourTUI runs the spinner program while Generate executes and returns
// Generate's result (or the cancellation error). The caller decides what to
// do with the markdown.
//
// Overridable for tests: tests assign a stub that returns a synthetic
// result without launching a real bubbletea program.
var runTourTUI = defaultRunTourTUI

func defaultRunTourTUI(ctx context.Context, w io.Writer, title, subtitle string, run func(context.Context) (*tour.Result, error)) (*tour.Result, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	model := newTourStatusModel(w, title, subtitle, run)
	model.ctx = runCtx
	model.cancel = cancel

	program := tea.NewProgram(model, tea.WithOutput(w))
	finalModel, err := program.Run()
	if err != nil {
		return nil, fmt.Errorf("run tour tui: %w", err)
	}

	finished, ok := finalModel.(tourStatusModel)
	if !ok {
		return nil, errors.New("unexpected tour loading state")
	}
	clearTourInlineView(w, finished.View().Content)
	if finished.out.err != nil {
		return nil, finished.out.err
	}
	return finished.out.result, nil
}

func newTourStatusModel(w io.Writer, title, subtitle string, run func(context.Context) (*tour.Result, error)) tourStatusModel {
	ss := newStatusStyles(w)
	styles := newTourStatusStyles(ss)
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	if ss.colorEnabled {
		sp.Style = styles.spinner
	}
	return tourStatusModel{
		spinner:  sp,
		styles:   styles,
		title:    title,
		subtitle: subtitle,
		run:      run,
	}
}

func newTourStatusStyles(ss statusStyles) tourStatusStyles {
	styles := tourStatusStyles{
		title:    lipgloss.NewStyle().Bold(true),
		subtitle: lipgloss.NewStyle(),
		footer:   lipgloss.NewStyle(),
		spinner:  lipgloss.NewStyle().Bold(true),
	}
	if !ss.colorEnabled {
		return styles
	}
	styles.title = styles.title.Foreground(lipgloss.Color("#fb923c"))
	styles.subtitle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	styles.footer = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	styles.spinner = lipgloss.NewStyle().Foreground(lipgloss.Color("#fb923c")).Bold(true)
	return styles
}

func (m tourStatusModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.runGenerate())
}

func (m tourStatusModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// View doesn't depend on terminal width — the status card lives
		// inline at a fixed-ish width — but we still drain the message
		// so bubbletea isn't queueing it indefinitely.
		_ = msg
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case tourGenerateResult:
		m.out = msg
		return m, tea.Quit
	case tea.KeyPressMsg:
		if key.Matches(msg, keys.Quit) || key.Matches(msg, keys.Back) {
			if m.cancel != nil {
				m.cancel()
			}
			m.out.err = errTourCancelled
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m tourStatusModel) View() tea.View {
	lines := []string{
		m.styles.spinner.Render(m.spinner.View()) + " " + m.styles.title.Render(m.title),
	}
	if m.subtitle != "" {
		lines = append(lines, m.styles.subtitle.Render(m.subtitle))
	}
	lines = append(lines, "", m.styles.footer.Render("Press ctrl+c to cancel"))
	return tea.NewView("\n" + strings.Join(lines, "\n"))
}

func (m tourStatusModel) runGenerate() tea.Cmd {
	return func() tea.Msg {
		result, err := m.run(m.ctx)
		return tourGenerateResult{result: result, err: err}
	}
}

func clearTourInlineView(w io.Writer, view string) {
	lineCount := strings.Count(view, "\n") + 1
	if view == "" {
		return
	}
	for range lineCount {
		_, _ = io.WriteString(w, "\x1b[1A\x1b[2K\r") //nolint:errcheck // terminal escape sequence, write errors are ignorable here
	}
}
