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

	// Token usage — individual keys for gmeta queryability
	if opts.TokenUsage != nil {
		writeTokenUsageEntries(s, sessionPath+"token-usage/", opts.TokenUsage, entries)
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

// writeTokenUsageEntries writes token usage as individual gmeta string values.
// Layout: <prefix>input/__value, <prefix>output/__value, etc.
func writeTokenUsageEntries(s *GmetaStore, prefix string, usage *agent.TokenUsage, entries map[string]object.TreeEntry) {
	if usage.InputTokens > 0 {
		s.writeStringValue(prefix+"input/__value", strconv.Itoa(usage.InputTokens), entries)
	}
	if usage.OutputTokens > 0 {
		s.writeStringValue(prefix+"output/__value", strconv.Itoa(usage.OutputTokens), entries)
	}
	if usage.CacheReadTokens > 0 {
		s.writeStringValue(prefix+"cache-read/__value", strconv.Itoa(usage.CacheReadTokens), entries)
	}
	if usage.CacheCreationTokens > 0 {
		s.writeStringValue(prefix+"cache-creation/__value", strconv.Itoa(usage.CacheCreationTokens), entries)
	}
	if usage.APICallCount > 0 {
		s.writeStringValue(prefix+"api-calls/__value", strconv.Itoa(usage.APICallCount), entries)
	}
	// Total for convenient querying
	total := usage.InputTokens + usage.OutputTokens + usage.CacheReadTokens + usage.CacheCreationTokens
	if total > 0 {
		s.writeStringValue(prefix+"total/__value", strconv.Itoa(total), entries)
	}
}

// readTokenUsageFromTree reads token usage from gmeta string values in a tree.
func readTokenUsageFromTree(repo *git.Repository, sessionTree *object.Tree) *agent.TokenUsage {
	usageTree, err := sessionTree.Tree("token-usage")
	if err != nil {
		return nil
	}

	usage := &agent.TokenUsage{
		InputTokens:         readGmetaIntValue(repo, usageTree, "input"),
		OutputTokens:        readGmetaIntValue(repo, usageTree, "output"),
		CacheReadTokens:     readGmetaIntValue(repo, usageTree, "cache-read"),
		CacheCreationTokens: readGmetaIntValue(repo, usageTree, "cache-creation"),
		APICallCount:        readGmetaIntValue(repo, usageTree, "api-calls"),
	}

	// Return nil if all zeros (no usage data)
	if usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.CacheReadTokens == 0 && usage.CacheCreationTokens == 0 {
		return nil
	}
	return usage
}

// readGmetaIntValue reads a string value from a tree and parses it as an int.
// Returns 0 if the value doesn't exist or can't be parsed.
func readGmetaIntValue(repo *git.Repository, tree *object.Tree, key string) int {
	v := readGmetaStringValue(repo, tree, key)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
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

// --- Read methods ---

// ReadCommitted reads a checkpoint summary from the gmeta tree.
// Returns nil, nil if the checkpoint doesn't exist.
func (s *GmetaStore) ReadCommitted(ctx context.Context, checkpointID id.CheckpointID) (*CheckpointSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err //nolint:wrapcheck // Propagating context cancellation
	}
	if checkpointID.IsEmpty() {
		return nil, nil //nolint:nilnil // Empty ID means no checkpoint
	}

	refName := plumbing.ReferenceName(GmetaRefName)
	ref, err := s.repo.Reference(refName, true)
	if err != nil {
		return nil, nil //nolint:nilnil,nilerr // Ref doesn't exist means no checkpoint
	}

	commit, err := s.repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, nil //nolint:nilnil,nilerr // Invalid ref
	}

	tree, err := commit.Tree()
	if err != nil {
		return nil, nil //nolint:nilnil,nilerr // Invalid tree
	}

	targetPath := gmetaTargetPath(checkpointID)
	targetTree, err := tree.Tree(targetPath)
	if err != nil {
		return nil, nil //nolint:nilnil,nilerr // Checkpoint doesn't exist
	}

	summary := &CheckpointSummary{
		CheckpointID: checkpointID,
	}

	// Read checkpoint-level fields from entire/
	if entireTree, treeErr := targetTree.Tree("entire"); treeErr == nil {
		summary.Strategy = readGmetaStringValue(s.repo, entireTree, "strategy")
		summary.CLIVersion = readGmetaStringValue(s.repo, entireTree, "cli-version")
		summary.Branch = readGmetaStringValue(s.repo, entireTree, "branch")
		if countStr := readGmetaStringValue(s.repo, entireTree, "checkpoints-count"); countStr != "" {
			if n, parseErr := strconv.Atoi(countStr); parseErr == nil {
				summary.CheckpointsCount = n
			}
		}
		summary.FilesTouched = readGmetaSetValues(s.repo, entireTree, "files-touched")
	}

	// Read session IDs from session/ids/__list/
	sessionIDs := s.readSessionIDList(targetTree)

	// Build sessions array and aggregate token usage
	summary.Sessions = make([]SessionFilePaths, len(sessionIDs))
	var aggregatedUsage *agent.TokenUsage
	for i, sid := range sessionIDs {
		summary.Sessions[i] = SessionFilePaths{
			Metadata: "/" + targetPath + "/session/" + sid + "/",
		}
		// Read per-session token usage for aggregation
		if sessionTree, sessionErr := targetTree.Tree("session/" + sid); sessionErr == nil {
			if usage := readTokenUsageFromTree(s.repo, sessionTree); usage != nil {
				if aggregatedUsage == nil {
					aggregatedUsage = &agent.TokenUsage{}
				}
				aggregatedUsage.InputTokens += usage.InputTokens
				aggregatedUsage.OutputTokens += usage.OutputTokens
				aggregatedUsage.CacheReadTokens += usage.CacheReadTokens
				aggregatedUsage.CacheCreationTokens += usage.CacheCreationTokens
				aggregatedUsage.APICallCount += usage.APICallCount
			}
		}
	}
	summary.TokenUsage = aggregatedUsage

	return summary, nil
}

// ReadSessionContent reads a session's transcript, prompt, and metadata from gmeta.
// sessionID is the session identifier (not an index).
// Returns ErrCheckpointNotFound if the checkpoint doesn't exist.
// Returns ErrNoTranscript if the session exists but has no transcript.
func (s *GmetaStore) ReadSessionContent(_ context.Context, checkpointID id.CheckpointID, sessionID string) (*SessionContent, error) {
	if checkpointID.IsEmpty() {
		return nil, ErrCheckpointNotFound
	}

	refName := plumbing.ReferenceName(GmetaRefName)
	ref, err := s.repo.Reference(refName, true)
	if err != nil {
		return nil, ErrCheckpointNotFound
	}

	commit, err := s.repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, ErrCheckpointNotFound
	}

	rootTree, err := commit.Tree()
	if err != nil {
		return nil, ErrCheckpointNotFound
	}

	targetPath := gmetaTargetPath(checkpointID)
	targetTree, err := rootTree.Tree(targetPath)
	if err != nil {
		return nil, ErrCheckpointNotFound
	}

	sessionPath := "session/" + sessionID
	sessionTree, err := targetTree.Tree(sessionPath)
	if err != nil {
		return nil, ErrCheckpointNotFound
	}

	result := &SessionContent{}

	// Read agent info
	result.Metadata.CheckpointID = checkpointID
	result.Metadata.SessionID = sessionID
	if agentTree, treeErr := sessionTree.Tree("agent"); treeErr == nil {
		agentName := readGmetaStringValue(s.repo, agentTree, "name")
		result.Metadata.Agent = types.AgentType(agentName)
		result.Metadata.Model = readGmetaStringValue(s.repo, agentTree, "model")
	}

	// Read token usage
	result.Metadata.TokenUsage = readTokenUsageFromTree(s.repo, sessionTree)

	// Read checkpoint-level fields into metadata
	if entireTree, treeErr := targetTree.Tree("entire"); treeErr == nil {
		result.Metadata.Strategy = readGmetaStringValue(s.repo, entireTree, "strategy")
		result.Metadata.Branch = readGmetaStringValue(s.repo, entireTree, "branch")
		result.Metadata.FilesTouched = readGmetaSetValues(s.repo, entireTree, "files-touched")
		if countStr := readGmetaStringValue(s.repo, entireTree, "checkpoints-count"); countStr != "" {
			if n, parseErr := strconv.Atoi(countStr); parseErr == nil {
				result.Metadata.CheckpointsCount = n
			}
		}
	}

	// Read prompt
	if promptValue := readGmetaStringValue(s.repo, sessionTree, "prompt"); promptValue != "" {
		result.Prompts = promptValue
	}

	// Read transcript from transcript/__list/
	result.Transcript = readGmetaListConcat(s.repo, sessionTree, "transcript")

	if len(result.Transcript) == 0 {
		return nil, ErrNoTranscript
	}

	return result, nil
}

// GetSessionLog retrieves the session transcript and session ID for a checkpoint.
// Reads the latest (last) session from the gmeta tree.
// Returns ErrCheckpointNotFound if the checkpoint doesn't exist.
// Returns ErrNoTranscript if the checkpoint exists but has no transcript.
func (s *GmetaStore) GetSessionLog(ctx context.Context, cpID id.CheckpointID) ([]byte, string, error) {
	summary, err := s.ReadCommitted(ctx, cpID)
	if err != nil {
		return nil, "", err
	}
	if summary == nil {
		return nil, "", ErrCheckpointNotFound
	}

	// Get session IDs to find the latest
	refName := plumbing.ReferenceName(GmetaRefName)
	ref, refErr := s.repo.Reference(refName, true)
	if refErr != nil {
		return nil, "", ErrCheckpointNotFound
	}
	commit, commitErr := s.repo.CommitObject(ref.Hash())
	if commitErr != nil {
		return nil, "", ErrCheckpointNotFound
	}
	rootTree, treeErr := commit.Tree()
	if treeErr != nil {
		return nil, "", ErrCheckpointNotFound
	}
	targetTree, targetErr := rootTree.Tree(gmetaTargetPath(cpID))
	if targetErr != nil {
		return nil, "", ErrCheckpointNotFound
	}

	sessionIDs := s.readSessionIDList(targetTree)
	if len(sessionIDs) == 0 {
		return nil, "", ErrCheckpointNotFound
	}

	latestSessionID := sessionIDs[len(sessionIDs)-1]
	content, err := s.ReadSessionContent(ctx, cpID, latestSessionID)
	if err != nil {
		return nil, "", err
	}
	return content.Transcript, latestSessionID, nil
}

// readSessionIDList reads ordered session IDs from session/ids/__list/.
func (s *GmetaStore) readSessionIDList(targetTree *object.Tree) []string {
	sessionTree, err := targetTree.Tree("session")
	if err != nil {
		return nil
	}
	return readGmetaListValues(s.repo, sessionTree, "ids")
}

// readGmetaStringValue reads a string value from <key>/__value in a tree.
func readGmetaStringValue(_ *git.Repository, tree *object.Tree, key string) string {
	valuePath := key + "/__value"
	file, err := tree.File(valuePath)
	if err != nil {
		return ""
	}
	content, err := file.Contents()
	if err != nil {
		return ""
	}
	return content
}

// readGmetaSetValues reads all values from <key>/__set/ in a tree.
func readGmetaSetValues(repo *git.Repository, tree *object.Tree, key string) []string {
	setPath := key + "/__set"
	setTree, err := tree.Tree(setPath)
	if err != nil {
		return nil
	}

	var values []string
	for _, entry := range setTree.Entries {
		if !entry.Mode.IsFile() {
			continue
		}
		blob, blobErr := repo.BlobObject(entry.Hash)
		if blobErr != nil {
			continue
		}
		reader, readerErr := blob.Reader()
		if readerErr != nil {
			continue
		}
		content := make([]byte, blob.Size)
		if _, readErr := reader.Read(content); readErr == nil {
			values = append(values, string(content))
		}
		_ = reader.Close()
	}
	return values
}

// readGmetaListValues reads all string values from <key>/__list/ in a tree, sorted by entry ID.
func readGmetaListValues(repo *git.Repository, tree *object.Tree, key string) []string {
	listPath := key + "/__list"
	listTree, err := tree.Tree(listPath)
	if err != nil {
		return nil
	}

	// Entries are already sorted by name (git tree convention), which for gmeta
	// list entry IDs (<timestamp-ms>-<hash>) gives chronological order.
	var values []string
	for _, entry := range listTree.Entries {
		if !entry.Mode.IsFile() {
			continue
		}
		blob, blobErr := repo.BlobObject(entry.Hash)
		if blobErr != nil {
			continue
		}
		reader, readerErr := blob.Reader()
		if readerErr != nil {
			continue
		}
		content := make([]byte, blob.Size)
		if _, readErr := reader.Read(content); readErr == nil {
			values = append(values, string(content))
		}
		_ = reader.Close()
	}
	return values
}

// readGmetaListConcat reads all blobs from <key>/__list/ and concatenates them.
// Used for transcript chunks that should be reassembled into a single byte slice.
func readGmetaListConcat(repo *git.Repository, tree *object.Tree, key string) []byte {
	listPath := key + "/__list"
	listTree, err := tree.Tree(listPath)
	if err != nil {
		return nil
	}

	var result []byte
	for _, entry := range listTree.Entries {
		if !entry.Mode.IsFile() {
			continue
		}
		blob, blobErr := repo.BlobObject(entry.Hash)
		if blobErr != nil {
			continue
		}
		reader, readerErr := blob.Reader()
		if readerErr != nil {
			continue
		}
		content := make([]byte, blob.Size)
		if _, readErr := reader.Read(content); readErr == nil {
			result = append(result, content...)
		}
		_ = reader.Close()
	}
	return result
}
