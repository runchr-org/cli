package pi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// Compile-time interface assertions
var (
	_ agent.TokenCalculator    = (*PiAgent)(nil)
	_ agent.TranscriptAnalyzer = (*PiAgent)(nil)
	_ agent.PromptExtractor    = (*PiAgent)(nil)
)

// Pi JSONL entry types
const (
	entryTypeMessage = "message"
	roleAssistant    = "assistant"
	roleUser         = "user"
	roleToolResult   = "toolResult"
	contentTypeText  = "text"
)

// maxScannerLine is 1 MB — large enough for Pi JSONL lines that may contain
// thinking blocks or full file contents in tool call arguments.
const maxScannerLine = 1 << 20

func newJSONLScanner(data []byte) *bufio.Scanner {
	s := bufio.NewScanner(bytes.NewReader(data))
	s.Buffer(make([]byte, 0, maxScannerLine), maxScannerLine)
	return s
}

// countLines returns the number of non-empty lines in data.
func countLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	n := bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		n++
	}
	return n
}

// skipLines returns data with the first n lines removed. Returns nil if data
// has fewer than n lines.
func skipLines(data []byte, n int) []byte {
	if n <= 0 {
		return data
	}
	off := 0
	for i := 0; i < n && off < len(data); i++ {
		idx := bytes.IndexByte(data[off:], '\n')
		if idx < 0 {
			return nil
		}
		off += idx + 1
	}
	return data[off:]
}

// messageEntry is the outer shell of a Pi "message" JSONL line.
type messageEntry struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	Timestamp string  `json:"timestamp"`
	Message   message `json:"message"`
}

// message is the inner Pi message object.
type message struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Timestamp  json.Number     `json:"timestamp"`
	Usage      *piTokenUsage   `json:"usage,omitempty"`
	StopReason string          `json:"stopReason,omitempty"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	ToolName   string          `json:"toolName,omitempty"`
	IsError    bool            `json:"isError,omitempty"`
}

// piTokenUsage mirrors pi-ai's Usage struct (token-count fields only).
type piTokenUsage struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	CacheRead  int `json:"cacheRead"`
	CacheWrite int `json:"cacheWrite"`
}

// contentItem is one entry in a Pi message's content array.
type contentItem struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Name      string          `json:"name,omitempty"`
	ID        string          `json:"id,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// resolveActiveBranch walks the Pi transcript tree and returns the set of
// entry IDs on the active conversation branch (the path from the root to the
// last message entry).
//
// Pi transcripts form a tree: every entry has an `id` and `parentId`. When
// the user forks/branches mid-conversation, the JSONL file accumulates
// entries from BOTH branches. Without filtering, downstream analysis would
// double-count tokens, files, and prompts.
//
// Returns nil when the transcript has no tree structure (every entry has
// no parent or all entries are linear) — callers should treat nil as "all
// entries are on the active branch".
func resolveActiveBranch(data []byte) map[string]bool {
	type node struct {
		Type     string  `json:"type"`
		ID       string  `json:"id"`
		ParentID *string `json:"parentId"`
	}

	var lastMessageID string
	hasTree := false
	parentOf := make(map[string]string)

	scanner := newJSONLScanner(data)
	for scanner.Scan() {
		var n node
		if err := json.Unmarshal(scanner.Bytes(), &n); err != nil || n.ID == "" {
			continue
		}
		if n.ParentID != nil {
			parentOf[n.ID] = *n.ParentID
			if *n.ParentID != "" {
				hasTree = true
			}
		}
		if n.Type == entryTypeMessage {
			lastMessageID = n.ID
		}
	}

	if !hasTree || lastMessageID == "" {
		return nil
	}

	active := make(map[string]bool)
	for cur := lastMessageID; cur != ""; {
		if active[cur] {
			break // cycle protection
		}
		active[cur] = true
		parent, ok := parentOf[cur]
		if !ok {
			break
		}
		cur = parent
	}
	return active
}

// CalculateTokenUsage sums per-assistant-message token usage from a Pi JSONL
// transcript starting at the given line offset. Only assistant messages on
// the active conversation branch contribute to the totals — see
// resolveActiveBranch for the rationale.
func (a *PiAgent) CalculateTokenUsage(transcriptData []byte, fromOffset int) (*agent.TokenUsage, error) {
	usage := &agent.TokenUsage{}
	if len(transcriptData) == 0 {
		return usage, nil
	}

	active := resolveActiveBranch(transcriptData)
	content := skipLines(transcriptData, fromOffset)

	scanner := newJSONLScanner(content)
	for scanner.Scan() {
		var entry messageEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry.Type != entryTypeMessage || entry.Message.Role != roleAssistant || entry.Message.Usage == nil {
			continue
		}
		if active != nil && !active[entry.ID] {
			continue
		}
		usage.InputTokens += entry.Message.Usage.Input
		usage.OutputTokens += entry.Message.Usage.Output
		usage.CacheReadTokens += entry.Message.Usage.CacheRead
		usage.CacheCreationTokens += entry.Message.Usage.CacheWrite
		usage.APICallCount++
	}
	if err := scanner.Err(); err != nil {
		return usage, fmt.Errorf("pi transcript scanner: %w", err)
	}
	return usage, nil
}

// GetTranscriptPosition returns the number of JSONL lines in the file at path.
// Used by the strategy as the offset for incremental ExtractModifiedFiles
// calls. Missing files report 0 (consistent with Claude Code).
func (a *PiAgent) GetTranscriptPosition(path string) (int, error) {
	if path == "" {
		return 0, nil
	}
	//nolint:gosec // path from validated SessionRef set by lifecycle hooks
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read pi transcript: %w", err)
	}
	return countLines(data), nil
}

// ExtractModifiedFilesFromOffset scans Pi assistant tool calls from startOffset
// onward and returns file paths touched by file-modifying tools (`write`,
// `edit`). Branch-aware: only counts entries on the active conversation
// branch.
//
// Returns the current line count alongside the file list so callers can
// advance their offset for the next call.
func (a *PiAgent) ExtractModifiedFilesFromOffset(path string, startOffset int) ([]string, int, error) {
	if path == "" {
		return nil, 0, nil
	}
	//nolint:gosec // path from validated SessionRef
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, fmt.Errorf("read pi transcript: %w", err)
	}

	totalLines := countLines(data)
	active := resolveActiveBranch(data)
	content := skipLines(data, startOffset)

	seen := make(map[string]bool)
	var files []string

	scanner := newJSONLScanner(content)
	for scanner.Scan() {
		var entry messageEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry.Type != entryTypeMessage || entry.Message.Role != roleAssistant {
			continue
		}
		if active != nil && !active[entry.ID] {
			continue
		}
		var items []contentItem
		if err := json.Unmarshal(entry.Message.Content, &items); err != nil {
			continue
		}
		for _, item := range items {
			if item.Type != "toolCall" {
				continue
			}
			if item.Name != "write" && item.Name != "edit" {
				continue
			}
			var args struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(item.Arguments, &args); err != nil {
				continue
			}
			if args.Path != "" && !seen[args.Path] {
				seen[args.Path] = true
				files = append(files, args.Path)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return files, totalLines, fmt.Errorf("pi transcript scanner: %w", err)
	}
	return files, totalLines, nil
}

// ExtractPrompts returns user-message text from the transcript starting at
// the given line offset. Branch-aware (drops abandoned-branch prompts).
//
// Used as a fallback when prompt data isn't captured via hooks.
func (a *PiAgent) ExtractPrompts(sessionRef string, fromOffset int) ([]string, error) {
	if sessionRef == "" {
		return nil, nil
	}
	//nolint:gosec // sessionRef from validated SessionRef
	data, err := os.ReadFile(sessionRef)
	if err != nil {
		return nil, fmt.Errorf("read pi transcript: %w", err)
	}

	active := resolveActiveBranch(data)
	content := skipLines(data, fromOffset)

	var prompts []string
	scanner := newJSONLScanner(content)
	for scanner.Scan() {
		var entry messageEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if entry.Type != entryTypeMessage || entry.Message.Role != roleUser {
			continue
		}
		if active != nil && !active[entry.ID] {
			continue
		}
		// User content can be either a plain string or an array of typed blocks.
		if text := decodeStringContent(entry.Message.Content); text != "" {
			prompts = append(prompts, text)
			continue
		}
		var items []contentItem
		if err := json.Unmarshal(entry.Message.Content, &items); err != nil {
			continue
		}
		for _, item := range items {
			if item.Type == contentTypeText && item.Text != "" {
				prompts = append(prompts, item.Text)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return prompts, fmt.Errorf("pi transcript scanner: %w", err)
	}
	return prompts, nil
}

// decodeStringContent returns the raw string when content is a plain string,
// or empty when it's a JSON array.
func decodeStringContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}
