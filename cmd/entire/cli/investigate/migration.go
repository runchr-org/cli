// Package investigate — see env.go for package-level rationale.
//
// migration.go moves an investigate config from .entire/settings.json
// (committed) to .entire/settings.local.json (worktree-local). Triggered
// on every `entire investigate` invocation while the legacy field
// exists; once moved, it self-extinguishes. Mirrors review/migration.go
// in shape and copy.
package investigate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/entireio/cli/cmd/entire/cli/settings"
)

type projectInvestigateSettings struct {
	path string
	raw  map[string]json.RawMessage
	body json.RawMessage
}

// maybePromptInvestigateSettingsMigration runs the one-time interactive
// migration. If the project settings file has an "investigate" key, the
// user is asked whether to move it. Non-interactive callers receive a
// guidance line on stderr.
func maybePromptInvestigateSettingsMigration(
	ctx context.Context,
	out io.Writer,
	errOut io.Writer,
	canPrompt bool,
	promptYN func(context.Context, string, bool) (bool, error),
) error {
	project, ok := loadProjectInvestigateSettings(ctx)
	if !ok {
		return nil
	}

	if !canPrompt {
		fmt.Fprintln(errOut,
			"Investigate preferences are stored in project settings (.entire/settings.json). "+
				"Run `entire investigate --edit` interactively to move them to local preferences.")
		return nil
	}

	if promptYN == nil {
		return errors.New("migration: promptYN required for interactive prompt")
	}
	migrate, err := promptYN(ctx,
		"Investigate preferences are stored in project settings (.entire/settings.json). "+
			"Move them to local preferences (.entire/settings.local.json) now?", false)
	if err != nil {
		return fmt.Errorf("investigate settings migration prompt: %w", err)
	}
	if !migrate {
		return nil
	}

	if err := migrateProjectInvestigateSettings(ctx, project); err != nil {
		return err
	}
	fmt.Fprintln(out, "Moved investigate preferences from project settings to local preferences.")
	return nil
}

// loadProjectInvestigateSettings returns the project settings carrying an
// "investigate" key, or ok=false when no migration is needed. Malformed
// JSON yields ok=false silently — the user will see the actual parse
// error from settings.Load downstream with proper guidance, and blocking
// the migration prompt on bad JSON would make `entire investigate`
// unusable in the very situation it exists to help recover from.
func loadProjectInvestigateSettings(ctx context.Context) (*projectInvestigateSettings, bool) {
	path, raw, _, err := settings.LoadProjectRaw(ctx)
	if err != nil {
		return nil, false
	}
	body, ok := raw["investigate"]
	if !ok || isJSONNull(body) {
		return nil, false
	}
	return &projectInvestigateSettings{path: path, raw: raw, body: body}, true
}

func migrateProjectInvestigateSettings(ctx context.Context, project *projectInvestigateSettings) error {
	if project == nil {
		return nil
	}

	localPath, localRaw, _, err := settings.LoadLocalRaw(ctx)
	if err != nil {
		return fmt.Errorf("read local settings during migration: %w", err)
	}

	if _, exists := localRaw["investigate"]; !exists {
		localRaw["investigate"] = project.body
		if err := settings.SaveLocalRaw(localPath, localRaw); err != nil {
			return fmt.Errorf("write local settings: %w", err)
		}
	}

	delete(project.raw, "investigate")
	if err := settings.SaveProjectRaw(project.path, project.raw); err != nil {
		return fmt.Errorf("write project settings: %w", err)
	}
	return nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
