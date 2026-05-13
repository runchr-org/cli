package checkpoint

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
)

// The /full/root commit is constructed with fixed inputs (empty tree, fixed
// author, fixed Unix-epoch timestamp, fixed message, no signature) so every
// client produces an identical SHA. That lets concurrent first-time creation
// across machines converge — both clients write the same bytes, so the push
// is a no-op rather than a conflict. Don't change these inputs without
// understanding the migration consequence: clients on different versions
// would produce different SHAs.
const (
	v2FullRootAuthorName  = "Entire Checkpoints"
	v2FullRootAuthorEmail = "checkpoints@entire.io"
	v2FullRootMessage     = "Entire checkpoints v2 root\n"

	// v2FullRootHash is the SHA of the deterministic root commit. Pinned so
	// production code can detect a /full/root ref that has been corrupted or
	// repointed (a tripwire — we surface a warning, we do not auto-repair).
	// Also used in tests to assert determinism end-to-end.
	v2FullRootHash = "c095af40b171ff4c3c4a781abacd39aa499e183b"
)

func buildV2FullRootCommit(ctx context.Context, repo *git.Repository) (plumbing.Hash, error) {
	emptyTreeHash, err := BuildTreeFromEntries(ctx, repo, make(map[string]object.TreeEntry))
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("build empty tree for /full/root: %w", err)
	}

	sig := object.Signature{
		Name:  v2FullRootAuthorName,
		Email: v2FullRootAuthorEmail,
		When:  time.Unix(0, 0).UTC(),
	}

	commit := &object.Commit{
		TreeHash:  emptyTreeHash,
		Author:    sig,
		Committer: sig,
		Message:   v2FullRootMessage,
	}
	// Don't sign — signatures are non-deterministic and would break SHA convergence.

	obj := repo.Storer.NewEncodedObject()
	if err := commit.Encode(obj); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("encode /full/root commit: %w", err)
	}

	hash, err := repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("store /full/root commit: %w", err)
	}
	return hash, nil
}

// ensureV2FullRoot returns the local /full/root commit hash, building and
// setting the ref if it does not yet exist. Local-only; remote publication
// happens through the push pipeline. If the ref exists but points at an
// unexpected hash, a warning is logged and the existing hash is returned —
// we don't auto-repair, because overwriting could clobber an intentional
// override.
func (s *V2GitStore) ensureV2FullRoot(ctx context.Context) (plumbing.Hash, error) {
	refName := plumbing.ReferenceName(paths.V2FullRootRefName)

	if ref, err := s.repo.Reference(refName, true); err == nil {
		if ref.Hash().String() != v2FullRootHash {
			logging.Warn(ctx, "v2 /full/root points at unexpected hash; future generations will use it as-is",
				slog.String("ref", refName.String()),
				slog.String("expected", v2FullRootHash),
				slog.String("actual", ref.Hash().String()),
			)
		}
		return ref.Hash(), nil
	}

	hash, err := buildV2FullRootCommit(ctx, s.repo)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("ensure /full/root: %w", err)
	}

	ref := plumbing.NewHashReference(refName, hash)
	if err := s.repo.Storer.SetReference(ref); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("set local /full/root: %w", err)
	}
	return hash, nil
}
