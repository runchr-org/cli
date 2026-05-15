package checkpoint

import (
	"context"
	"errors"
	"fmt"

	"github.com/entireio/cli/cmd/entire/cli/paths"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
)

// FetchRefFunc is a function that fetches a ref from the remote.
// Used as a dependency injection point so this package doesn't import cli.
type FetchRefFunc func(ctx context.Context) error

// GetV2MetadataTree resolves the v2 /main ref tree with fetch fallback.
// See GetV2MetadataTreeWithHooks for the full strategy chain.
func GetV2MetadataTree(ctx context.Context, treelessFetchFn, fullFetchFn FetchRefFunc, openRepoFn func(context.Context) (*git.Repository, error)) (*object.Tree, *git.Repository, error) {
	return GetV2MetadataTreeWithHooks(ctx, treelessFetchFn, fullFetchFn, openRepoFn, AttemptHooks{})
}

// GetV2MetadataTreeWithHooks resolves the v2 /main ref tree with fetch fallback,
// emitting AttemptHooks events around each strategy:
//  1. Treeless fetch → open fresh repo → read /main ref tree
//  2. Local ref lookup
//  3. Full fetch → read tree
//
// Takes fetch functions as dependencies to avoid importing the cli package.
// openRepoFn opens a fresh repository (needed after fetch to see new packfiles).
// hooks may be the zero value to opt out of progress notifications.
func GetV2MetadataTreeWithHooks(ctx context.Context, treelessFetchFn, fullFetchFn FetchRefFunc, openRepoFn func(context.Context) (*git.Repository, error), hooks AttemptHooks) (*object.Tree, *git.Repository, error) {
	refName := plumbing.ReferenceName(paths.V2MainRefName)

	// Helper: run one attempt (fetch + open + read), return (tree, repo, ok).
	// Emits OnStart/OnFinish around the combined work; the finish error is
	// whatever step failed first (or nil on success).
	attempt := func(label string, fetchFn FetchRefFunc) (*object.Tree, *git.Repository, bool) {
		var (
			tree    *object.Tree
			repo    *git.Repository
			runErr  error
			started bool
		)
		fn := func() error {
			if fetchFn != nil {
				if err := fetchFn(ctx); err != nil {
					runErr = err
					return err
				}
			}
			r, err := openRepoFn(ctx)
			if err != nil {
				runErr = err
				return err
			}
			t, err := getV2RefTree(r, refName)
			if err != nil {
				runErr = err
				return err
			}
			tree, repo = t, r
			return nil
		}
		hooks.WithLabel(label, fn)
		started = runErr == nil
		return tree, repo, started
	}

	if treelessFetchFn != nil {
		if tree, repo, ok := attempt("Treeless fetch of v2 /main from origin", treelessFetchFn); ok {
			return tree, repo, nil
		}
	}

	if tree, repo, ok := attempt("Reading v2 /main from local", nil); ok {
		return tree, repo, nil
	}

	if fullFetchFn != nil {
		if tree, repo, ok := attempt("Full fetch of v2 /main from origin", fullFetchFn); ok {
			return tree, repo, nil
		}
	}

	return nil, nil, errors.New("v2 /main ref not available")
}

// getV2RefTree reads the tree from a custom ref (not a branch — no refs/heads/ prefix).
func getV2RefTree(repo *git.Repository, refName plumbing.ReferenceName) (*object.Tree, error) {
	ref, err := repo.Reference(refName, true)
	if err != nil {
		return nil, fmt.Errorf("ref %s not found: %w", refName, err)
	}

	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit for ref %s: %w", refName, err)
	}

	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("failed to get tree for ref %s: %w", refName, err)
	}
	return tree, nil
}
