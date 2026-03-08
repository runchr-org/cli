package gitbackend

// Provider identifies a git backend implementation.
type Provider string

const (
	// ProviderGoGit uses the go-git library for git operations.
	ProviderGoGit Provider = "go-git"
	// ProviderCLI uses the git CLI for git operations.
	ProviderCLI Provider = "cli"
	// ProviderAuto selects the best implementation per operation category.
	ProviderAuto Provider = "auto"
)

// OpCategory identifies a category of git operations.
// Each category can be independently configured to use a different provider.
type OpCategory string

const (
	OpRefs     OpCategory = "refs"     // Reference/branch operations
	OpCommits  OpCategory = "commits"  // Commit reading/creation/traversal
	OpObjects  OpCategory = "objects"  // Tree/blob object store operations
	OpWorktree OpCategory = "worktree" // Working directory status/checkout/reset
	OpRemote   OpCategory = "remote"   // Fetch/push network operations
	OpDiff     OpCategory = "diff"     // Diff between commits
	OpConfig   OpCategory = "config"   // Git configuration and utility
)

// AllOpCategories returns all operation categories.
func AllOpCategories() []OpCategory {
	return []OpCategory{OpRefs, OpCommits, OpObjects, OpWorktree, OpRemote, OpDiff, OpConfig}
}

// DefaultProvider returns the default provider for each operation category.
// These defaults reflect known issues with go-git (see CLAUDE.md):
//   - worktree: CLI avoids go-git's bug with .gitignore handling and untracked directory deletion
//   - remote: CLI supports credential helpers that go-git lacks
//   - diff: CLI provides efficient diff-tree that go-git doesn't expose
//   - config: CLI handles non-standard config locations better
//
// All other categories default to go-git for performance and richer API access.
func DefaultProvider(cat OpCategory) Provider {
	switch cat { //nolint:exhaustive // default covers remaining categories
	case OpWorktree:
		return ProviderCLI
	case OpRemote:
		return ProviderCLI
	case OpDiff:
		return ProviderCLI
	default:
		return ProviderGoGit
	}
}

// ValidProvider returns true if p is a recognized provider.
func ValidProvider(p Provider) bool {
	return p == ProviderGoGit || p == ProviderCLI || p == ProviderAuto
}
