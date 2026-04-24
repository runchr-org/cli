package cli

import (
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

// TestTUIModel_InitialViewAllQueued pins that newReviewTUIModel renders all
// configured agents as "queued" before any state messages arrive.
func TestTUIModel_InitialViewAllQueued(t *testing.T) {
	t.Parallel()
	m := newReviewTUIModel([]MultiAgentTask{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	}, nil, nil)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 20))

	// Force quit quickly so FinalOutput terminates. The model exits on
	// its own only when all rows are terminal; here we haven't sent any
	// state msgs, so we need an explicit Quit.
	if err := tm.Quit(); err != nil {
		t.Fatalf("Quit: %v", err)
	}
	out, readErr := io.ReadAll(tm.FinalOutput(t, teatest.WithFinalTimeout(time.Second)))
	if readErr != nil {
		t.Fatalf("read FinalOutput: %v", readErr)
	}
	view := string(out)
	if !strings.Contains(view, "queued") {
		t.Errorf("initial view should contain 'queued'; got:\n%s", view)
	}
	if !strings.Contains(view, "a") || !strings.Contains(view, "b") || !strings.Contains(view, "c") {
		t.Errorf("initial view should list all 3 agents; got:\n%s", view)
	}
}

// TestTUIModel_TransitionsToRunningOnMsg pins that sending an agentStateMsg
// with Status=AgentRunRunning causes the view to reflect "running".
func TestTUIModel_TransitionsToRunningOnMsg(t *testing.T) {
	t.Parallel()
	m := newReviewTUIModel([]MultiAgentTask{{Name: "a"}}, nil, nil)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 20))
	tm.Send(agentStateMsg{Name: "a", Status: AgentRunRunning})
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(string(b), "running")
	}, teatest.WithDuration(time.Second))
	if err := tm.Quit(); err != nil {
		t.Fatalf("Quit: %v", err)
	}
	tm.WaitFinished(t, teatest.WithFinalTimeout(time.Second))
}

// TestTUIModel_DoneQuits pins that once every agent reaches a terminal
// state, the model returns tea.Quit without an explicit Quit call.
func TestTUIModel_DoneQuits(t *testing.T) {
	t.Parallel()
	m := newReviewTUIModel([]MultiAgentTask{{Name: "a"}}, nil, nil)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 20))
	tm.Send(agentStateMsg{Name: "a", Status: AgentRunDone, Duration: time.Second})
	tm.WaitFinished(t, teatest.WithFinalTimeout(time.Second))
}

// TestTUIModel_PreviewTruncates pins that a preview line longer than the
// terminal width is truncated in the rendered view.
func TestTUIModel_PreviewTruncates(t *testing.T) {
	t.Parallel()
	m := newReviewTUIModel([]MultiAgentTask{{Name: "a"}}, nil, nil)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(60, 20))
	longLine := strings.Repeat("x", 300)
	tm.Send(agentPreviewMsg{Name: "a", Line: longLine})
	// Drive to a terminal state so the model can exit naturally.
	tm.Send(agentStateMsg{Name: "a", Status: AgentRunDone})
	tm.WaitFinished(t, teatest.WithFinalTimeout(time.Second))

	out, readErr := io.ReadAll(tm.FinalOutput(t, teatest.WithFinalTimeout(time.Second)))
	if readErr != nil {
		t.Fatalf("read FinalOutput: %v", readErr)
	}
	view := string(out)
	// View must not contain a 100-char unbroken run of 'x' — that would
	// mean truncation was disabled or mis-sized for the terminal width.
	if strings.Contains(view, strings.Repeat("x", 100)) {
		t.Errorf("preview should truncate at terminal width; got:\n%s", view)
	}
}

// TestTUIModel_CtrlOEntersDetailMode pins that Ctrl+O switches the TUI
// into the full-screen drill-in view and renders the active agent's
// buffered stdout.
func TestTUIModel_CtrlOEntersDetailMode(t *testing.T) {
	t.Parallel()
	buf := &agentBuffer{}
	if _, err := buf.Write([]byte("some content\n")); err != nil {
		t.Fatalf("buf.Write: %v", err)
	}
	m := newReviewTUIModel(
		[]MultiAgentTask{{Name: "a"}},
		nil,
		[]*agentBuffer{buf},
	)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 20))
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlO})
	tm.Send(agentStateMsg{Name: "a", Status: AgentRunDone, Duration: time.Second})
	out, readErr := io.ReadAll(tm.FinalOutput(t, teatest.WithFinalTimeout(500*time.Millisecond)))
	if readErr != nil {
		t.Fatalf("read FinalOutput: %v", readErr)
	}
	view := string(out)
	if !strings.Contains(view, "agent 1 of 1") {
		t.Errorf("expected detail view header; got:\n%s", view)
	}
	if !strings.Contains(view, "some content") {
		t.Errorf("expected buffer content in detail view; got:\n%s", view)
	}
}

// TestTUIModel_EscExitsDetailMode pins that Esc returns the TUI to the
// dashboard. After Esc the final repaint must be in dashboard format
// (the "N of M complete" footer), not the detail-mode header.
func TestTUIModel_EscExitsDetailMode(t *testing.T) {
	t.Parallel()
	buf := &agentBuffer{}
	if _, err := buf.Write([]byte("content\n")); err != nil {
		t.Fatalf("buf.Write: %v", err)
	}
	m := newReviewTUIModel(
		[]MultiAgentTask{{Name: "a"}},
		nil,
		[]*agentBuffer{buf},
	)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 20))
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlO})
	tm.Send(tea.KeyMsg{Type: tea.KeyEsc})
	tm.Send(agentStateMsg{Name: "a", Status: AgentRunDone, Duration: time.Second})
	out, readErr := io.ReadAll(tm.FinalOutput(t, teatest.WithFinalTimeout(500*time.Millisecond)))
	if readErr != nil {
		t.Fatalf("read FinalOutput: %v", readErr)
	}
	view := string(out)
	if strings.Contains(view, "agent 1 of 1") {
		t.Errorf("detail header should not be in final output after Esc; got:\n%s", view)
	}
	if !strings.Contains(view, "1 of 1 complete") {
		t.Errorf("dashboard footer missing after Esc; got:\n%s", view)
	}
}

// TestTUIModel_RightArrowSwitchesAgent pins that Right arrow in detail
// mode advances detailIdx so the next agent's buffer is rendered.
func TestTUIModel_RightArrowSwitchesAgent(t *testing.T) {
	t.Parallel()
	bufA := &agentBuffer{}
	if _, err := bufA.Write([]byte("A-content\n")); err != nil {
		t.Fatalf("bufA.Write: %v", err)
	}
	bufB := &agentBuffer{}
	if _, err := bufB.Write([]byte("B-content\n")); err != nil {
		t.Fatalf("bufB.Write: %v", err)
	}
	m := newReviewTUIModel(
		[]MultiAgentTask{{Name: "a"}, {Name: "b"}},
		nil,
		[]*agentBuffer{bufA, bufB},
	)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 20))
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlO})
	tm.Send(tea.KeyMsg{Type: tea.KeyRight})
	tm.Send(agentStateMsg{Name: "a", Status: AgentRunDone, Duration: time.Second})
	tm.Send(agentStateMsg{Name: "b", Status: AgentRunDone, Duration: time.Second})
	out, readErr := io.ReadAll(tm.FinalOutput(t, teatest.WithFinalTimeout(500*time.Millisecond)))
	if readErr != nil {
		t.Fatalf("read FinalOutput: %v", readErr)
	}
	view := string(out)
	if !strings.Contains(view, "agent 2 of 2") {
		t.Errorf("right arrow should switch to agent 2 of 2; got:\n%s", view)
	}
}

// TestTUIModel_KeyCtrlCCallsOnCancel pins the fix for the raw-mode Ctrl+C
// bug: when bubbletea delivers KeyCtrlC to the model, the onCancel hook
// must fire so the orchestrator can tear down subprocesses. Without this,
// Ctrl+C only flips the banner and the subprocesses keep running because
// the terminal swallowed the 0x03 byte before it could become a SIGINT.
func TestTUIModel_KeyCtrlCCallsOnCancel(t *testing.T) {
	t.Parallel()
	called := false
	m := newReviewTUIModel([]MultiAgentTask{{Name: "a"}}, func() { called = true }, nil)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 20))
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	// Send a terminal state so the model quits and the test finishes.
	tm.Send(agentStateMsg{Name: "a", Status: AgentRunCancelled, Duration: time.Second})
	tm.WaitFinished(t, teatest.WithFinalTimeout(500*time.Millisecond))
	if !called {
		t.Error("onCancel was not called on Ctrl+C")
	}
}
