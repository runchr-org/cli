package gitprovider

import (
	"context"
	"errors"
	"fmt"

	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/settings"

	"github.com/go-git/go-git/v6"
)

// ProviderName identifies a git provider implementation.
type ProviderName string

const (
	// ProviderGoGit selects the go-git library implementation.
	ProviderGoGit ProviderName = "go-git"
	// ProviderCLI selects the git CLI implementation.
	ProviderCLI ProviderName = "cli"
)

// CategoryConfig maps each provider category to an implementation name.
// Any zero-value entry falls back to the category default.
type CategoryConfig struct {
	References ProviderName `json:"references,omitempty"`
	Objects    ProviderName `json:"objects,omitempty"`
	Worktree   ProviderName `json:"worktree,omitempty"`
	Remote     ProviderName `json:"remote,omitempty"`
	Config     ProviderName `json:"config,omitempty"`
	Diff       ProviderName `json:"diff,omitempty"`
}

// DefaultConfig returns the recommended defaults:
//   - go-git for references and objects (fast, in-process)
//   - CLI for worktree, remote, config, diff (correct, uses credential helpers)
func DefaultConfig() CategoryConfig {
	return CategoryConfig{
		References: ProviderGoGit,
		Objects:    ProviderGoGit,
		Worktree:   ProviderCLI,
		Remote:     ProviderCLI,
		Config:     ProviderCLI,
		Diff:       ProviderCLI,
	}
}

// Open creates a Repository using the given category configuration.
// It opens a go-git repository if any category requires it, and creates
// a CLI provider for categories that need it.
func Open(ctx context.Context, cfg CategoryConfig) (Repository, error) {
	cfg = withDefaults(cfg)

	needsGoGit := cfg.References == ProviderGoGit ||
		cfg.Objects == ProviderGoGit

	var goGitProvider *GoGit
	var cliProvider *CLI

	if needsGoGit {
		repo, err := openGoGitRepo(ctx)
		if err != nil {
			return nil, fmt.Errorf("opening go-git repository: %w", err)
		}
		goGitProvider = NewGoGit(repo)
	}

	// CLI is always available as fallback.
	cliProvider = NewCLI("")

	refs, err := pickRef(cfg.References, goGitProvider, cliProvider)
	if err != nil {
		return nil, err
	}

	objs, err := pickObj(cfg.Objects, goGitProvider, cliProvider)
	if err != nil {
		return nil, err
	}

	return NewComposite(
		WithReferenceProvider(refs),
		WithObjectProvider(objs),
		WithWorktreeProvider(cliProvider),
		WithRemoteProvider(cliProvider),
		WithConfigProvider(cliProvider),
		WithDiffProvider(cliProvider),
	), nil
}

// OpenDefault creates a Repository using defaults merged with any
// git_provider overrides from .entire/settings.json.
func OpenDefault(ctx context.Context) (Repository, error) {
	return Open(ctx, ConfigFromSettings(ctx))
}

// ConfigFromSettings reads the git_provider section from the Entire settings
// and converts it to a CategoryConfig. Missing fields use sensible defaults.
func ConfigFromSettings(ctx context.Context) CategoryConfig {
	cfg := DefaultConfig()

	s, err := settings.Load(ctx)
	if err != nil || s.GitProvider == nil {
		return cfg
	}

	gp := s.GitProvider
	if p := toProviderName(gp.References); p != "" {
		cfg.References = p
	}
	if p := toProviderName(gp.Objects); p != "" {
		cfg.Objects = p
	}
	if p := toProviderName(gp.Worktree); p != "" {
		cfg.Worktree = p
	}
	if p := toProviderName(gp.Remote); p != "" {
		cfg.Remote = p
	}
	if p := toProviderName(gp.Config); p != "" {
		cfg.Config = p
	}
	if p := toProviderName(gp.Diff); p != "" {
		cfg.Diff = p
	}
	return cfg
}

// toProviderName converts a settings string to a ProviderName.
// Returns "" for unrecognized values, which the caller treats as "use default".
func toProviderName(s string) ProviderName {
	switch s {
	case string(ProviderGoGit):
		return ProviderGoGit
	case string(ProviderCLI):
		return ProviderCLI
	default:
		return ""
	}
}

// openGoGitRepo opens a go-git repository from the worktree root.
func openGoGitRepo(ctx context.Context) (*git.Repository, error) {
	repoRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		repoRoot = "."
	}
	repo, err := git.PlainOpen(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository at %s: %w", repoRoot, err)
	}
	return repo, nil
}

// withDefaults fills in zero-value fields with sensible defaults.
func withDefaults(cfg CategoryConfig) CategoryConfig {
	defaults := DefaultConfig()
	if cfg.References == "" {
		cfg.References = defaults.References
	}
	if cfg.Objects == "" {
		cfg.Objects = defaults.Objects
	}
	if cfg.Worktree == "" {
		cfg.Worktree = defaults.Worktree
	}
	if cfg.Remote == "" {
		cfg.Remote = defaults.Remote
	}
	if cfg.Config == "" {
		cfg.Config = defaults.Config
	}
	if cfg.Diff == "" {
		cfg.Diff = defaults.Diff
	}
	return cfg
}

func pickRef(name ProviderName, goGit *GoGit, _ *CLI) (ReferenceProvider, error) {
	if name == ProviderGoGit {
		if goGit == nil {
			return nil, errors.New("go-git provider required for references but not available")
		}
		return goGit, nil
	}
	return nil, errors.New("CLI provider does not support full ReferenceProvider (only BranchExists/DeleteBranch)")
}

func pickObj(name ProviderName, goGit *GoGit, _ *CLI) (ObjectProvider, error) {
	if name == ProviderGoGit {
		if goGit == nil {
			return nil, errors.New("go-git provider required for objects but not available")
		}
		return goGit, nil
	}
	return nil, errors.New("CLI provider does not support ObjectProvider")
}
