// Binary entire-agent-test implements the external agent protocol as a minimal
// reference example. It declares no optional capabilities, making it useful for
// testing the base external-agent integration path.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) < 2 {
		fatal("usage: entire-agent-test <subcommand> [args...]")
	}

	var err error
	switch os.Args[1] {
	case "info":
		err = cmdInfo()
	case "detect":
		err = cmdDetect()
	case "get-session-dir":
		err = runGetSessionDir()
	case "resolve-session-file":
		err = runResolveSessionFile()
	case "read-session":
		err = cmdReadSession()
	case "write-session":
		err = cmdWriteSession()
	case "format-resume-command":
		err = runFormatResumeCommand()
	case "read-transcript":
		err = runReadTranscript()
	case "chunk-transcript":
		err = runChunkTranscript()
	case "reassemble-transcript":
		err = cmdReassembleTranscript()
	default:
		fatal("unknown subcommand: " + os.Args[1])
	}

	if err != nil {
		fatal(err.Error())
	}
}

// ---------------------------------------------------------------------------
// Subcommand implementations
// ---------------------------------------------------------------------------

func cmdInfo() error {
	return writeJSON(map[string]interface{}{
		"protocol_version": 1,
		"name":             "test-agent",
		"type":             "Test Agent",
		"description":      "Minimal reference implementation of the external agent protocol",
		"is_preview":       true,
		"protected_dirs":   []string{".test-agent"},
		"hook_names":       []string{},
		"capabilities": map[string]bool{
			"hooks":                    false,
			"transcript_analyzer":      false,
			"transcript_preparer":      false,
			"token_calculator":         false,
			"text_generator":           false,
			"hook_response_writer":     false,
			"subagent_aware_extractor": false,
		},
	})
}

func cmdDetect() error {
	return writeJSON(map[string]interface{}{
		"present": true,
	})
}

func runGetSessionDir() error {
	fs := flag.NewFlagSet("get-session-dir", flag.ContinueOnError)
	repoPath := fs.String("repo-path", "", "repository path")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return fmt.Errorf("failed to parse flags: %w", err)
	}
	return writeJSON(map[string]interface{}{
		"session_dir": filepath.Join(*repoPath, ".test-agent", "sessions"),
	})
}

func runResolveSessionFile() error {
	fs := flag.NewFlagSet("resolve-session-file", flag.ContinueOnError)
	sessionDir := fs.String("session-dir", "", "session directory")
	sessionID := fs.String("session-id", "", "session ID")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return fmt.Errorf("failed to parse flags: %w", err)
	}
	return writeJSON(map[string]interface{}{
		"session_file": filepath.Join(*sessionDir, *sessionID+".jsonl"),
	})
}

func cmdReadSession() error {
	// Read and discard stdin (HookInput JSON).
	if _, err := io.ReadAll(os.Stdin); err != nil {
		return fmt.Errorf("failed to read stdin: %w", err)
	}
	// Return a minimal AgentSession stub.
	return writeJSON(map[string]interface{}{
		"session_id":     "",
		"agent_name":     "test-agent",
		"repo_path":      "",
		"session_ref":    "",
		"start_time":     "",
		"native_data":    nil,
		"modified_files": []string{},
		"new_files":      []string{},
		"deleted_files":  []string{},
	})
}

func cmdWriteSession() error {
	// Read and discard stdin (AgentSession JSON). No-op.
	if _, err := io.ReadAll(os.Stdin); err != nil {
		return fmt.Errorf("failed to read stdin: %w", err)
	}
	return nil
}

func runFormatResumeCommand() error {
	fs := flag.NewFlagSet("format-resume-command", flag.ContinueOnError)
	sessionID := fs.String("session-id", "", "session ID")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return fmt.Errorf("failed to parse flags: %w", err)
	}
	return writeJSON(map[string]interface{}{
		"command": "test-agent resume " + *sessionID,
	})
}

func runReadTranscript() error {
	fs := flag.NewFlagSet("read-transcript", flag.ContinueOnError)
	sessionRef := fs.String("session-ref", "", "session reference path")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return fmt.Errorf("failed to parse flags: %w", err)
	}
	data, err := os.ReadFile(*sessionRef)
	if err != nil {
		return fmt.Errorf("failed to read transcript: %w", err)
	}
	if _, err := os.Stdout.Write(data); err != nil {
		return fmt.Errorf("failed to write output: %w", err)
	}
	return nil
}

func runChunkTranscript() error {
	fs := flag.NewFlagSet("chunk-transcript", flag.ContinueOnError)
	maxSize := fs.Int("max-size", 0, "maximum chunk size in bytes")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return fmt.Errorf("failed to parse flags: %w", err)
	}

	content, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("failed to read stdin: %w", err)
	}

	chunkSize := *maxSize
	if chunkSize <= 0 {
		chunkSize = len(content)
	}

	// json.Marshal automatically base64-encodes []byte elements.
	var chunks [][]byte
	for i := 0; i < len(content); i += chunkSize {
		end := i + chunkSize
		if end > len(content) {
			end = len(content)
		}
		chunks = append(chunks, content[i:end])
	}

	return writeJSON(map[string]interface{}{
		"chunks": chunks,
	})
}

func cmdReassembleTranscript() error {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("failed to read stdin: %w", err)
	}

	var resp struct {
		Chunks [][]byte `json:"chunks"`
	}
	if err := json.Unmarshal(input, &resp); err != nil {
		return fmt.Errorf("failed to unmarshal chunks: %w", err)
	}

	// json.Unmarshal automatically base64-decodes []byte elements.
	for _, chunk := range resp.Chunks {
		if _, err := os.Stdout.Write(chunk); err != nil {
			return fmt.Errorf("failed to write output: %w", err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func writeJSON(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}
	if _, err := os.Stdout.Write(data); err != nil {
		return fmt.Errorf("failed to write output: %w", err)
	}
	return nil
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
