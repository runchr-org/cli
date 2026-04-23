package cli

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/teatest"
)

// TestTUIModel_InitialViewAllQueued pins that newReviewTUIModel renders all
// configured agents as "queued" before any state messages arrive.
func TestTUIModel_InitialViewAllQueued(t *testing.T) {
	t.Parallel()
	m := newReviewTUIModel([]MultiAgentTask{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	})
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
	m := newReviewTUIModel([]MultiAgentTask{{Name: "a"}})
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
	m := newReviewTUIModel([]MultiAgentTask{{Name: "a"}})
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 20))
	tm.Send(agentStateMsg{Name: "a", Status: AgentRunDone, Duration: time.Second})
	tm.WaitFinished(t, teatest.WithFinalTimeout(time.Second))
}

// TestTUIModel_PreviewTruncates pins that a preview line longer than the
// terminal width is truncated in the rendered view.
func TestTUIModel_PreviewTruncates(t *testing.T) {
	t.Parallel()
	m := newReviewTUIModel([]MultiAgentTask{{Name: "a"}})
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
