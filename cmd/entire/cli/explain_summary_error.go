package cli

import (
	"errors"
	"fmt"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
)

// displayNameFor maps an AgentName to the proper-noun display string used in
// user-facing error messages. Unknown providers fall through to the registry
// key so we never render an empty prefix.
func displayNameFor(p types.AgentName) string {
	switch p {
	case agent.AgentNameClaudeCode:
		return "Claude"
	case agent.AgentNameCodex:
		return "Codex"
	case agent.AgentNameGemini:
		return "Gemini"
	case agent.AgentNameCursor:
		return "Cursor"
	case agent.AgentNameCopilotCLI:
		return "Copilot"
	default:
		return string(p)
	}
}

// kindPrefix returns the user-facing prefix for a given TextGenErrorKind,
// parameterized by the provider's display name. CLIMissing is handled
// separately by renderTextGenError because its message omits the prefix
// entirely; Unknown falls through to the default branch.
func kindPrefix(k agent.TextGenErrorKind, displayName string) string {
	switch k { //nolint:exhaustive // CLIMissing handled separately, Unknown in default
	case agent.TextGenErrorAuth:
		return displayName + " authentication failed"
	case agent.TextGenErrorRateLimit:
		return displayName + " rejected the summary request due to rate limits or quota"
	case agent.TextGenErrorConfig:
		return displayName + " rejected the summary request"
	default:
		return displayName + " failed to generate the summary"
	}
}

// syntheticFallback holds a generic remediation line per-provider per-kind.
// It is applied only when the provider is in
// providersNeedingSynthesizedRemediation AND the envelope-derived Message is
// absent (so we have nothing better to show), OR — for Claude — in addition
// to Message (to byte-match 963's established wording).
//
// Non-Claude entries are deliberately generic ("Check your X CLI
// authentication") rather than inventing CLI-specific subcommands: the
// user's actual CLI stderr already carries the authoritative remediation,
// and inventing fake commands like `gemini auth login` would mislead users
// when the real subcommand is different.
var syntheticFallback = map[types.AgentName]map[agent.TextGenErrorKind]string{
	agent.AgentNameClaudeCode: {
		agent.TextGenErrorAuth:      "Run `claude login` and retry",
		agent.TextGenErrorRateLimit: "Wait and retry",
		agent.TextGenErrorConfig:    "Check your Claude CLI config and selected model",
	},
	agent.AgentNameCodex: {
		agent.TextGenErrorAuth:      "Check your Codex CLI authentication",
		agent.TextGenErrorRateLimit: "Wait and retry",
		agent.TextGenErrorConfig:    "Check your Codex CLI config and selected model",
	},
	agent.AgentNameGemini: {
		agent.TextGenErrorAuth:      "Check your Gemini CLI authentication",
		agent.TextGenErrorRateLimit: "Wait and retry",
		agent.TextGenErrorConfig:    "Check your Gemini CLI config and selected model",
	},
	agent.AgentNameCursor: {
		agent.TextGenErrorAuth:      "Check your Cursor CLI authentication",
		agent.TextGenErrorRateLimit: "Wait and retry",
		agent.TextGenErrorConfig:    "Check your Cursor CLI config and selected model",
	},
	agent.AgentNameCopilotCLI: {
		agent.TextGenErrorAuth:      "Check your Copilot CLI authentication",
		agent.TextGenErrorRateLimit: "Wait and retry",
		agent.TextGenErrorConfig:    "Check your Copilot CLI config and selected model",
	},
}

// providersNeedingSynthesizedRemediation lists providers whose envelope
// rarely carries an actionable remediation line, so we append our
// syntheticFallback even when a Message is already present.
//
// Claude is the only such provider today: its structured envelope surfaces
// a terse API error (e.g. "Invalid API key") without telling the user what
// to do about it, so 963's user-facing output appended "Run `claude login`
// and retry". Non-Claude CLIs (codex, gemini, cursor, copilot) emit full
// human-readable remediation in their own stderr output, which we capture
// verbatim into Message — synthesizing another remediation line on top
// would produce noisy or contradictory output.
var providersNeedingSynthesizedRemediation = map[types.AgentName]bool{
	agent.AgentNameClaudeCode: true,
}

// formatTextGenErrorSuffix builds a non-empty diagnostic suffix for the
// Unknown fallthrough path. Mirrors 963's formatClaudeErrorSuffix — prefers
// the envelope Message, then HTTP status, then a real exit code, then the
// "abnormal termination" branch for ExitCode < 0, and finally a sentinel so
// the user never sees "<Display> failed to generate the summary" followed
// by nothing.
func formatTextGenErrorSuffix(e *agent.TextGenError, displayName string) string {
	if e.Message != "" {
		return ": " + e.Message
	}
	switch {
	case e.APIStatus != 0:
		return fmt.Sprintf(" (%s API returned HTTP %d)", displayName, e.APIStatus)
	case e.ExitCode > 0:
		return fmt.Sprintf(" (%s CLI exited with code %d)", displayName, e.ExitCode)
	case e.ExitCode < 0:
		return fmt.Sprintf(" (%s CLI terminated abnormally — no exit code captured)", displayName)
	default:
		return fmt.Sprintf(" (no diagnostic detail available from %s CLI)", displayName)
	}
}

// renderTextGenError maps a typed *agent.TextGenError to the user-facing
// error message. Claude's wording is byte-identical to 963's baseline in
// formatCheckpointSummaryError; non-Claude providers prefer their CLI's
// own stderr verbatim (captured in Message) and only synthesize a generic
// remediation line when Message is empty.
func renderTextGenError(e *agent.TextGenError) error {
	displayName := displayNameFor(e.Provider)

	if e.Kind == agent.TextGenErrorCLIMissing {
		// Short, provider-agnostic: the CLI isn't even present, so there's
		// no stderr to show and no useful kind-specific remediation beyond
		// "install it".
		return fmt.Errorf("%s CLI is not installed or not on PATH", displayName)
	}

	if e.Kind == agent.TextGenErrorUnknown {
		return fmt.Errorf("%s%s", kindPrefix(e.Kind, displayName), formatTextGenErrorSuffix(e, displayName))
	}

	prefix := kindPrefix(e.Kind, displayName)
	needsSynthesis := providersNeedingSynthesizedRemediation[e.Provider]
	fallback := syntheticFallback[e.Provider][e.Kind]

	switch {
	case e.Message != "" && needsSynthesis && fallback != "":
		// Claude path: byte-identical to 963's "<prefix>: <msg>\n<fallback>"
		// when the envelope carries a terse API message that lacks
		// remediation.
		return fmt.Errorf("%s: %s\n%s", prefix, e.Message, fallback)
	case e.Message != "":
		// Non-Claude path: the CLI's own stderr already carries the
		// authoritative remediation, so we render it verbatim without
		// appending a synthesized line.
		return fmt.Errorf("%s: %s", prefix, e.Message)
	case fallback != "":
		// No Message but we have a generic fallback — better than a bare
		// prefix line.
		return fmt.Errorf("%s\n%s", prefix, fallback)
	default:
		return errors.New(prefix)
	}
}
