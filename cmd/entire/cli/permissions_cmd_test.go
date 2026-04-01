package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/permissions"
)

func TestRenderPermissionsTerminal_ShowsSuggestionsAndUnsupportedAgents(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	renderPermissionsTerminal(&buf, permissions.Report{
		SessionsAnalyzed: 4,
		Agents: []permissions.AgentReport{
			{
				Agent:            "Claude Code",
				SessionsAnalyzed: 2,
				Suggestions: []permissions.Suggestion{
					{
						Rule:         "mise run test",
						Reason:       "Claude reviewed the repo task and judged it safe to pre-approve.",
						Count:        2,
						SessionIDs:   []string{"s-1", "s-2"},
						Examples:     []string{"mise run test TestPermissions"},
						Conservative: true,
					},
				},
			},
			{
				Agent:            "Gemini CLI",
				SessionsAnalyzed: 2,
			},
		},
	})

	out := buf.String()
	for _, want := range []string{
		"Permissions Suggestions",
		"Claude Code",
		"mise run test",
		"Gemini CLI",
		"no conservative suggestions",
	} {
		if !strings.Contains(strings.ToLower(out), strings.ToLower(want)) {
			t.Fatalf("expected %q in output:\n%s", want, out)
		}
	}
}

func TestRenderPermissionsJSON_EncodesReport(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	report := permissions.Report{
		SessionsAnalyzed: 1,
		Agents: []permissions.AgentReport{
			{
				Agent: "Claude Code",
				Suggestions: []permissions.Suggestion{
					{Rule: "mise run test", Count: 2, Conservative: true},
				},
			},
		},
	}

	if err := renderPermissionsJSON(&buf, report); err != nil {
		t.Fatalf("renderPermissionsJSON returned error: %v", err)
	}

	var decoded permissions.Report
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("failed to decode JSON output: %v\n%s", err, buf.String())
	}

	if len(decoded.Agents) != 1 || decoded.Agents[0].Agent != "Claude Code" {
		t.Fatalf("unexpected decoded report: %+v", decoded)
	}
}

func TestNewRootCmd_RegistersPermissionsCommand(t *testing.T) {
	t.Parallel()

	cmd := NewRootCmd()
	found := false
	for _, child := range cmd.Commands() {
		if child.Name() == "permissions" {
			found = true
			break
		}
	}

	if !found {
		t.Fatal("expected root command to register permissions subcommand")
	}
}
