package gitprovider

// Compile-time interface satisfaction checks.
var (
	// GoGit implements reference and object operations.
	_ ReferenceProvider = (*GoGit)(nil)
	_ ObjectProvider    = (*GoGit)(nil)

	// CLI implements worktree, remote, config, and diff operations.
	_ WorktreeProvider = (*CLI)(nil)
	_ RemoteProvider   = (*CLI)(nil)
	_ ConfigProvider   = (*CLI)(nil)
	_ DiffProvider     = (*CLI)(nil)

	// Composite implements the full Repository interface.
	_ Repository = (*Composite)(nil)
)
