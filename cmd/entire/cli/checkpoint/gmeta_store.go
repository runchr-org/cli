package checkpoint

import (
	"context"
	"crypto/sha1" //nolint:gosec // SHA-1 used per gmeta spec for fanout/set keys, not for security
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/validation"
	"github.com/entireio/cli/cmd/entire/cli/versioninfo"
	"github.com/entireio/cli/redact"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/object"
)

// GmetaRefName is the local ref for gmeta exchange format metadata.
// Per gmeta spec, local metadata lives on refs/meta/local/main.
const GmetaRefName = "refs/meta/local/main"

// GmetaRemoteRefName is the ref name used on the remote server.
// Per gmeta spec, the remote stores metadata at refs/meta/main (no local/ prefix).
// Push refspec: refs/meta/local/main:refs/meta/main
const GmetaRemoteRefName = "refs/meta/main"

// GmetaStore provides checkpoint storage in gmeta exchange format.
// It writes metadata to refs/meta/local/main using the gmeta tree layout
// convention (change-id targets with string/__value, list/__list, set/__set).
//
// GmetaStore is non-authoritative — it's a third write alongside v1 and v2,
// proving interop with the gmeta Rust CLI.
type GmetaStore struct {
	repo *git.Repository
	gs   *GitStore // shared entry-building helpers
}

// NewGmetaStore creates a new gmeta checkpoint store backed by the given git repository.
func NewGmetaStore(repo *git.Repository) *GmetaStore {
	return &GmetaStore{
		repo: repo,
		gs:   &GitStore{repo: repo},
	}
}

// WriteCommitted writes or appends a session to a checkpoint in gmeta format.
// If the checkpoint already exists, the new session is added alongside existing ones.
// Handles both session and task checkpoints (including incremental).
func (s *GmetaStore) WriteCommitted(ctx context.Context, opts WriteCommittedOptions) error {
	if err := validateWriteOpts(opts); err != nil {
		return err
	}

	refName := plumbing.ReferenceName(GmetaRefName)
	if err := s.ensureRef(refName); err != nil {
		return fmt.Errorf("failed to ensure gmeta ref: %w", err)
	}

	parentHash, rootTreeHash, err := s.getRefState(refName)
	if err != nil {
		return err
	}

	targetPath := gmetaTargetPath(opts.CheckpointID)
	basePath := targetPath + "/"

	// Read existing entries at this target path
	entries, err := s.flattenTargetEntries(rootTreeHash, targetPath)
	if err != nil {
		return err
	}

	// Write checkpoint-level fields
	s.writeCheckpointFields(opts, basePath, entries)

	// Write session data
	sessionID := opts.SessionID
	sessionPath := basePath + "session/" + sessionID + "/"

	if opts.IsTask {
		if err := s.writeTaskEntries(ctx, opts, sessionPath, entries); err != nil {
			return err
		}
	} else {
		if err := s.writeSessionEntries(ctx, opts, sessionPath, entries); err != nil {
			return err
		}
	}

	// Add session ID to the ordered list (if not already present)
	s.addSessionIDToList(basePath, sessionID, entries)

	// Build tree and commit
	return s.commitEntries(refName, parentHash, rootTreeHash, targetPath, basePath, entries,
		fmt.Sprintf("Checkpoint: %s", opts.CheckpointID), opts.AuthorName, opts.AuthorEmail)
}

// UpdateCommitted replaces transcript and prompts for an existing session.
// Used at stop time to finalize with complete session data.
func (s *GmetaStore) UpdateCommitted(ctx context.Context, opts UpdateCommittedOptions) error {
	if opts.CheckpointID.IsEmpty() {
		return errors.New("invalid update options: checkpoint ID is required")
	}

	refName := plumbing.ReferenceName(GmetaRefName)
	parentHash, rootTreeHash, err := s.getRefState(refName)
	if err != nil {
		return ErrCheckpointNotFound
	}

	targetPath := gmetaTargetPath(opts.CheckpointID)
	basePath := targetPath + "/"
	sessionPath := basePath + "session/" + opts.SessionID + "/"

	entries, err := s.flattenTargetEntries(rootTreeHash, targetPath)
	if err != nil {
		return err
	}

	// Check that the checkpoint exists
	if len(entries) == 0 {
		return ErrCheckpointNotFound
	}

	// Replace transcript
	if len(opts.Transcript) > 0 {
		// Clear existing transcript list entries
		listPrefix := sessionPath + "transcript/__list/"
		for key := range entries {
			if strings.HasPrefix(key, listPrefix) {
				delete(entries, key)
			}
		}
		if err := s.writeTranscriptList(ctx, opts.Transcript, opts.Agent, sessionPath, entries); err != nil {
			return err
		}
	}

	// Replace prompt
	if len(opts.Prompts) > 0 {
		promptContent := redact.String(JoinPrompts(opts.Prompts))
		blobHash, err := CreateBlobFromContent(s.repo, []byte(promptContent))
		if err != nil {
			return fmt.Errorf("failed to create prompt blob: %w", err)
		}
		entries[sessionPath+"prompt/__value"] = object.TreeEntry{
			Name: sessionPath + "prompt/__value",
			Mode: filemode.Regular,
			Hash: blobHash,
		}
	}

	authorName, authorEmail := GetGitAuthorFromRepo(s.repo)
	return s.commitEntries(refName, parentHash, rootTreeHash, targetPath, basePath, entries,
		fmt.Sprintf("Finalize checkpoint: %s", opts.CheckpointID), authorName, authorEmail)
}

// gmetaTargetPath returns the gmeta tree base path for a checkpoint ID.
// Per gmeta spec: change-id/<sha1(checkpoint-id)[:2]>/<checkpoint-id>/
func gmetaTargetPath(cpID id.CheckpointID) string {
	fanout := gmetaFanout(string(cpID))
	return "change-id/" + fanout + "/" + string(cpID)
}

// gmetaFanout returns the first 2 hex chars of SHA-1(value).
// Per gmeta spec, change-id targets use SHA-1 hash of the value for fanout.
func gmetaFanout(value string) string {
	h := sha1.Sum([]byte(value)) //nolint:gosec // gmeta spec requires SHA-1 for fanout
	return fmt.Sprintf("%02x", h[0])
}

// gmetaListEntryID generates a list entry ID: <timestamp-ms>-<content-hash-prefix>.
// Per gmeta spec, list entries use this format for deterministic ordering.
func gmetaListEntryID(content []byte, offsetMs int) string {
	ts := time.Now().UnixMilli() + int64(offsetMs)
	h := sha1.Sum(content) //nolint:gosec // gmeta spec uses SHA-1 for content hash prefix
	return fmt.Sprintf("%d-%05x", ts, h[:3])
}

// gmetaSetEntryName returns the set entry filename: sha1(value)[:10].
func gmetaSetEntryName(value string) string {
	h := sha1.Sum([]byte(value)) //nolint:gosec // gmeta spec uses SHA-1 for set keys
	return fmt.Sprintf("%010x", h[:5])
}

// writeCheckpointFields writes checkpoint-level gmeta entries (strategy, cli-version, branch, etc.).
func (s *GmetaStore) writeCheckpointFields(opts WriteCommittedOptions, basePath string, entries map[string]object.TreeEntry) {
	entirePrefix := basePath + "entire/"

	// String values
	stringFields := map[string]string{
		"strategy":          opts.Strategy,
		"cli-version":       versioninfo.Version,
		"branch":            opts.Branch,
		"checkpoints-count": strconv.Itoa(opts.CheckpointsCount),
	}
	for key, value := range stringFields {
		if value == "" {
			continue
		}
		s.writeStringValue(entirePrefix+key+"/__value", value, entries)
	}

	// Set: files-touched
	if len(opts.FilesTouched) > 0 {
		setPrefix := entirePrefix + "files-touched/__set/"
		for _, file := range opts.FilesTouched {
			entryName := gmetaSetEntryName(file)
			path := setPrefix + entryName
			s.writeStringValue(path, file, entries)
		}
	}
}

// writeSessionEntries writes session-level gmeta entries (agent info, prompt, transcript).
func (s *GmetaStore) writeSessionEntries(ctx context.Context, opts WriteCommittedOptions, sessionPath string, entries map[string]object.TreeEntry) error {
	// Agent info
	if opts.Agent != "" {
		s.writeStringValue(sessionPath+"agent/name/__value", string(opts.Agent), entries)
	}
	if opts.Model != "" {
		s.writeStringValue(sessionPath+"agent/model/__value", opts.Model, entries)
	}

	// Prompt
	if len(opts.Prompts) > 0 {
		promptContent := redact.String(JoinPrompts(opts.Prompts))
		blobHash, err := CreateBlobFromContent(s.repo, []byte(promptContent))
		if err != nil {
			return fmt.Errorf("failed to create prompt blob: %w", err)
		}
		entries[sessionPath+"prompt/__value"] = object.TreeEntry{
			Name: sessionPath + "prompt/__value",
			Mode: filemode.Regular,
			Hash: blobHash,
		}
	}

	// Transcript
	transcript := opts.Transcript
	if len(transcript) == 0 && opts.TranscriptPath != "" {
		var readErr error
		transcript, readErr = os.ReadFile(opts.TranscriptPath)
		if readErr != nil {
			transcript = nil
		}
	}
	if len(transcript) > 0 {
		if err := s.writeTranscriptList(ctx, transcript, opts.Agent, sessionPath, entries); err != nil {
			return err
		}
	}

	return nil
}

// writeTaskEntries writes task checkpoint entries under session/<id>/task/<tool-use-id>/.
func (s *GmetaStore) writeTaskEntries(ctx context.Context, opts WriteCommittedOptions, sessionPath string, entries map[string]object.TreeEntry) error {
	if err := validation.ValidateToolUseID(opts.ToolUseID); err != nil {
		return fmt.Errorf("invalid tool use ID: %w", err)
	}

	taskPath := sessionPath + "task/" + opts.ToolUseID + "/"

	if opts.IsIncremental {
		// Incremental task checkpoint: append to incremental/__list/
		data := opts.IncrementalData
		redactedData, err := redact.JSONLBytes(data)
		if err != nil {
			redactedData = redact.Bytes(data)
		}

		entryID := gmetaListEntryID(redactedData, 0)
		entryPath := taskPath + "incremental/__list/" + entryID
		blobHash, err := CreateBlobFromContent(s.repo, redactedData)
		if err != nil {
			return fmt.Errorf("failed to create incremental blob: %w", err)
		}
		entries[entryPath] = object.TreeEntry{
			Name: entryPath,
			Mode: filemode.Regular,
			Hash: blobHash,
		}
		return nil
	}

	// Final task checkpoint
	if opts.AgentID != "" {
		s.writeStringValue(taskPath+"agent-id/__value", opts.AgentID, entries)
	}
	if opts.CheckpointUUID != "" {
		s.writeStringValue(taskPath+"checkpoint-uuid/__value", opts.CheckpointUUID, entries)
	}

	// Subagent transcript
	if opts.SubagentTranscriptPath != "" {
		transcriptData, err := os.ReadFile(opts.SubagentTranscriptPath)
		if err != nil {
			logging.Warn(ctx, "gmeta: failed to read subagent transcript",
				slog.String("path", opts.SubagentTranscriptPath),
				slog.String("error", err.Error()),
			)
		} else if len(transcriptData) > 0 {
			redacted, redactErr := redact.JSONLBytes(transcriptData)
			if redactErr != nil {
				redacted = redact.Bytes(transcriptData)
			}

			chunks, chunkErr := agent.ChunkTranscript(ctx, redacted, opts.Agent)
			if chunkErr != nil {
				return fmt.Errorf("failed to chunk subagent transcript: %w", chunkErr)
			}
			for i, chunk := range chunks {
				entryID := gmetaListEntryID(chunk, i)
				entryPath := taskPath + "transcript/__list/" + entryID
				blobHash, err := CreateBlobFromContent(s.repo, chunk)
				if err != nil {
					return fmt.Errorf("failed to create transcript chunk blob: %w", err)
				}
				entries[entryPath] = object.TreeEntry{
					Name: entryPath,
					Mode: filemode.Regular,
					Hash: blobHash,
				}
			}
		}
	}

	return nil
}

// writeTranscriptList writes redacted, chunked transcript as gmeta list entries.
func (s *GmetaStore) writeTranscriptList(ctx context.Context, transcript []byte, agentType types.AgentType, sessionPath string, entries map[string]object.TreeEntry) error {
	redacted, err := redact.JSONLBytes(transcript)
	if err != nil {
		return fmt.Errorf("failed to redact transcript: %w", err)
	}

	chunks, err := agent.ChunkTranscript(ctx, redacted, agentType)
	if err != nil {
		return fmt.Errorf("failed to chunk transcript: %w", err)
	}

	listPrefix := sessionPath + "transcript/__list/"
	for i, chunk := range chunks {
		entryID := gmetaListEntryID(chunk, i)
		entryPath := listPrefix + entryID
		blobHash, err := CreateBlobFromContent(s.repo, chunk)
		if err != nil {
			return fmt.Errorf("failed to create transcript chunk blob: %w", err)
		}
		entries[entryPath] = object.TreeEntry{
			Name: entryPath,
			Mode: filemode.Regular,
			Hash: blobHash,
		}
	}

	return nil
}

// addSessionIDToList adds a session ID to the session/ids/__list/ if not already present.
func (s *GmetaStore) addSessionIDToList(basePath, sessionID string, entries map[string]object.TreeEntry) {
	listPrefix := basePath + "session/ids/__list/"

	// Check if session ID is already in the list by scanning existing entries
	for key, entry := range entries {
		if strings.HasPrefix(key, listPrefix) {
			// Read blob to check if it matches
			blob, err := s.repo.BlobObject(entry.Hash)
			if err == nil {
				reader, err := blob.Reader()
				if err == nil {
					content := make([]byte, blob.Size)
					if _, readErr := reader.Read(content); readErr == nil {
						if string(content) == sessionID {
							_ = reader.Close()
							return // Already present
						}
					}
					_ = reader.Close()
				}
			}
		}
	}

	// Add new entry
	entryID := gmetaListEntryID([]byte(sessionID), 0)
	entryPath := listPrefix + entryID
	blobHash, err := CreateBlobFromContent(s.repo, []byte(sessionID))
	if err != nil {
		return // Best-effort
	}
	entries[entryPath] = object.TreeEntry{
		Name: entryPath,
		Mode: filemode.Regular,
		Hash: blobHash,
	}
}

// writeStringValue is a helper that creates a blob and adds a tree entry.
func (s *GmetaStore) writeStringValue(path, value string, entries map[string]object.TreeEntry) {
	blobHash, err := CreateBlobFromContent(s.repo, []byte(value))
	if err != nil {
		return // Best-effort; caller logs warning
	}
	entries[path] = object.TreeEntry{
		Name: path,
		Mode: filemode.Regular,
		Hash: blobHash,
	}
}

// ensureRef ensures that a ref exists, creating an orphan commit with empty tree if not.
func (s *GmetaStore) ensureRef(refName plumbing.ReferenceName) error {
	_, err := s.repo.Reference(refName, true)
	if err == nil {
		return nil
	}

	emptyTreeHash, err := BuildTreeFromEntries(s.repo, make(map[string]object.TreeEntry))
	if err != nil {
		return fmt.Errorf("failed to build empty tree: %w", err)
	}

	authorName, authorEmail := GetGitAuthorFromRepo(s.repo)
	commitHash, err := CreateCommit(s.repo, emptyTreeHash, plumbing.ZeroHash, "Initialize gmeta ref", authorName, authorEmail)
	if err != nil {
		return fmt.Errorf("failed to create initial commit: %w", err)
	}

	ref := plumbing.NewHashReference(refName, commitHash)
	if err := s.repo.Storer.SetReference(ref); err != nil {
		return fmt.Errorf("failed to set gmeta ref: %w", err)
	}
	return nil
}

// getRefState returns the parent commit hash and root tree hash for a ref.
func (s *GmetaStore) getRefState(refName plumbing.ReferenceName) (parentHash, treeHash plumbing.Hash, err error) {
	ref, err := s.repo.Reference(refName, true)
	if err != nil {
		return plumbing.ZeroHash, plumbing.ZeroHash, fmt.Errorf("ref %s not found: %w", refName, err)
	}

	commit, err := s.repo.CommitObject(ref.Hash())
	if err != nil {
		return plumbing.ZeroHash, plumbing.ZeroHash, fmt.Errorf("failed to get commit: %w", err)
	}

	return ref.Hash(), commit.TreeHash, nil
}

// flattenTargetEntries reads entries under a gmeta target path from the root tree.
func (s *GmetaStore) flattenTargetEntries(rootTreeHash plumbing.Hash, targetPath string) (map[string]object.TreeEntry, error) {
	entries := make(map[string]object.TreeEntry)
	if rootTreeHash == plumbing.ZeroHash {
		return entries, nil
	}

	rootTree, err := s.repo.TreeObject(rootTreeHash)
	if err != nil {
		return entries, nil //nolint:nilerr // Tree doesn't exist yet
	}

	subtree, err := rootTree.Tree(targetPath)
	if err != nil {
		return entries, nil //nolint:nilerr // Target doesn't exist yet
	}

	if err := FlattenTree(s.repo, subtree, targetPath, entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// commitEntries builds a tree from entries, splices it into the root, and commits.
// targetPath is like "change-id/a3/a3b2c4d5e6f7" (no trailing slash).
// basePath is targetPath + "/" (with trailing slash).
func (s *GmetaStore) commitEntries(refName plumbing.ReferenceName, parentHash, rootTreeHash plumbing.Hash, targetPath, basePath string, entries map[string]object.TreeEntry, message, authorName, authorEmail string) error {
	// Convert entries to relative paths (strip basePath prefix)
	relEntries := make(map[string]object.TreeEntry, len(entries))
	for path, entry := range entries {
		relPath := strings.TrimPrefix(path, basePath)
		if relPath == path {
			continue
		}
		relEntries[relPath] = entry
	}

	// Build the target subtree from relative entries
	targetTreeHash, err := BuildTreeFromEntries(s.repo, relEntries)
	if err != nil {
		return fmt.Errorf("failed to build gmeta subtree: %w", err)
	}

	// Splice into root tree using tree surgery.
	// targetPath = "change-id/<fanout>/<checkpoint-id>"
	// We splice at ["change-id", "<fanout>"] with the checkpoint-id entry.
	segments := strings.Split(targetPath, "/")
	if len(segments) < 3 {
		return fmt.Errorf("invalid gmeta target path: %s", targetPath)
	}
	// Path segments for UpdateSubtree: all but the last segment
	parentSegments := segments[:len(segments)-1]
	leafName := segments[len(segments)-1]

	newRootHash, err := UpdateSubtree(s.repo, rootTreeHash, parentSegments, []object.TreeEntry{
		{Name: leafName, Mode: filemode.Dir, Hash: targetTreeHash},
	}, UpdateSubtreeOptions{MergeMode: MergeKeepExisting})
	if err != nil {
		return fmt.Errorf("failed to splice gmeta subtree: %w", err)
	}

	// Commit
	if authorName == "" || authorEmail == "" {
		authorName, authorEmail = GetGitAuthorFromRepo(s.repo)
	}
	commitHash, err := CreateCommit(s.repo, newRootHash, parentHash, message, authorName, authorEmail)
	if err != nil {
		return fmt.Errorf("failed to create gmeta commit: %w", err)
	}

	ref := plumbing.NewHashReference(refName, commitHash)
	if err := s.repo.Storer.SetReference(ref); err != nil {
		return fmt.Errorf("failed to update gmeta ref: %w", err)
	}
	return nil
}
