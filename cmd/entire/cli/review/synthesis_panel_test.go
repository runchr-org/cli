package review

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// stubJudge is a SynthesisProvider returning canned output (or an error).
type stubJudge struct {
	out      string
	err      error
	lastSeen string // the prompt it was asked to synthesize
	calls    int
}

func (s *stubJudge) Synthesize(_ context.Context, prompt string) (string, error) {
	s.calls++
	s.lastSeen = prompt
	return s.out, s.err
}

func TestPanel_SingleJudgePassesThrough(t *testing.T) {
	j := &stubJudge{out: "verdict A"}
	p := PanelSynthesisProvider{Judges: []SynthesisProvider{j}, Labels: []string{"a"}}
	got, err := p.Synthesize(context.Background(), "PROMPT")
	if err != nil || got != "verdict A" {
		t.Fatalf("got (%q,%v), want (verdict A, nil)", got, err)
	}
	if j.lastSeen != "PROMPT" {
		t.Errorf("single judge should get the raw prompt, got %q", j.lastSeen)
	}
}

func TestPanel_MultiJudgeChairMerges(t *testing.T) {
	j1 := &stubJudge{out: "ship it"}
	j2 := &stubJudge{out: "block: race in cache.go"}
	chair := &stubJudge{out: "FINAL: block — j2 found a real race"}
	// chair is judge index 2.
	p := PanelSynthesisProvider{
		Judges:   []SynthesisProvider{j1, j2, chair},
		Labels:   []string{"claude", "codex", "chair"},
		ChairIdx: 2,
	}
	got, err := p.Synthesize(context.Background(), "PROMPT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "FINAL: block") {
		t.Errorf("final verdict should come from the chair, got:\n%s", got)
	}
	// Panel appendix shows each judge's verdict.
	for _, want := range []string{"## Panel", "ship it", "block: race in cache.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	// The chair was asked to merge (its prompt mentions the other verdicts).
	if !strings.Contains(chair.lastSeen, "ship it") || !strings.Contains(chair.lastSeen, "race in cache.go") {
		t.Errorf("chair prompt should include the panel verdicts, got:\n%s", chair.lastSeen)
	}
}

func TestPanel_DroppedFailuresCollapseToSingle(t *testing.T) {
	good := &stubJudge{out: "only verdict"}
	bad := &stubJudge{err: errors.New("boom")}
	p := PanelSynthesisProvider{
		Judges:   []SynthesisProvider{bad, good},
		Labels:   []string{"bad", "good"},
		ChairIdx: 0, // chair failed → falls back to the surviving judge
	}
	got, err := p.Synthesize(context.Background(), "PROMPT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "only verdict" {
		t.Errorf("one survivor should pass through without a panel, got:\n%s", got)
	}
}

func TestPanel_AllFailErrors(t *testing.T) {
	p := PanelSynthesisProvider{
		Judges: []SynthesisProvider{
			&stubJudge{err: errors.New("e1")},
			&stubJudge{err: errors.New("e2")},
		},
		Labels: []string{"a", "b"},
	}
	if _, err := p.Synthesize(context.Background(), "PROMPT"); err == nil {
		t.Fatal("expected an error when every judge fails")
	}
}
