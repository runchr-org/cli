package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/jsonutil"
	"github.com/entireio/cli/cmd/entire/cli/versioninfo"
)

// HookMetaKey is the top-level JSON key that stores Entire's hook metadata
// stamp on each agent's config file (.claude/settings.json, .cursor/hooks.json, etc.).
const HookMetaKey = "entireMeta"

// HookMeta records which CLI version generated or last modified the installed
// hooks. It lets `entire status` / `entire enable` detect when a user's hooks
// fall below an agent's minimum compatible version and prompt an upgrade.
type HookMeta struct {
	CLIVersion string `json:"cli_version"`
}

// HookMetaVersion returns the CLI version string to stamp into config files.
func HookMetaVersion() string {
	return versioninfo.Version
}

// WriteJSONHookMeta stamps the current CLI version into rawSettings under
// HookMetaKey. Callers invoke it right before marshalling the settings map
// back to disk in InstallHooks.
func WriteJSONHookMeta(rawSettings map[string]json.RawMessage) error {
	meta := HookMeta{CLIVersion: HookMetaVersion()}
	data, err := jsonutil.MarshalWithNoHTMLEscape(meta)
	if err != nil {
		return fmt.Errorf("failed to marshal entireMeta: %w", err)
	}
	rawSettings[HookMetaKey] = data
	return nil
}

// ReadJSONHookMeta extracts a HookMeta from a decoded settings map. The second
// return value is false when the stamp is missing or unreadable — callers treat
// that as "needs upgrade once to acquire a stamp".
func ReadJSONHookMeta(rawSettings map[string]json.RawMessage) (HookMeta, bool) {
	raw, ok := rawSettings[HookMetaKey]
	if !ok {
		return HookMeta{}, false
	}
	var meta HookMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return HookMeta{}, false
	}
	if meta.CLIVersion == "" {
		return HookMeta{}, false
	}
	return meta, true
}

// ReadJSONHookMetaFromFile is a convenience for agents whose ReadHookMeta method
// just needs to unmarshal a single config file.
func ReadJSONHookMetaFromFile(data []byte) (HookMeta, bool) {
	var rawSettings map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawSettings); err != nil {
		return HookMeta{}, false
	}
	return ReadJSONHookMeta(rawSettings)
}

// tsHookMetaCommentPrefix is the comment marker used in OpenCode's TS plugin
// (the only non-JSON config). InstallHooks embeds one line such as:
//
//	// entireMeta: {"cli_version":"0.5.3"}
//
// in the generated plugin header; ReadTSHookMeta scans for it.
const tsHookMetaCommentPrefix = "// entireMeta: "

// TSHookMetaCommentLine returns the comment line that OpenCode's InstallHooks
// embeds in the generated plugin to stamp the CLI version.
func TSHookMetaCommentLine() (string, error) {
	meta := HookMeta{CLIVersion: HookMetaVersion()}
	data, err := jsonutil.MarshalWithNoHTMLEscape(meta)
	if err != nil {
		return "", fmt.Errorf("failed to marshal entireMeta comment: %w", err)
	}
	return tsHookMetaCommentPrefix + string(data), nil
}

// ReadTSHookMeta scans content (typically the first few lines of a generated
// TS plugin) for a `// entireMeta: {...}` marker and returns the parsed meta.
// Returns ok=false when no marker is found or the JSON is malformed.
func ReadTSHookMeta(content string) (HookMeta, bool) {
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, tsHookMetaCommentPrefix) {
			continue
		}
		payload := strings.TrimPrefix(trimmed, tsHookMetaCommentPrefix)
		var meta HookMeta
		if err := json.Unmarshal([]byte(payload), &meta); err != nil {
			return HookMeta{}, false
		}
		if meta.CLIVersion == "" {
			return HookMeta{}, false
		}
		return meta, true
	}
	return HookMeta{}, false
}

// MinCompatibleCLIVersion is the lowest CLI version whose installed hooks
// remain compatible with the running binary, applied uniformly across every
// hook-supporting agent. Bump this string exactly when a hook-contract change
// in any agent warrants forcing users to re-run `entire enable --force`.
// Seeded at "0.0.0" so existing installs never trigger drift warnings until
// we intentionally raise the floor.
const MinCompatibleCLIVersion = "0.0.0"

// HookVersionSupport is an optional capability implemented by hook-supporting
// agents that carry a version stamp in their config file. It lets drift.go
// discover an agent's installed stamp without knowing the concrete config
// format. The compatibility floor itself is a package-wide constant
// (MinCompatibleCLIVersion); agents don't pick their own.
type HookVersionSupport interface {
	// ReadHookMeta returns the stamp recorded in the agent's config. The second
	// return value is false when the config lacks a stamp — treated as drift
	// once the global MinCompatibleCLIVersion rises above "0.0.0".
	ReadHookMeta(ctx context.Context) (HookMeta, bool, error)
}

type WarningFormat int

const (
	WarningFormatSingleLine WarningFormat = iota + 1
	WarningFormatMultiLine
)

func MissingEntireWarning(format WarningFormat) string {
	switch format {
	case WarningFormatSingleLine:
		return "Entire CLI is enabled but not installed or not on PATH. Installation guide: https://docs.entire.io/cli/installation#installation-methods"
	case WarningFormatMultiLine:
		return "\n\nEntire CLI is enabled but not installed or not on PATH.\nInstallation guide: https://docs.entire.io/cli/installation#installation-methods"
	default:
		return MissingEntireWarning(WarningFormatSingleLine)
	}
}

// WrapProductionSilentHookCommand exits successfully without output when the
// Entire CLI is missing from PATH.
func WrapProductionSilentHookCommand(command string) string {
	return fmt.Sprintf(
		`sh -c 'if ! command -v entire >/dev/null 2>&1; then exit 0; fi; exec %s'`,
		command,
	)
}

// WrapProductionJSONWarningHookCommand emits a JSON hook response with a
// systemMessage field on stdout when the Entire CLI is missing from PATH.
func WrapProductionJSONWarningHookCommand(command string, format WarningFormat) string {
	payload, err := jsonutil.MarshalWithNoHTMLEscape(struct {
		SystemMessage string `json:"systemMessage,omitempty"`
	}{
		SystemMessage: MissingEntireWarning(format),
	})
	if err != nil {
		// Fallback to plain text on stdout if JSON payload construction somehow fails.
		return WrapProductionPlainTextWarningHookCommand(command, format)
	}

	return fmt.Sprintf(
		`sh -c 'if ! command -v entire >/dev/null 2>&1; then printf "%%s\n" %q; exit 0; fi; exec %s'`,
		string(payload),
		command,
	)
}

// WrapProductionPlainTextWarningHookCommand emits the warning as plain
// text to stdout when the Entire CLI is missing from PATH.
func WrapProductionPlainTextWarningHookCommand(command string, format WarningFormat) string {
	return fmt.Sprintf(
		`sh -c 'if ! command -v entire >/dev/null 2>&1; then printf "%%s\n" %q; exit 0; fi; exec %s'`,
		MissingEntireWarning(format),
		command,
	)
}

const productionHookWrapperPrefix = `sh -c 'if ! command -v entire >/dev/null 2>&1; then `

// IsManagedHookCommand reports whether command is either a direct Entire hook
// command or one of Entire's production wrapper forms that exec that command.
func IsManagedHookCommand(command string, prefixes []string) bool {
	if hasManagedHookPrefix(command, prefixes) {
		return true
	}
	if !strings.HasPrefix(command, productionHookWrapperPrefix) {
		return false
	}

	_, wrappedCommand, ok := strings.Cut(command, "; fi; exec ")
	if !ok {
		return false
	}

	return hasManagedHookPrefix(wrappedCommand, prefixes)
}

func hasManagedHookPrefix(command string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(command, prefix) {
			return true
		}
	}
	return false
}
