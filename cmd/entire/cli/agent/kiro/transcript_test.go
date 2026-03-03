package kiro

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// testKiroTranscript is a realistic 4-entry Kiro transcript:
//
//	[0] Prompt: "Create a hello.go file" → Response: "I'll create..."
//	[1] Prompt: "Now add a test"         → ToolUse: fs_write /repo/hello.go
//	[2] ToolUseResults                   → ToolUse: fs_write /repo/hello_test.go
//	[3] ToolUseResults                   → Response: "Done! I created both files."
const testKiroTranscript = `{
  "conversation_id": "test-conv-123",
  "history": [
    {
      "user": {"content": {"Prompt": {"prompt": "Create a hello.go file"}}, "timestamp": "2026-01-01T00:00:00Z"},
      "assistant": {"Response": {"message_id": "msg-1", "content": "I'll create that file for you."}}
    },
    {
      "user": {"content": {"Prompt": {"prompt": "Now add a test"}}, "timestamp": "2026-01-01T00:01:00Z"},
      "assistant": {"ToolUse": {"message_id": "msg-2", "tool_uses": [
        {"id": "tu-1", "name": "fs_write", "args": {"path": "/repo/hello.go", "content": "package main"}}
      ]}}
    },
    {
      "user": {"content": {"ToolUseResults": {"tool_use_results": [{"id": "tu-1", "result": "ok"}]}}},
      "assistant": {"ToolUse": {"message_id": "msg-3", "tool_uses": [
        {"id": "tu-2", "name": "fs_write", "args": {"path": "/repo/hello_test.go", "content": "package main"}}
      ]}}
    },
    {
      "user": {"content": {"ToolUseResults": {"tool_use_results": [{"id": "tu-2", "result": "ok"}]}}},
      "assistant": {"Response": {"message_id": "msg-4", "content": "Done! I created both files."}}
    }
  ]
}`

// --- parseTranscript ---

func TestParseTranscript(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       []byte
		wantEntries int
		wantConvID  string
		wantErr     bool
	}{
		{
			name:        "valid transcript",
			input:       []byte(testKiroTranscript),
			wantEntries: 4,
			wantConvID:  "test-conv-123",
		},
		{
			name:        "empty history",
			input:       []byte(`{"conversation_id":"abc","history":[]}`),
			wantEntries: 0,
			wantConvID:  "abc",
		},
		{
			name:        "placeholder {}",
			input:       []byte(`{}`),
			wantEntries: 0,
			wantConvID:  "",
		},
		{
			name:        "empty bytes",
			input:       []byte{},
			wantEntries: 0,
			wantConvID:  "",
		},
		{
			name:    "invalid JSON",
			input:   []byte(`{not json`),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseTranscript(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.ConversationID != tc.wantConvID {
				t.Errorf("ConversationID = %q, want %q", got.ConversationID, tc.wantConvID)
			}
			if len(got.History) != tc.wantEntries {
				t.Errorf("len(History) = %d, want %d", len(got.History), tc.wantEntries)
			}
		})
	}
}

// --- extractUserPrompt ---

func TestExtractUserPrompt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "Prompt variant",
			raw:  `{"Prompt": {"prompt": "hello world"}}`,
			want: "hello world",
		},
		{
			name: "ToolUseResults variant",
			raw:  `{"ToolUseResults": {"tool_use_results": []}}`,
			want: "",
		},
		{
			name: "empty content",
			raw:  `{}`,
			want: "",
		},
		{
			name: "null content",
			raw:  "",
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := extractUserPrompt(json.RawMessage(tc.raw))
			if got != tc.want {
				t.Errorf("extractUserPrompt() = %q, want %q", got, tc.want)
			}
		})
	}
}

// --- extractModifiedFilesFromHistory ---

func TestExtractModifiedFilesFromHistory(t *testing.T) {
	t.Parallel()

	transcript, err := parseTranscript([]byte(testKiroTranscript))
	if err != nil {
		t.Fatalf("failed to parse test transcript: %v", err)
	}

	tests := []struct {
		name      string
		entries   []kiroHistoryEntry
		wantFiles []string
	}{
		{
			name:      "all entries - finds both fs_write files",
			entries:   transcript.History,
			wantFiles: []string{"/repo/hello.go", "/repo/hello_test.go"},
		},
		{
			name:      "from offset 2 - only second file",
			entries:   transcript.History[2:],
			wantFiles: []string{"/repo/hello_test.go"},
		},
		{
			name:      "first entry only - no tool use",
			entries:   transcript.History[:1],
			wantFiles: nil,
		},
		{
			name:      "empty entries",
			entries:   nil,
			wantFiles: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := extractModifiedFilesFromHistory(tc.entries)
			if len(got) != len(tc.wantFiles) {
				t.Fatalf("got %d files %v, want %d files %v", len(got), got, len(tc.wantFiles), tc.wantFiles)
			}
			for i, want := range tc.wantFiles {
				if got[i] != want {
					t.Errorf("files[%d] = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

func TestExtractModifiedFilesFromHistory_Dedup(t *testing.T) {
	t.Parallel()

	// Two tool calls writing to the same file should only appear once.
	transcript := `{
		"conversation_id": "dedup-test",
		"history": [
			{
				"user": {"content": {"Prompt": {"prompt": "write"}}},
				"assistant": {"ToolUse": {"message_id": "m1", "tool_uses": [
					{"id": "t1", "name": "fs_write", "args": {"path": "/repo/main.go"}}
				]}}
			},
			{
				"user": {"content": {"ToolUseResults": {}}},
				"assistant": {"ToolUse": {"message_id": "m2", "tool_uses": [
					{"id": "t2", "name": "fs_edit", "args": {"path": "/repo/main.go"}}
				]}}
			}
		]
	}`

	t2, err := parseTranscript([]byte(transcript))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	files := extractModifiedFilesFromHistory(t2.History)
	if len(files) != 1 || files[0] != "/repo/main.go" {
		t.Errorf("got %v, want [/repo/main.go]", files)
	}
}

func TestExtractModifiedFilesFromHistory_NonFileTool(t *testing.T) {
	t.Parallel()

	transcript := `{
		"conversation_id": "non-file",
		"history": [{
			"user": {"content": {"Prompt": {"prompt": "search"}}},
			"assistant": {"ToolUse": {"message_id": "m1", "tool_uses": [
				{"id": "t1", "name": "shell_exec", "args": {"command": "ls"}}
			]}}
		}]
	}`

	t2, err := parseTranscript([]byte(transcript))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	files := extractModifiedFilesFromHistory(t2.History)
	if len(files) != 0 {
		t.Errorf("got %v, want empty", files)
	}
}

// --- extractFilePath ---

func TestExtractFilePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args string
		want string
	}{
		{
			name: "path key",
			args: `{"path": "/repo/file.go", "content": "..."}`,
			want: "/repo/file.go",
		},
		{
			name: "file_path key",
			args: `{"file_path": "/repo/other.go"}`,
			want: "/repo/other.go",
		},
		{
			name: "filename key",
			args: `{"filename": "/repo/third.go"}`,
			want: "/repo/third.go",
		},
		{
			name: "path takes priority over file_path",
			args: `{"path": "/first", "file_path": "/second"}`,
			want: "/first",
		},
		{
			name: "empty args",
			args: `{}`,
			want: "",
		},
		{
			name: "null args",
			args: "",
			want: "",
		},
		{
			name: "no path keys",
			args: `{"content": "some text"}`,
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := extractFilePath(json.RawMessage(tc.args))
			if got != tc.want {
				t.Errorf("extractFilePath() = %q, want %q", got, tc.want)
			}
		})
	}
}

// --- extractLastAssistantResponse ---

func TestExtractLastAssistantResponse(t *testing.T) {
	t.Parallel()

	transcript, err := parseTranscript([]byte(testKiroTranscript))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	tests := []struct {
		name    string
		entries []kiroHistoryEntry
		want    string
	}{
		{
			name:    "full transcript - last Response",
			entries: transcript.History,
			want:    "Done! I created both files.",
		},
		{
			name:    "only first two entries - first Response",
			entries: transcript.History[:2],
			want:    "I'll create that file for you.",
		},
		{
			name:    "single ToolUse entry - no Response",
			entries: transcript.History[2:3],
			want:    "",
		},
		{
			name:    "empty entries",
			entries: nil,
			want:    "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := extractLastAssistantResponse(tc.entries)
			if got != tc.want {
				t.Errorf("extractLastAssistantResponse() = %q, want %q", got, tc.want)
			}
		})
	}
}

// --- GetTranscriptPosition ---

func TestGetTranscriptPosition(t *testing.T) {
	t.Parallel()

	ag := &KiroAgent{}

	t.Run("empty path", func(t *testing.T) {
		t.Parallel()
		pos, err := ag.GetTranscriptPosition("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pos != 0 {
			t.Errorf("got %d, want 0", pos)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()
		pos, err := ag.GetTranscriptPosition("/nonexistent/file.json")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pos != 0 {
			t.Errorf("got %d, want 0", pos)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		t.Parallel()
		path := writeTestFile(t, "")
		pos, err := ag.GetTranscriptPosition(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pos != 0 {
			t.Errorf("got %d, want 0", pos)
		}
	})

	t.Run("placeholder {}", func(t *testing.T) {
		t.Parallel()
		path := writeTestFile(t, "{}")
		pos, err := ag.GetTranscriptPosition(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pos != 0 {
			t.Errorf("got %d, want 0", pos)
		}
	})

	t.Run("normal transcript", func(t *testing.T) {
		t.Parallel()
		path := writeTestFile(t, testKiroTranscript)
		pos, err := ag.GetTranscriptPosition(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pos != 4 {
			t.Errorf("got %d, want 4", pos)
		}
	})
}

// --- ExtractModifiedFilesFromOffset ---

func TestExtractModifiedFilesFromOffset(t *testing.T) {
	t.Parallel()

	ag := &KiroAgent{}

	t.Run("offset 0 - all files", func(t *testing.T) {
		t.Parallel()
		path := writeTestFile(t, testKiroTranscript)
		files, pos, err := ag.ExtractModifiedFilesFromOffset(path, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pos != 4 {
			t.Errorf("position = %d, want 4", pos)
		}
		if len(files) != 2 {
			t.Fatalf("got %d files, want 2: %v", len(files), files)
		}
	})

	t.Run("offset 2 - only second file", func(t *testing.T) {
		t.Parallel()
		path := writeTestFile(t, testKiroTranscript)
		files, pos, err := ag.ExtractModifiedFilesFromOffset(path, 2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pos != 4 {
			t.Errorf("position = %d, want 4", pos)
		}
		if len(files) != 1 || files[0] != "/repo/hello_test.go" {
			t.Errorf("got %v, want [/repo/hello_test.go]", files)
		}
	})

	t.Run("offset >= len - no files", func(t *testing.T) {
		t.Parallel()
		path := writeTestFile(t, testKiroTranscript)
		files, pos, err := ag.ExtractModifiedFilesFromOffset(path, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pos != 4 {
			t.Errorf("position = %d, want 4", pos)
		}
		if len(files) != 0 {
			t.Errorf("got %v, want empty", files)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()
		files, pos, err := ag.ExtractModifiedFilesFromOffset("/nonexistent.json", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pos != 0 || len(files) != 0 {
			t.Errorf("expected zero pos and empty files, got pos=%d files=%v", pos, files)
		}
	})

	t.Run("empty path", func(t *testing.T) {
		t.Parallel()
		files, pos, err := ag.ExtractModifiedFilesFromOffset("", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pos != 0 || len(files) != 0 {
			t.Errorf("expected zero pos and empty files, got pos=%d files=%v", pos, files)
		}
	})
}

// --- ExtractPrompts ---

func TestExtractPrompts(t *testing.T) {
	t.Parallel()

	ag := &KiroAgent{}

	t.Run("all prompts from offset 0", func(t *testing.T) {
		t.Parallel()
		path := writeTestFile(t, testKiroTranscript)
		prompts, err := ag.ExtractPrompts(path, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Entries 0 and 1 have Prompt content; entries 2 and 3 have ToolUseResults.
		if len(prompts) != 2 {
			t.Fatalf("got %d prompts, want 2: %v", len(prompts), prompts)
		}
		if prompts[0] != "Create a hello.go file" {
			t.Errorf("prompts[0] = %q, want %q", prompts[0], "Create a hello.go file")
		}
		if prompts[1] != "Now add a test" {
			t.Errorf("prompts[1] = %q, want %q", prompts[1], "Now add a test")
		}
	})

	t.Run("with offset skips first prompt", func(t *testing.T) {
		t.Parallel()
		path := writeTestFile(t, testKiroTranscript)
		prompts, err := ag.ExtractPrompts(path, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(prompts) != 1 || prompts[0] != "Now add a test" {
			t.Errorf("got %v, want [Now add a test]", prompts)
		}
	})

	t.Run("offset beyond all prompts", func(t *testing.T) {
		t.Parallel()
		path := writeTestFile(t, testKiroTranscript)
		prompts, err := ag.ExtractPrompts(path, 2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Entries 2 and 3 are ToolUseResults, no prompts.
		if len(prompts) != 0 {
			t.Errorf("got %v, want empty", prompts)
		}
	})
}

// --- ExtractSummary ---

func TestExtractSummary(t *testing.T) {
	t.Parallel()

	ag := &KiroAgent{}

	t.Run("last Response from full transcript", func(t *testing.T) {
		t.Parallel()
		path := writeTestFile(t, testKiroTranscript)
		summary, err := ag.ExtractSummary(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if summary != "Done! I created both files." {
			t.Errorf("summary = %q, want %q", summary, "Done! I created both files.")
		}
	})

	t.Run("empty transcript", func(t *testing.T) {
		t.Parallel()
		path := writeTestFile(t, `{"conversation_id":"x","history":[]}`)
		summary, err := ag.ExtractSummary(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if summary != "" {
			t.Errorf("summary = %q, want empty", summary)
		}
	})

	t.Run("only ToolUse entries - no summary", func(t *testing.T) {
		t.Parallel()
		onlyToolUse := `{
			"conversation_id": "tu-only",
			"history": [{
				"user": {"content": {"Prompt": {"prompt": "write"}}},
				"assistant": {"ToolUse": {"message_id": "m1", "tool_uses": []}}
			}]
		}`
		path := writeTestFile(t, onlyToolUse)
		summary, err := ag.ExtractSummary(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if summary != "" {
			t.Errorf("summary = %q, want empty", summary)
		}
	})
}

// --- isFileModificationTool ---

func TestIsFileModificationTool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tool string
		want bool
	}{
		{"fs_write", "fs_write", true},
		{"fs_edit", "fs_edit", true},
		{"shell_exec", "shell_exec", false},
		{"fs_read", "fs_read", false},
		{"empty", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isFileModificationTool(tc.tool); got != tc.want {
				t.Errorf("isFileModificationTool(%q) = %v, want %v", tc.tool, got, tc.want)
			}
		})
	}
}

// writeTestFile is a helper that creates a temporary transcript file.
func writeTestFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	return path
}
