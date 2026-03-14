package external

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

func TestDeclarative_DetectPaths(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	// Create a temp dir with a marker file
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, ".myagent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".myagent", "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("detect_paths present", func(t *testing.T) {
		t.Parallel()
		if detectByPaths(tmpDir, []string{".myagent/config.json"}) != true {
			t.Error("detectByPaths should return true when file exists")
		}
	})

	t.Run("detect_paths absent", func(t *testing.T) {
		t.Parallel()
		if detectByPaths(tmpDir, []string{".other/config.json"}) != false {
			t.Error("detectByPaths should return false when file doesn't exist")
		}
	})

	t.Run("detect_paths multiple, one present", func(t *testing.T) {
		t.Parallel()
		if detectByPaths(tmpDir, []string{".other/config.json", ".myagent/config.json"}) != true {
			t.Error("detectByPaths should return true when any file exists")
		}
	})
}

func TestDeclarative_ExpandSessionDirTemplate(t *testing.T) {
	t.Parallel()

	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory")
	}

	tests := []struct {
		name     string
		template string
		repoPath string
		wantErr  bool
		check    func(t *testing.T, result string)
	}{
		{
			name:     "simple template with HomeDir",
			template: "{{.HomeDir}}/.myagent/sessions",
			repoPath: "/home/user/myrepo",
			check: func(t *testing.T, result string) {
				t.Helper()
				want := homeDir + "/.myagent/sessions"
				if result != want {
					t.Errorf("got %q, want %q", result, want)
				}
			},
		},
		{
			name:     "template with RepoHash",
			template: "{{.HomeDir}}/.myagent/{{.RepoHash}}",
			repoPath: "/home/user/myrepo",
			check: func(t *testing.T, result string) {
				t.Helper()
				// Should contain a 64-char hex hash
				parts := filepath.SplitList(result)
				_ = parts
				if len(result) < 64 {
					t.Error("result should contain a SHA256 hash")
				}
			},
		},
		{
			name:     "template with RepoPath",
			template: "{{.RepoPath}}/.myagent/sessions",
			repoPath: "/home/user/myrepo",
			check: func(t *testing.T, result string) {
				t.Helper()
				want := "/home/user/myrepo/.myagent/sessions"
				if result != want {
					t.Errorf("got %q, want %q", result, want)
				}
			},
		},
		{
			name:     "invalid template",
			template: "{{.Invalid",
			repoPath: "/repo",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := expandSessionDirTemplate(tt.template, tt.repoPath)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, result)
			}
		})
	}
}

func TestDeclarative_ExpandSessionFileTemplate(t *testing.T) {
	t.Parallel()

	result, err := expandSessionFileTemplate(
		"{{.SessionDir}}/{{.SessionID}}.jsonl",
		"/home/user/.myagent/sessions",
		"session-123",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "/home/user/.myagent/sessions/session-123.jsonl"
	if result != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

func TestDeclarative_ExpandResumeCommandTemplate(t *testing.T) {
	t.Parallel()

	result, err := expandResumeCommandTemplate(
		"myagent --resume {{.SessionID}}",
		"session-456",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "myagent --resume session-456"
	if result != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

func TestDeclarative_ChunkByFormat(t *testing.T) {
	t.Parallel()

	t.Run("jsonl", func(t *testing.T) {
		t.Parallel()
		content := []byte(`{"line":1}
{"line":2}
{"line":3}`)
		// Use a small maxSize to force chunking
		chunks, err := chunkByFormat("jsonl", content, 20)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(chunks) < 2 {
			t.Errorf("expected multiple chunks, got %d", len(chunks))
		}
	})

	t.Run("json", func(t *testing.T) {
		t.Parallel()
		content := []byte(`{"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"}]}`)
		// Use a small maxSize to force chunking
		chunks, err := chunkByFormat("json", content, 60)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(chunks) < 2 {
			t.Errorf("expected multiple chunks, got %d", len(chunks))
		}
	})

	t.Run("unknown format returns nil", func(t *testing.T) {
		t.Parallel()
		chunks, err := chunkByFormat("xml", []byte("<data/>"), 100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if chunks != nil {
			t.Error("expected nil for unknown format")
		}
	})
}

func TestDeclarative_ReassembleByFormat(t *testing.T) {
	t.Parallel()

	t.Run("jsonl", func(t *testing.T) {
		t.Parallel()
		chunks := [][]byte{
			[]byte(`{"line":1}`),
			[]byte(`{"line":2}`),
		}
		result, err := reassembleByFormat("jsonl", chunks)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := `{"line":1}
{"line":2}`
		if string(result) != want {
			t.Errorf("got %q, want %q", string(result), want)
		}
	})

	t.Run("json", func(t *testing.T) {
		t.Parallel()
		chunks := [][]byte{
			[]byte(`{"messages":[{"role":"user"}]}`),
			[]byte(`{"messages":[{"role":"assistant"}]}`),
		}
		result, err := reassembleByFormat("json", chunks)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Should contain both messages
		if len(result) == 0 {
			t.Error("expected non-empty result")
		}
	})

	t.Run("unknown format returns nil", func(t *testing.T) {
		t.Parallel()
		result, err := reassembleByFormat("xml", [][]byte{[]byte("<data/>")})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != nil {
			t.Error("expected nil for unknown format")
		}
	})
}

// declarativeInfoJSON is a minimal info response using declarative fields.
const declarativeInfoJSON = `{
  "protocol_version": 1,
  "name": "declarative-test",
  "type": "Declarative Test",
  "description": "A test agent using declarative config",
  "is_preview": false,
  "protected_dirs": [".myagent"],
  "hook_names": ["session-start", "stop"],
  "capabilities": {"hooks": true},
  "detect_paths": [".myagent/config.json"],
  "transcript_format": "jsonl",
  "session_dir_template": "{{.HomeDir}}/.myagent/sessions/{{.RepoHash}}",
  "session_file_template": "{{.SessionDir}}/{{.SessionID}}.jsonl",
  "resume_command_template": "myagent --resume {{.SessionID}}"
}`

// mockDeclarativeScript returns a script that only implements info + parse-hook.
// All other subcommands are absent — the CLI handles them via declarative fields.
func mockDeclarativeScript(infoJSON string) string {
	return `#!/bin/sh
case "$1" in
  info)
    echo '` + infoJSON + `'
    ;;
  parse-hook)
    echo 'null'
    ;;
  *)
    echo "subcommand not implemented: $1" >&2
    exit 1
    ;;
esac
`
}

func TestDeclarativeAgent_GetSessionDir(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	binPath := testBinaryDir(t, mockDeclarativeScript(declarativeInfoJSON))
	ea := newExternalAgent(t, binPath)

	dir, err := ea.GetSessionDir("/home/user/myrepo")
	if err != nil {
		t.Fatalf("GetSessionDir: %v", err)
	}

	// Should be expanded from template, not from subcommand
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory")
	}
	if dir == "" {
		t.Error("GetSessionDir returned empty string")
	}
	if len(dir) < len(homeDir) {
		t.Errorf("GetSessionDir result %q seems too short", dir)
	}
}

func TestDeclarativeAgent_ResolveSessionFile(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	binPath := testBinaryDir(t, mockDeclarativeScript(declarativeInfoJSON))
	ea := newExternalAgent(t, binPath)

	file := ea.ResolveSessionFile("/tmp/sessions", "session-abc")
	want := "/tmp/sessions/session-abc.jsonl"
	if file != want {
		t.Errorf("ResolveSessionFile = %q, want %q", file, want)
	}
}

func TestDeclarativeAgent_FormatResumeCommand(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	binPath := testBinaryDir(t, mockDeclarativeScript(declarativeInfoJSON))
	ea := newExternalAgent(t, binPath)

	cmd := ea.FormatResumeCommand("session-xyz")
	want := "myagent --resume session-xyz"
	if cmd != want {
		t.Errorf("FormatResumeCommand = %q, want %q", cmd, want)
	}
}

func TestDeclarativeAgent_ChunkTranscript(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	binPath := testBinaryDir(t, mockDeclarativeScript(declarativeInfoJSON))
	ea := newExternalAgent(t, binPath)

	content := []byte(`{"line":1}
{"line":2}`)
	// Single chunk (under max size)
	chunks, err := ea.ChunkTranscript(context.Background(), content, 1024*1024)
	if err != nil {
		t.Fatalf("ChunkTranscript: %v", err)
	}
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk, got %d", len(chunks))
	}
}

func TestDeclarativeAgent_ReassembleTranscript(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	binPath := testBinaryDir(t, mockDeclarativeScript(declarativeInfoJSON))
	ea := newExternalAgent(t, binPath)

	chunks := [][]byte{
		[]byte(`{"line":1}`),
		[]byte(`{"line":2}`),
	}
	result, err := ea.ReassembleTranscript(chunks)
	if err != nil {
		t.Fatalf("ReassembleTranscript: %v", err)
	}
	want := "{\"line\":1}\n{\"line\":2}"
	if string(result) != want {
		t.Errorf("ReassembleTranscript = %q, want %q", string(result), want)
	}
}

func TestDeclarativeAgent_ReadTranscript(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	binPath := testBinaryDir(t, mockDeclarativeScript(declarativeInfoJSON))
	ea := newExternalAgent(t, binPath)

	// Create a temp transcript file
	tmpFile := filepath.Join(t.TempDir(), "transcript.jsonl")
	transcriptContent := `{"role":"user","content":"hello"}
{"role":"assistant","content":"hi"}`
	if err := os.WriteFile(tmpFile, []byte(transcriptContent), 0o644); err != nil {
		t.Fatal(err)
	}

	data, err := ea.ReadTranscript(tmpFile)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	if string(data) != transcriptContent {
		t.Errorf("ReadTranscript = %q, want %q", string(data), transcriptContent)
	}
}

func TestDeclarativeAgent_GetSessionID(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	binPath := testBinaryDir(t, mockDeclarativeScript(declarativeInfoJSON))
	ea := newExternalAgent(t, binPath)

	input := &agent.HookInput{
		SessionID:  "test-session-42",
		SessionRef: "/tmp/transcript.jsonl",
		Timestamp:  time.Now(),
	}
	sid := ea.GetSessionID(input)
	if sid != "test-session-42" {
		t.Errorf("GetSessionID = %q, want %q", sid, "test-session-42")
	}
}

func TestDeclarativeAgent_WriteSession_Noop(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	binPath := testBinaryDir(t, mockDeclarativeScript(declarativeInfoJSON))
	ea := newExternalAgent(t, binPath)

	// WriteSession should be a no-op (not call subcommand which would fail)
	err := ea.WriteSession(context.Background(), &agent.AgentSession{
		SessionID: "test",
		AgentName: "declarative-test",
	})
	if err != nil {
		t.Fatalf("WriteSession should be no-op for declarative agents, got: %v", err)
	}
}

func TestDeclarativeAgent_HasDeclarativeTemplates(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	t.Run("with templates", func(t *testing.T) {
		t.Parallel()
		binPath := testBinaryDir(t, mockDeclarativeScript(declarativeInfoJSON))
		ea := newExternalAgent(t, binPath)
		if !ea.hasDeclarativeTemplates() {
			t.Error("hasDeclarativeTemplates should return true")
		}
	})

	t.Run("without templates", func(t *testing.T) {
		t.Parallel()
		binPath := testBinaryDir(t, mockInfoScript(validInfoJSON))
		ea := newExternalAgent(t, binPath)
		if ea.hasDeclarativeTemplates() {
			t.Error("hasDeclarativeTemplates should return false for non-declarative agent")
		}
	})
}

func TestDeclarativeAgent_JSON_ChunkAndReassemble(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	jsonInfoJSON := `{
  "protocol_version": 1,
  "name": "json-agent",
  "type": "JSON Agent",
  "description": "Agent with JSON transcripts",
  "is_preview": false,
  "protected_dirs": [],
  "hook_names": ["stop"],
  "capabilities": {"hooks": true},
  "transcript_format": "json"
}`

	binPath := testBinaryDir(t, mockDeclarativeScript(jsonInfoJSON))
	ea := newExternalAgent(t, binPath)

	content := []byte(`{"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"world"}]}`)

	// Chunk with large maxSize → single chunk
	chunks, err := ea.ChunkTranscript(context.Background(), content, 1024*1024)
	if err != nil {
		t.Fatalf("ChunkTranscript: %v", err)
	}
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk, got %d", len(chunks))
	}

	// Reassemble
	result, err := ea.ReassembleTranscript(chunks)
	if err != nil {
		t.Fatalf("ReassembleTranscript: %v", err)
	}
	if len(result) == 0 {
		t.Error("ReassembleTranscript returned empty result")
	}
}
