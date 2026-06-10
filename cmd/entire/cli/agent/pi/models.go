package pi

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

var _ agent.ModelLister = (*PiAgent)(nil)

// ListModels returns the models Pi can run, fetched live from `pi
// --list-models`. Pi is the only supported agent whose CLI can enumerate
// models, so this is a real list rather than a curated/example one. The model
// picker always offers Default and Custom on top of this, and falls back to
// Default + Custom when the call fails (returned error).
func (a *PiAgent) ListModels(ctx context.Context) ([]agent.ModelInfo, error) {
	bin, err := exec.LookPath("pi")
	if err != nil {
		return nil, fmt.Errorf("pi not found on PATH: %w", err)
	}
	// `pi --list-models` prints the table to stderr, so capture combined output.
	out, err := exec.CommandContext(ctx, bin, "--list-models").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("pi --list-models: %w", err)
	}
	return parsePiModelList(string(out)), nil
}

// parsePiModelList parses the tabular `pi --list-models` output into ModelInfo
// values. The output is a whitespace-aligned table:
//
//	provider   model                context  max-out  thinking  images
//	anthropic  claude-opus-4-5      200K     64K      yes       yes
//
// The first two columns (provider, model) become the "provider/model" id Pi
// accepts via --model; the header row and malformed lines are skipped.
func parsePiModelList(output string) []agent.ModelInfo {
	var models []agent.ModelInfo
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		provider, model := fields[0], fields[1]
		if provider == "provider" && model == "model" {
			continue // header row
		}
		models = append(models, agent.ModelInfo{ID: provider + "/" + model})
	}
	return models
}
