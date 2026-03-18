package trail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/go-git/go-git/v6"
)

// ResolveStore returns the best available Store for the current context.
// If the user is authenticated, returns an APIStore (migrating git trails if needed).
// Otherwise, falls back to a GitStore.
func ResolveStore(ctx context.Context, repo *git.Repository) (Store, error) { //nolint:ireturn,unparam // intentional interface return; error reserved for future auth failures
	apiStore, err := NewAPIStore(ctx)
	if err != nil {
		if errors.Is(err, ErrNotAuthenticated) {
			// Not logged in — fall back to git store silently
			return NewGitStore(repo), nil
		}
		// Other errors (no remote, etc.) — fall back to git store with a warning
		slog.Debug("falling back to git-based trail store",
			slog.String("reason", err.Error()),
		)
		return NewGitStore(repo), nil
	}

	// Authenticated — migrate if needed, then use API
	migrated, migrateErr := MigrateIfNeeded(ctx, apiStore, repo)
	if migrateErr != nil {
		slog.Warn("trail migration failed, using API store anyway",
			slog.String("error", migrateErr.Error()),
		)
	} else if migrated > 0 {
		fmt.Printf("Migrated %d trail(s) to Entire API.\n", migrated)
	}

	return apiStore, nil
}

// IsAPIBacked returns true if the store is an API-backed store.
func IsAPIBacked(store Store) bool {
	_, ok := store.(*APIStore)
	return ok
}
