package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWrapProductionJSONWarningHookCommand(t *testing.T) {
	t.Parallel()

	command := WrapProductionJSONWarningHookCommand("entire hooks claude-code session-start", WarningFormatMultiLine)

	if command == "entire hooks claude-code session-start" {
		t.Fatal("expected wrapped command, got raw command")
	}
	if strings.Contains(command, `>&2`) {
		t.Fatalf("claude wrapper should not print warning to stderr, got %q", command)
	}
	if want := `systemMessage`; !strings.Contains(command, want) {
		t.Fatalf("claude wrapper missing systemMessage JSON, got %q", command)
	}
	if !strings.Contains(command, "Entire CLI") {
		t.Fatalf("claude wrapper missing warning text, got %q", command)
	}
	if want := "exec entire hooks claude-code session-start"; !strings.Contains(command, want) {
		t.Fatalf("claude wrapper missing exec target, got %q", command)
	}
}

func TestWrapProductionPlainTextWarningHookCommand(t *testing.T) {
	t.Parallel()

	command := WrapProductionPlainTextWarningHookCommand("entire hooks factoryai-droid session-start", WarningFormatSingleLine)

	if command == "entire hooks factoryai-droid session-start" {
		t.Fatal("expected wrapped command, got raw command")
	}
	if strings.Contains(command, `>&2`) {
		t.Fatalf("plain text wrapper should not print warning to stderr, got %q", command)
	}
	if !strings.Contains(command, "Entire CLI is enabled but not installed") {
		t.Fatalf("plain text wrapper missing warning text, got %q", command)
	}
	if want := "exec entire hooks factoryai-droid session-start"; !strings.Contains(command, want) {
		t.Fatalf("plain text wrapper missing exec target, got %q", command)
	}
}

func TestMissingEntireWarning(t *testing.T) {
	t.Parallel()

	if got := MissingEntireWarning(WarningFormatSingleLine); strings.Contains(got, "\n") {
		t.Fatalf("single-line warning should not contain newlines, got %q", got)
	}
	if got := MissingEntireWarning(WarningFormatMultiLine); !strings.Contains(got, "\n") {
		t.Fatalf("multiline warning should contain newlines, got %q", got)
	}
}

func TestIsManagedHookCommand_DirectPrefix(t *testing.T) {
	t.Parallel()

	prefixes := []string{"entire ", `go run "$(git rev-parse --show-toplevel)"/cmd/entire/main.go `}

	if !IsManagedHookCommand("entire hooks codex stop", prefixes) {
		t.Fatal("expected direct entire command to match")
	}
	if !IsManagedHookCommand(`go run "$(git rev-parse --show-toplevel)"/cmd/entire/main.go hooks codex stop`, prefixes) {
		t.Fatal("expected local-dev command to match")
	}
}

func TestIsManagedHookCommand_WrappedPrefix(t *testing.T) {
	t.Parallel()

	prefixes := []string{"entire "}

	if !IsManagedHookCommand(
		WrapProductionSilentHookCommand("entire hooks cursor stop"),
		prefixes,
	) {
		t.Fatal("expected wrapped silent command to match")
	}
	if !IsManagedHookCommand(
		WrapProductionJSONWarningHookCommand("entire hooks claude-code session-start", WarningFormatSingleLine),
		prefixes,
	) {
		t.Fatal("expected wrapped json warning command to match")
	}
	if !IsManagedHookCommand(
		WrapProductionPlainTextWarningHookCommand("entire hooks factoryai-droid stop", WarningFormatSingleLine),
		prefixes,
	) {
		t.Fatal("expected wrapped plain text warning command to match")
	}
}

func TestIsManagedHookCommand_DoesNotMatchSubstring(t *testing.T) {
	t.Parallel()

	prefixes := []string{"entire ", `go run "$(git rev-parse --show-toplevel)"/cmd/entire/main.go `}

	if IsManagedHookCommand(`echo "the entire workflow finished"`, prefixes) {
		t.Fatal("unexpected match for unrelated substring command")
	}
	if IsManagedHookCommand(`sh -c 'echo "the entire workflow finished"; exit 0'`, prefixes) {
		t.Fatal("unexpected match for unrelated wrapped shell command")
	}
	if IsManagedHookCommand(`sh -c 'if ! command -v entire >/dev/null 2>&1; then exit 0; fi; exec echo "the entire workflow finished"'`, prefixes) {
		t.Fatal("unexpected match for wrapper that does not exec an Entire hook")
	}
}

func TestWriteAndReadJSONHookMeta(t *testing.T) {
	t.Parallel()

	raw := map[string]json.RawMessage{}
	if err := WriteJSONHookMeta(raw); err != nil {
		t.Fatalf("WriteJSONHookMeta: %v", err)
	}
	stamp, ok := raw[HookMetaKey]
	if !ok {
		t.Fatalf("expected key %q after WriteJSONHookMeta", HookMetaKey)
	}
	if !strings.Contains(string(stamp), `"cli_version"`) {
		t.Fatalf("expected cli_version in stamp, got %s", stamp)
	}

	meta, ok := ReadJSONHookMeta(raw)
	if !ok {
		t.Fatal("expected ReadJSONHookMeta to find stamp")
	}
	if meta.CLIVersion != HookMetaVersion() {
		t.Fatalf("meta.CLIVersion = %q, want %q", meta.CLIVersion, HookMetaVersion())
	}
}

func TestReadJSONHookMeta_Missing(t *testing.T) {
	t.Parallel()

	if _, ok := ReadJSONHookMeta(map[string]json.RawMessage{}); ok {
		t.Fatal("expected ok=false when stamp missing")
	}
	if _, ok := ReadJSONHookMeta(map[string]json.RawMessage{HookMetaKey: json.RawMessage(`{}`)}); ok {
		t.Fatal("expected ok=false when cli_version empty")
	}
	if _, ok := ReadJSONHookMeta(map[string]json.RawMessage{HookMetaKey: json.RawMessage(`not json`)}); ok {
		t.Fatal("expected ok=false when stamp is malformed")
	}
}

func TestReadJSONHookMetaFromFile(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"hooks": {}, "entireMeta": {"cli_version": "1.2.3"}}`)
	meta, ok := ReadJSONHookMetaFromFile(payload)
	if !ok {
		t.Fatal("expected ok=true for valid payload")
	}
	if meta.CLIVersion != "1.2.3" {
		t.Fatalf("meta.CLIVersion = %q, want %q", meta.CLIVersion, "1.2.3")
	}

	if _, ok := ReadJSONHookMetaFromFile([]byte(`not json`)); ok {
		t.Fatal("expected ok=false for invalid JSON")
	}
}

func TestReadTSHookMeta(t *testing.T) {
	t.Parallel()

	content := `// Entire CLI plugin for OpenCode
// Auto-generated by ` + "`entire enable --agent opencode`" + `
// entireMeta: {"cli_version":"0.4.9"}
// Do not edit manually.
import type { Plugin } from "@opencode-ai/plugin"`

	meta, ok := ReadTSHookMeta(content)
	if !ok {
		t.Fatal("expected ok=true for TS plugin with stamp comment")
	}
	if meta.CLIVersion != "0.4.9" {
		t.Fatalf("meta.CLIVersion = %q, want %q", meta.CLIVersion, "0.4.9")
	}

	if _, ok := ReadTSHookMeta("// Entire CLI plugin for OpenCode\n"); ok {
		t.Fatal("expected ok=false when no entireMeta comment")
	}
	if _, ok := ReadTSHookMeta("// entireMeta: {broken"); ok {
		t.Fatal("expected ok=false when JSON payload malformed")
	}
}

func TestTSHookMetaCommentLine(t *testing.T) {
	t.Parallel()

	line, err := TSHookMetaCommentLine()
	if err != nil {
		t.Fatalf("TSHookMetaCommentLine: %v", err)
	}
	if !strings.HasPrefix(line, "// entireMeta: ") {
		t.Fatalf("expected TS comment prefix, got %q", line)
	}
	// Round-trip the generated line through the reader.
	meta, ok := ReadTSHookMeta(line)
	if !ok {
		t.Fatalf("generated comment failed ReadTSHookMeta round-trip: %q", line)
	}
	if meta.CLIVersion != HookMetaVersion() {
		t.Fatalf("round-trip version = %q, want %q", meta.CLIVersion, HookMetaVersion())
	}
}
