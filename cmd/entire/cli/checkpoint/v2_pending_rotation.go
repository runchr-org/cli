package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/jsonutil"
)

const (
	pendingV2FullGenerationPublicationVersion = 1
	pendingV2FullGenerationPublicationDirName = "entire-v2-rotations"
	pendingV2FullGenerationPublicationFile    = "pending.json"
)

type PendingV2FullGenerationPublication struct {
	ArchiveRefName    string `json:"archive_ref_name"`
	ArchiveCommitHash string `json:"archive_commit_hash"`
	// PreviousFullCurrentHash and ResetFullCurrentRootHash are set when the
	// archive publication came from a local /full/current rotation.
	PreviousFullCurrentHash  string    `json:"previous_full_current_hash,omitempty"`
	ResetFullCurrentRootHash string    `json:"reset_full_current_root_hash,omitempty"`
	QueuedAt                 time.Time `json:"queued_at"`
}

type pendingV2FullGenerationPublicationState struct {
	Version      int                                  `json:"version"`
	Publications []PendingV2FullGenerationPublication `json:"publications"`
}

func (s *V2GitStore) AppendPendingFullGenerationPublication(ctx context.Context, publication PendingV2FullGenerationPublication) error {
	return s.AppendPendingFullGenerationPublications(ctx, []PendingV2FullGenerationPublication{publication})
}

func (s *V2GitStore) AppendPendingFullGenerationPublications(ctx context.Context, publications []PendingV2FullGenerationPublication) error {
	if len(publications) == 0 {
		return nil
	}
	state, err := s.readPendingFullGenerationPublicationState(ctx)
	if err != nil {
		return err
	}
	state.Version = pendingV2FullGenerationPublicationVersion
	state.Publications = append(state.Publications, publications...)
	return s.writePendingFullGenerationPublicationState(ctx, state)
}

func (s *V2GitStore) ReadPendingFullGenerationPublications(ctx context.Context) ([]PendingV2FullGenerationPublication, error) {
	state, err := s.readPendingFullGenerationPublicationState(ctx)
	if err != nil {
		return nil, err
	}
	return state.Publications, nil
}

func (s *V2GitStore) ClearPendingFullGenerationPublications(ctx context.Context) error {
	path, err := s.pendingFullGenerationPublicationFilePath(ctx)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove pending v2 full generation publications: %w", err)
	}
	return nil
}

func (s *V2GitStore) readPendingFullGenerationPublicationState(ctx context.Context) (pendingV2FullGenerationPublicationState, error) {
	path, err := s.pendingFullGenerationPublicationFilePath(ctx)
	if err != nil {
		return pendingV2FullGenerationPublicationState{}, err
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is under git common dir
	if os.IsNotExist(err) {
		return pendingV2FullGenerationPublicationState{Version: pendingV2FullGenerationPublicationVersion}, nil
	}
	if err != nil {
		return pendingV2FullGenerationPublicationState{}, fmt.Errorf("read pending v2 full generation publications: %w", err)
	}

	var state pendingV2FullGenerationPublicationState
	if err := json.Unmarshal(data, &state); err != nil {
		return pendingV2FullGenerationPublicationState{}, fmt.Errorf("parse pending v2 full generation publications: %w", err)
	}
	if state.Version != pendingV2FullGenerationPublicationVersion {
		return pendingV2FullGenerationPublicationState{}, fmt.Errorf("unsupported pending v2 full generation publication version %d", state.Version)
	}
	return state, nil
}

func (s *V2GitStore) writePendingFullGenerationPublicationState(ctx context.Context, state pendingV2FullGenerationPublicationState) error {
	path, err := s.pendingFullGenerationPublicationFilePath(ctx)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create pending v2 full generation publication dir: %w", err)
	}

	data, err := jsonutil.MarshalIndentWithNewline(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pending v2 full generation publications: %w", err)
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(path), pendingV2FullGenerationPublicationFile+".*.tmp")
	if err != nil {
		return fmt.Errorf("create pending v2 full generation publication temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write pending v2 full generation publications: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close pending v2 full generation publications: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace pending v2 full generation publications: %w", err)
	}
	removeTmp = false
	return nil
}

func (s *V2GitStore) pendingFullGenerationPublicationFilePath(ctx context.Context) (string, error) {
	commonDir, err := s.gitCommonDir(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(commonDir, pendingV2FullGenerationPublicationDirName, pendingV2FullGenerationPublicationFile), nil
}

func (s *V2GitStore) gitCommonDir(ctx context.Context) (string, error) {
	worktree, err := s.repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("open worktree for pending v2 full generation publications: %w", err)
	}
	root := worktree.Filesystem.Root()
	if root == "" {
		return "", errors.New("resolve worktree root for pending v2 full generation publications")
	}

	cmd := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--git-common-dir")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("resolve git common dir for pending v2 full generation publications: %w", err)
	}
	commonDir := strings.TrimSpace(string(output))
	if commonDir == "" {
		return "", errors.New("resolve git common dir for pending v2 full generation publications: empty output")
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(root, commonDir)
	}
	return filepath.Clean(commonDir), nil
}
