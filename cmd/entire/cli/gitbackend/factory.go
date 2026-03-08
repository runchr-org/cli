package gitbackend

import (
	"context"
	"fmt"

	"github.com/go-git/go-git/v6"
)

// Config holds per-category provider overrides.
// A nil or empty Config uses DefaultProvider for all categories.
type Config struct {
	// Default is the default provider for categories not listed in Overrides.
	// If empty or "auto", DefaultProvider() is used per category.
	Default Provider `json:"default,omitempty"`

	// Overrides maps specific operation categories to a provider.
	// Only listed categories are overridden; the rest use Default.
	Overrides map[OpCategory]Provider `json:"overrides,omitempty"`
}

// ProviderFor returns the resolved provider for a given operation category.
func (c *Config) ProviderFor(cat OpCategory) Provider {
	if c != nil {
		if p, ok := c.Overrides[cat]; ok && ValidProvider(p) && p != ProviderAuto {
			return p
		}
		if c.Default != "" && c.Default != ProviderAuto && ValidProvider(c.Default) {
			return c.Default
		}
	}
	return DefaultProvider(cat)
}

// Open opens a git repository and returns a Repository with per-category
// backend selection based on the given config.
//
// repoRoot is the absolute path to the worktree root directory.
// cfg controls which provider is used for each operation category.
// Pass nil for cfg to use all defaults (equivalent to "auto").
func Open(_ context.Context, repoRoot string, cfg *Config) (Repository, error) {
	// Always open a go-git repo — needed for GoGitRepository() and
	// as the backing implementation for go-git categories.
	goGitRepo, err := git.PlainOpen(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository at %s: %w", repoRoot, err)
	}

	return Build(goGitRepo, repoRoot, cfg), nil
}

// Build creates a Repository from an already-opened go-git repository.
// This is useful when the caller already has a *git.Repository.
func Build(goGitRepo *git.Repository, repoRoot string, cfg *Config) *compositeRepo { //nolint:revive // unexported return is intentional; callers use Repository interface
	goGit := NewGoGitRepository(goGitRepo, repoRoot)
	cli := NewCLIRepository(repoRoot, goGitRepo)

	pick := func(cat OpCategory) Repository {
		if cfg.ProviderFor(cat) == ProviderCLI {
			return cli
		}
		return goGit
	}

	return &compositeRepo{
		refs:      pick(OpRefs),
		commits:   pick(OpCommits),
		objects:   pick(OpObjects),
		worktree:  pick(OpWorktree),
		remote:    pick(OpRemote),
		diff:      pick(OpDiff),
		config:    pick(OpConfig),
		goGitRepo: goGitRepo,
	}
}

// compositeRepo delegates each operation category to its configured implementation.
type compositeRepo struct {
	refs      RefOps
	commits   CommitOps
	objects   ObjectOps
	worktree  WorktreeOps
	remote    RemoteOps
	diff      DiffOps
	config    ConfigOps
	goGitRepo *git.Repository
}

var _ Repository = (*compositeRepo)(nil)
