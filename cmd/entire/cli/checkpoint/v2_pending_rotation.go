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
	pendingV2FullRotationVersion = 1
	pendingV2FullRotationDirName = "entire-v2-rotations"
	pendingV2FullRotationFile    = "pending.json"
)

type PendingV2FullRotation struct {
	ArchiveRefName             string    `json:"archive_ref_name"`
	PreviousFullCurrentHash    string    `json:"previous_full_current_hash"`
	ArchivedFullGenerationHash string    `json:"archived_full_generation_hash"`
	ResetFullCurrentRootHash   string    `json:"reset_full_current_root_hash"`
	RotatedAt                  time.Time `json:"rotated_at"`
}

type pendingV2FullRotationState struct {
	Version   int                     `json:"version"`
	Rotations []PendingV2FullRotation `json:"rotations"`
}

func (s *V2GitStore) AppendPendingFullRotation(ctx context.Context, rotation PendingV2FullRotation) error {
	state, err := s.readPendingFullRotationState(ctx)
	if err != nil {
		return err
	}
	state.Version = pendingV2FullRotationVersion
	state.Rotations = append(state.Rotations, rotation)
	return s.writePendingFullRotationState(ctx, state)
}

func (s *V2GitStore) ReadPendingFullRotations(ctx context.Context) ([]PendingV2FullRotation, error) {
	state, err := s.readPendingFullRotationState(ctx)
	if err != nil {
		return nil, err
	}
	return state.Rotations, nil
}

func (s *V2GitStore) ClearPendingFullRotations(ctx context.Context) error {
	path, err := s.pendingFullRotationFilePath(ctx)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove pending v2 full rotations: %w", err)
	}
	return nil
}

func (s *V2GitStore) readPendingFullRotationState(ctx context.Context) (pendingV2FullRotationState, error) {
	path, err := s.pendingFullRotationFilePath(ctx)
	if err != nil {
		return pendingV2FullRotationState{}, err
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is under git common dir
	if os.IsNotExist(err) {
		return pendingV2FullRotationState{Version: pendingV2FullRotationVersion}, nil
	}
	if err != nil {
		return pendingV2FullRotationState{}, fmt.Errorf("read pending v2 full rotations: %w", err)
	}

	var state pendingV2FullRotationState
	if err := json.Unmarshal(data, &state); err != nil {
		return pendingV2FullRotationState{}, fmt.Errorf("parse pending v2 full rotations: %w", err)
	}
	if state.Version != pendingV2FullRotationVersion {
		return pendingV2FullRotationState{}, fmt.Errorf("unsupported pending v2 full rotation version %d", state.Version)
	}
	return state, nil
}

func (s *V2GitStore) writePendingFullRotationState(ctx context.Context, state pendingV2FullRotationState) error {
	path, err := s.pendingFullRotationFilePath(ctx)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create pending v2 full rotation dir: %w", err)
	}

	data, err := jsonutil.MarshalIndentWithNewline(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pending v2 full rotations: %w", err)
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(path), pendingV2FullRotationFile+".*.tmp")
	if err != nil {
		return fmt.Errorf("create pending v2 full rotation temp file: %w", err)
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
		return fmt.Errorf("write pending v2 full rotations: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close pending v2 full rotations: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace pending v2 full rotations: %w", err)
	}
	removeTmp = false
	return nil
}

func (s *V2GitStore) pendingFullRotationFilePath(ctx context.Context) (string, error) {
	commonDir, err := s.gitCommonDir(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(commonDir, pendingV2FullRotationDirName, pendingV2FullRotationFile), nil
}

func (s *V2GitStore) gitCommonDir(ctx context.Context) (string, error) {
	worktree, err := s.repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("open worktree for pending v2 full rotations: %w", err)
	}
	root := worktree.Filesystem.Root()
	if root == "" {
		return "", errors.New("resolve worktree root for pending v2 full rotations")
	}

	cmd := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--git-common-dir")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("resolve git common dir for pending v2 full rotations: %w", err)
	}
	commonDir := strings.TrimSpace(string(output))
	if commonDir == "" {
		return "", errors.New("resolve git common dir for pending v2 full rotations: empty output")
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(root, commonDir)
	}
	return filepath.Clean(commonDir), nil
}
