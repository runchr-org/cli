// Package external provides an adapter that bridges external agent binaries
// (discovered via PATH as entire-agent-<name>) to the agent.Agent interface.
//
// This file implements CLI-side handling for declarative InfoResponse fields,
// allowing external agents to skip implementing subcommands for common patterns.
package external

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// Supported transcript format values for InfoResponse.TranscriptFormat.
const (
	TranscriptFormatJSONL = "jsonl"
	TranscriptFormatJSON  = "json"
)

// sessionDirVars holds template variables for session_dir_template expansion.
type sessionDirVars struct {
	RepoPath string // Absolute repo root path
	RepoHash string // SHA256 hex of RepoPath
	HomeDir  string // User home directory
}

// sessionFileVars holds template variables for session_file_template expansion.
type sessionFileVars struct {
	SessionDir string // Resolved session directory
	SessionID  string // Agent session ID
}

// resumeCommandVars holds template variables for resume_command_template expansion.
type resumeCommandVars struct {
	SessionID string // Session ID to resume
}

// expandSessionDirTemplate expands a session_dir_template with the given repo path.
func expandSessionDirTemplate(tmplStr, repoPath string) (string, error) {
	tmpl, err := template.New("session_dir").Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("invalid session_dir_template: %w", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}

	hash := sha256.Sum256([]byte(repoPath))
	vars := sessionDirVars{
		RepoPath: repoPath,
		RepoHash: hex.EncodeToString(hash[:]),
		HomeDir:  homeDir,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("expanding session_dir_template: %w", err)
	}
	return buf.String(), nil
}

// expandSessionFileTemplate expands a session_file_template with the given session dir and ID.
func expandSessionFileTemplate(tmplStr, sessionDir, sessionID string) (string, error) {
	tmpl, err := template.New("session_file").Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("invalid session_file_template: %w", err)
	}

	vars := sessionFileVars{
		SessionDir: sessionDir,
		SessionID:  sessionID,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("expanding session_file_template: %w", err)
	}
	return buf.String(), nil
}

// expandResumeCommandTemplate expands a resume_command_template with the given session ID.
func expandResumeCommandTemplate(tmplStr, sessionID string) (string, error) {
	tmpl, err := template.New("resume_command").Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("invalid resume_command_template: %w", err)
	}

	vars := resumeCommandVars{
		SessionID: sessionID,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("expanding resume_command_template: %w", err)
	}
	return buf.String(), nil
}

// detectByPaths checks if any of the given paths exist relative to repoRoot.
func detectByPaths(repoRoot string, paths []string) bool {
	for _, p := range paths {
		abs := filepath.Join(repoRoot, p)
		if _, err := os.Stat(abs); err == nil {
			return true
		}
	}
	return false
}

// chunkByFormat uses the built-in chunking logic for the declared transcript format.
// Returns nil, nil if the format is unknown (caller should fall back to subcommand).
func chunkByFormat(format string, content []byte, maxSize int) ([][]byte, error) {
	switch strings.ToLower(format) {
	case TranscriptFormatJSONL:
		chunks, err := agent.ChunkJSONL(content, maxSize)
		if err != nil {
			return nil, fmt.Errorf("chunk jsonl: %w", err)
		}
		return chunks, nil
	case TranscriptFormatJSON:
		return chunkJSON(content, maxSize)
	default:
		return nil, nil
	}
}

// reassembleByFormat uses the built-in reassembly logic for the declared transcript format.
// Returns nil, nil if the format is unknown (caller should fall back to subcommand).
func reassembleByFormat(format string, chunks [][]byte) ([]byte, error) {
	switch strings.ToLower(format) {
	case TranscriptFormatJSONL:
		return agent.ReassembleJSONL(chunks), nil
	case TranscriptFormatJSON:
		return reassembleJSON(chunks)
	default:
		return nil, nil
	}
}
