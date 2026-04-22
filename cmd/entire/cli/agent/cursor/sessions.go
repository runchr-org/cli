package cursor

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// SessionInfo describes one discovered Cursor agent session on disk.
type SessionInfo struct {
	AgentID         string `json:"agent_id"`
	DBPath          string `json:"db_path"`
	DBSize          int64  `json:"db_size_bytes"`
	TranscriptPath  string `json:"transcript_path,omitempty"`
	TranscriptLines int    `json:"transcript_lines"`
	WorkspaceHash   string `json:"workspace_hash"`
}

// ListSessions enumerates every Cursor agent session stored under ~/.cursor/chats.
// Matches transcripts from ~/.cursor/projects/*/agent-transcripts/<agentID>.jsonl when present.
// Returns an empty slice (not nil) if no sessions exist.
func ListSessions() ([]SessionInfo, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home directory: %w", err)
	}
	chatsDir := filepath.Join(homeDir, ".cursor", "chats")
	projectsDir := filepath.Join(homeDir, ".cursor", "projects")

	dbs, err := filepath.Glob(filepath.Join(chatsDir, "*", "*", "store.db"))
	if err != nil {
		return nil, fmt.Errorf("glob chats: %w", err)
	}

	out := make([]SessionInfo, 0, len(dbs))
	for _, dbPath := range dbs {
		info, err := os.Stat(dbPath)
		if err != nil {
			continue
		}
		agentID := filepath.Base(filepath.Dir(dbPath))
		workspaceHash := filepath.Base(filepath.Dir(filepath.Dir(dbPath)))

		si := SessionInfo{
			AgentID:       agentID,
			DBPath:        dbPath,
			DBSize:        info.Size(),
			WorkspaceHash: workspaceHash,
		}

		matches, _ := filepath.Glob(filepath.Join(projectsDir, "*", "agent-transcripts", agentID+".jsonl")) //nolint:errcheck // best-effort
		if len(matches) > 0 {
			si.TranscriptPath = matches[0]
			si.TranscriptLines = countLines(matches[0])
		}
		out = append(out, si)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	return out, nil
}

func countLines(path string) int {
	f, err := os.Open(path) //nolint:gosec // path comes from glob of known dirs, not user input
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		n++
	}
	return n
}
