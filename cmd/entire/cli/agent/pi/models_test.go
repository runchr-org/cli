package pi

import (
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// TestPiAgent_IsModelLister locks Pi in as the one agent with live enumeration.
func TestPiAgent_IsModelLister(t *testing.T) {
	t.Parallel()
	if _, ok := agent.AsModelLister(NewPiAgent()); !ok {
		t.Fatal("PiAgent should implement agent.ModelLister")
	}
}

func TestParsePiModelList(t *testing.T) {
	t.Parallel()
	output := `provider      model                         context  max-out  thinking  images
anthropic     claude-opus-4-5               200K     64K      yes       yes
anthropic     claude-sonnet-4-5             200K     64K      yes       yes
openai        gpt-5                         400K     128K     yes       yes
`
	models := parsePiModelList(output)
	want := []string{
		"anthropic/claude-opus-4-5",
		"anthropic/claude-sonnet-4-5",
		"openai/gpt-5",
	}
	if len(models) != len(want) {
		t.Fatalf("got %d models, want %d: %+v", len(models), len(want), models)
	}
	for i, w := range want {
		if models[i].ID != w {
			t.Errorf("models[%d].ID = %q, want %q", i, models[i].ID, w)
		}
	}
}

func TestParsePiModelList_SkipsHeaderAndBlanks(t *testing.T) {
	t.Parallel()
	if got := parsePiModelList("provider model context\n\n   \n"); len(got) != 0 {
		t.Fatalf("expected no models from header/blank-only output, got %+v", got)
	}
	if got := parsePiModelList(""); got != nil {
		t.Fatalf("expected nil for empty output, got %+v", got)
	}
}
