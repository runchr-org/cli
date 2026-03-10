package checkpoint

import (
	"github.com/entireio/cli/cmd/entire/cli/gitprovider"

	"github.com/go-git/go-git/v6"
)

// Compile-time check that GitStore implements the Store interface.
var _ Store = (*GitStore)(nil)

// GitStore provides operations for both temporary and committed checkpoint storage.
// It implements the Store interface by wrapping a git repository.
type GitStore struct {
	repo gitprovider.Repository
}

// NewGitStore creates a new checkpoint store from a *git.Repository.
// It wraps the repository with gitprovider.GoGit for reference and object operations.
func NewGitStore(repo *git.Repository) *GitStore {
	goGit := gitprovider.NewGoGit(repo)
	return &GitStore{repo: gitprovider.NewComposite(
		gitprovider.WithReferenceProvider(goGit),
		gitprovider.WithObjectProvider(goGit),
	)}
}

// NewGitStoreFromProvider creates a new checkpoint store backed by a gitprovider.Repository.
// Use this when you already have a gitprovider.Repository instance.
func NewGitStoreFromProvider(repo gitprovider.Repository) *GitStore {
	return &GitStore{repo: repo}
}

// Repository returns the underlying git repository provider.
// This is useful for strategies that need direct repository access.
func (s *GitStore) Repository() gitprovider.Repository {
	return s.repo
}
