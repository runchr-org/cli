package filter

import (
	"context"
	"log/slog"
	"os"

	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/settings"
)

// FromContext constructs a Pipeline using the current repo root, home directory,
// and user-configured transcript filters from settings.
// On error, logs a warning and returns nil (nil *Pipeline is safe to use).
func FromContext(ctx context.Context) *Pipeline {
	repoRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		logging.Warn(ctx, "filter: failed to get repo root, transcript filtering disabled",
			slog.String("error", err.Error()))
		return nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		logging.Warn(ctx, "filter: failed to get home directory, transcript filtering disabled",
			slog.String("error", err.Error()))
		return nil
	}

	var userFilters []settings.TranscriptFilter
	s, err := settings.Load(ctx)
	if err != nil {
		logging.Warn(ctx, "filter: failed to load settings, user transcript filters unavailable",
			slog.String("error", err.Error()))
		// Continue with built-in filters only — repo root and home dir
		// normalization still works even when settings are broken.
	} else {
		userFilters = s.TranscriptFilters
	}

	p, err := NewPipeline(repoRoot, homeDir, userFilters)
	if err != nil {
		logging.Warn(ctx, "filter: failed to build pipeline, transcript filtering disabled",
			slog.String("error", err.Error()))
		return nil
	}
	return p
}
