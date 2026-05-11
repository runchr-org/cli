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

	"github.com/entireio/cli/cmd/entire/cli/learn"
)

// errLearnCancelled is returned when the user presses ctrl+c / q during
// the spinner. Surfaced as a normal cancellation, not an error.
var errLearnCancelled = errors.New("learn cancelled")

// learnGenerateResult is the tea.Msg the TUI uses to deliver Generate's
// completion back to its Update loop.
type learnGenerateResult struct {
	result *learn.Result
	err    error
}

type learnStatusModel struct {
	ctx      context.Context
	cancel   context.CancelFunc
	spinner  spinner.Model
	styles   learnStatusStyles
	title    string
	subtitle string
	run      func(context.Context) (*learn.Result, error)
	out      learnGenerateResult
}

type learnStatusStyles struct {
	title    lipgloss.Style
	subtitle lipgloss.Style
	footer   lipgloss.Style
	spinner  lipgloss.Style
}

// runLearnTUI runs the spinner program while Generate executes and returns
// Generate's result (or the cancellation error). The caller decides what to
// do with the markdown.
//
// Overridable for tests: tests assign a stub that returns a synthetic
// result without launching a real bubbletea program.
var runLearnTUI = defaultRunLearnTUI

func defaultRunLearnTUI(ctx context.Context, w io.Writer, title, subtitle string, run func(context.Context) (*learn.Result, error)) (*learn.Result, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	model := newLearnStatusModel(w, title, subtitle, run)
	model.ctx = runCtx
	model.cancel = cancel

	program := tea.NewProgram(model, tea.WithOutput(w))
	finalModel, err := program.Run()
	if err != nil {
		return nil, fmt.Errorf("run learn tui: %w", err)
	}

	finished, ok := finalModel.(learnStatusModel)
	if !ok {
		return nil, errors.New("unexpected learn loading state")
	}
	clearLearnInlineView(w, finished.View().Content)
	if finished.out.err != nil {
		return nil, finished.out.err
	}
	return finished.out.result, nil
}

func newLearnStatusModel(w io.Writer, title, subtitle string, run func(context.Context) (*learn.Result, error)) learnStatusModel {
	ss := newStatusStyles(w)
	styles := newLearnStatusStyles(ss)
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	if ss.colorEnabled {
		sp.Style = styles.spinner
	}
	return learnStatusModel{
		spinner:  sp,
		styles:   styles,
		title:    title,
		subtitle: subtitle,
		run:      run,
	}
}

func newLearnStatusStyles(ss statusStyles) learnStatusStyles {
	styles := learnStatusStyles{
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

func (m learnStatusModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.runGenerate())
}

func (m learnStatusModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
	case learnGenerateResult:
		m.out = msg
		return m, tea.Quit
	case tea.KeyPressMsg:
		if key.Matches(msg, keys.Quit) || key.Matches(msg, keys.Back) {
			if m.cancel != nil {
				m.cancel()
			}
			m.out.err = errLearnCancelled
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m learnStatusModel) View() tea.View {
	lines := []string{
		m.styles.spinner.Render(m.spinner.View()) + " " + m.styles.title.Render(m.title),
	}
	if m.subtitle != "" {
		lines = append(lines, m.styles.subtitle.Render(m.subtitle))
	}
	lines = append(lines, "", m.styles.footer.Render("Press ctrl+c to cancel"))
	return tea.NewView("\n" + strings.Join(lines, "\n"))
}

func (m learnStatusModel) runGenerate() tea.Cmd {
	return func() tea.Msg {
		result, err := m.run(m.ctx)
		return learnGenerateResult{result: result, err: err}
	}
}

func clearLearnInlineView(w io.Writer, view string) {
	lineCount := strings.Count(view, "\n") + 1
	if view == "" {
		return
	}
	for range lineCount {
		_, _ = io.WriteString(w, "\x1b[1A\x1b[2K\r") //nolint:errcheck // terminal escape sequence, write errors are ignorable here
	}
}
