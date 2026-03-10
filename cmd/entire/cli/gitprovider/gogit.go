//nolint:wrapcheck // GoGit is a thin delegation layer over go-git; wrapping would add noise without value.
package gitprovider

import (
	"errors"
	"fmt"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/plumbing/storer"
	"github.com/go-git/go-git/v6/storage"
)

// GoGit implements ReferenceProvider and ObjectProvider using the go-git library.
type GoGit struct {
	repo *git.Repository
}

// NewGoGit creates a go-git backed provider from an already-opened repository.
func NewGoGit(repo *git.Repository) *GoGit {
	return &GoGit{repo: repo}
}

// WrapGoGit creates a gitprovider.Repository from a *git.Repository by using
// GoGit for reference/object operations. This is a convenience for callers that
// have a *git.Repository and need to pass it to functions expecting Repository.
// Note: only ReferenceProvider and ObjectProvider are backed; other sub-interfaces
// will panic if called. Use OpenDefault() for a fully-featured Repository.
func WrapGoGit(repo *git.Repository) Repository {
	goGit := NewGoGit(repo)
	return NewComposite(
		WithReferenceProvider(goGit),
		WithObjectProvider(goGit),
	)
}

// --- ReferenceProvider ---

func (g *GoGit) Head() (*plumbing.Reference, error) {
	return g.repo.Head()
}

func (g *GoGit) GetReference(name plumbing.ReferenceName, resolve bool) (*plumbing.Reference, error) {
	return g.repo.Reference(name, resolve)
}

func (g *GoGit) SetReference(ref *plumbing.Reference) error {
	return g.repo.Storer.SetReference(ref)
}

func (g *GoGit) ListBranches() (storer.ReferenceIter, error) {
	return g.repo.Branches()
}

func (g *GoGit) ListReferences() (storer.ReferenceIter, error) {
	return g.repo.References()
}

func (g *GoGit) BranchExists(name string) (bool, error) {
	_, err := g.repo.Reference(plumbing.NewBranchReferenceName(name), true)
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("checking branch %s: %w", name, err)
	}
	return true, nil
}

func (g *GoGit) DeleteBranch(name string) error {
	refName := plumbing.NewBranchReferenceName(name)
	return g.repo.Storer.RemoveReference(refName)
}

// --- ObjectProvider ---

func (g *GoGit) CommitObject(hash plumbing.Hash) (*object.Commit, error) {
	return g.repo.CommitObject(hash)
}

func (g *GoGit) TreeObject(hash plumbing.Hash) (*object.Tree, error) {
	return g.repo.TreeObject(hash)
}

func (g *GoGit) BlobObject(hash plumbing.Hash) (*object.Blob, error) {
	return g.repo.BlobObject(hash)
}

func (g *GoGit) Log(opts *git.LogOptions) (object.CommitIter, error) {
	return g.repo.Log(opts)
}

func (g *GoGit) IsEmpty() (bool, error) {
	_, err := g.repo.Head()
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return true, nil
		}
		return false, fmt.Errorf("checking if repository is empty: %w", err)
	}
	return false, nil
}

func (g *GoGit) Storer() storage.Storer {
	return g.repo.Storer
}
