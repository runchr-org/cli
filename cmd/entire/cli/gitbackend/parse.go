package gitbackend

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
)

// parsePorcelainNul parses git status --porcelain -z output (NUL-separated).
// Returns lists of changed files and deleted files.
func parsePorcelainNul(data []byte) (changed, deleted []string, err error) {
	entries := bytes.Split(data, []byte{0})
	for _, entry := range entries {
		if len(entry) < 4 {
			continue
		}
		staging := entry[0]
		worktree := entry[1]
		path := string(entry[3:])
		if path == "" {
			continue
		}

		switch {
		case staging == 'D' || worktree == 'D':
			deleted = append(deleted, path)
		default:
			changed = append(changed, path)
		}
	}
	return changed, deleted, nil
}

// parseDiffTreeOutput parses NUL-separated git diff-tree -r -z output.
// Format: :<old-mode> <new-mode> <old-hash> <new-hash> <status>\0<path>\0
func parseDiffTreeOutput(data []byte) []string {
	if len(data) == 0 {
		return nil
	}

	parts := bytes.Split(data, []byte{0})
	var files []string
	i := 0
	for i < len(parts) {
		part := string(parts[i])
		switch {
		case strings.HasPrefix(part, ":"):
			status := extractDiffStatus(part)
			i++
			if i >= len(parts) {
				break
			}
			path := string(parts[i])
			if path != "" {
				files = append(files, path)
			}
			i++
			// Renames and copies have a second path
			if (status == 'R' || status == 'C') && i < len(parts) {
				path2 := string(parts[i])
				if path2 != "" && !strings.HasPrefix(path2, ":") {
					files = append(files, path2)
					i++
				}
			}
		default:
			i++
		}
	}
	return files
}

// extractDiffStatus extracts the status character from a diff-tree status line.
func extractDiffStatus(statusLine string) byte {
	trimmed := strings.TrimSpace(statusLine)
	if len(trimmed) == 0 {
		return 0
	}
	fields := strings.Fields(trimmed)
	if len(fields) < 5 {
		return 0
	}
	statusField := fields[4]
	if len(statusField) == 0 {
		return 0
	}
	return statusField[0]
}

// parseLsTree parses the output of git ls-tree into TreeEntry objects.
// Each line has format: "<mode> <type> <hash>\t<name>"
func parseLsTree(output string) ([]TreeEntry, error) { //nolint:unparam // error return kept for consistency with other parse functions
	var entries []TreeEntry
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		// Split on tab first to get name (may contain spaces)
		tabParts := strings.SplitN(line, "\t", 2)
		if len(tabParts) != 2 {
			continue
		}
		name := tabParts[1]
		fields := strings.Fields(tabParts[0])
		if len(fields) < 3 {
			continue
		}

		mode, err := filemode.New(fields[0])
		if err != nil {
			continue
		}

		entries = append(entries, TreeEntry{
			Name: name,
			Mode: mode,
			Hash: plumbing.NewHash(fields[2]),
		})
	}
	return entries, nil
}

// parseCatFileCommit parses the output of git cat-file -p for a commit object.
func parseCatFileCommit(hash Hash, output string) (*Commit, error) {
	c := &Commit{Hash: hash}
	lines := strings.SplitN(output, "\n\n", 2)
	if len(lines) == 2 {
		c.Message = lines[1]
	}

	for _, headerLine := range strings.Split(lines[0], "\n") {
		parts := strings.SplitN(headerLine, " ", 2)
		if len(parts) < 2 {
			continue
		}
		key, value := parts[0], parts[1]
		switch key {
		case "tree":
			c.TreeHash = plumbing.NewHash(value)
		case "parent":
			c.Parents = append(c.Parents, plumbing.NewHash(value))
		case "author":
			c.Author = parseSignature(value)
		case "committer":
			c.Committer = parseSignature(value)
		}
	}
	return c, nil
}

// parseLogEntry parses a single git log entry in the custom format used by LogEach.
// Format: %H\n%T\n%P\n%an\n%ae\n%aI\n%cn\n%ce\n%cI\n%B
func parseLogEntry(entry string) (*Commit, error) {
	lines := strings.SplitN(entry, "\n", 10)
	if len(lines) < 9 {
		return nil, fmt.Errorf("incomplete log entry: got %d lines", len(lines))
	}

	c := &Commit{
		Hash:     plumbing.NewHash(lines[0]),
		TreeHash: plumbing.NewHash(lines[1]),
	}

	// Parse parent hashes (space-separated)
	if parents := strings.TrimSpace(lines[2]); parents != "" {
		for _, p := range strings.Fields(parents) {
			c.Parents = append(c.Parents, plumbing.NewHash(p))
		}
	}

	authorTime, _ := time.Parse(time.RFC3339, strings.TrimSpace(lines[5])) //nolint:errcheck // best-effort parsing; zero time is acceptable fallback
	c.Author = Signature{
		Name:  lines[3],
		Email: lines[4],
		When:  authorTime,
	}

	committerTime, _ := time.Parse(time.RFC3339, strings.TrimSpace(lines[8])) //nolint:errcheck // best-effort parsing; zero time is acceptable fallback
	c.Committer = Signature{
		Name:  lines[6],
		Email: lines[7],
		When:  committerTime,
	}

	if len(lines) > 9 {
		c.Message = lines[9]
	}

	return c, nil
}

// parseSignature parses a git signature string like "Name <email> timestamp timezone"
func parseSignature(s string) Signature {
	sig := Signature{}

	// Find email between < and >
	ltIdx := strings.LastIndex(s, "<")
	gtIdx := strings.LastIndex(s, ">")
	if ltIdx >= 0 && gtIdx > ltIdx {
		sig.Name = strings.TrimSpace(s[:ltIdx])
		sig.Email = s[ltIdx+1 : gtIdx]

		// Parse timestamp after >
		rest := strings.TrimSpace(s[gtIdx+1:])
		parts := strings.Fields(rest)
		if len(parts) >= 1 {
			if ts, err := strconv.ParseInt(parts[0], 10, 64); err == nil {
				sig.When = time.Unix(ts, 0)
			}
		}
	}
	return sig
}
