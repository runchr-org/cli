package importclaude

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent/claudecode"
)

// LookbackDays bounds how far back import reaches. Fixed this pass (no --since).
const LookbackDays = 30

// DiscoverSessions returns absolute paths of Claude transcript files for the
// repo modified within the lookback window. overridePath replaces the default
// ~/.claude/projects/<slug> dir. sessionFilter, when non-empty, keeps only
// files whose stem (name without .jsonl) matches one of the entries.
func DiscoverSessions(repoRoot, overridePath string, now time.Time, sessionFilter []string) ([]string, error) {
	dir := overridePath
	if dir == "" {
		ag := &claudecode.ClaudeCodeAgent{}
		d, err := ag.GetSessionDir(repoRoot)
		if err != nil {
			return nil, fmt.Errorf("resolve claude session dir: %w", err)
		}
		dir = d
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no transcripts for this repo
		}
		return nil, fmt.Errorf("read claude session dir: %w", err)
	}
	cutoff := now.AddDate(0, 0, -LookbackDays)
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		stem := strings.TrimSuffix(e.Name(), ".jsonl")
		if len(sessionFilter) > 0 && !slices.Contains(sessionFilter, stem) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	slices.Sort(out)
	return out, nil
}
