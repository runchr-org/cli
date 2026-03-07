package trail

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v6"
)

// initTestRepo creates a test git repository with an initial commit.
func initTestRepo(t *testing.T) *git.Repository {
	t.Helper()

	dir := t.TempDir()

	ctx := context.Background()
	cmds := [][]string{
		{"git", "init", dir},
		{"git", "-C", dir, "config", "user.name", "Test"},
		{"git", "-C", dir, "config", "user.email", "test@test.com"},
	}
	for _, args := range cmds {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}

	// Create a file and commit
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test"), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	commitCmds := [][]string{
		{"git", "-C", dir, "add", "."},
		{"git", "-C", dir, "commit", "-m", "Initial commit"},
	}
	for _, args := range commitCmds {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}

	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatalf("failed to open repo: %v", err)
	}
	return repo
}

func TestStore_EnsureBranch(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewStore(repo)

	// First call should create the branch
	if err := store.EnsureBranch(); err != nil {
		t.Fatalf("EnsureBranch() error = %v", err)
	}

	// Second call should be idempotent
	if err := store.EnsureBranch(); err != nil {
		t.Fatalf("EnsureBranch() second call error = %v", err)
	}
}

func TestStore_WriteAndRead(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewStore(repo)

	trailID, err := GenerateID()
	if err != nil {
		t.Fatalf("GenerateID() error = %v", err)
	}

	now := time.Now().Truncate(time.Second)
	metadata := &Metadata{
		TrailID:   trailID,
		Branch:    "feature/test",
		Base:      "main",
		Title:     "Test trail",
		Body:      "A test trail",
		Status:    StatusDraft,
		Author:    "tester",
		Assignees: []string{},
		Labels:    []string{"test"},
		CreatedAt: now,
		UpdatedAt: now,
	}

	discussion := &Discussion{Comments: []Comment{}}

	if err := store.Write(metadata, discussion, nil, nil); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	// Read it back
	gotMeta, gotDisc, _, _, err := store.Read(trailID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if gotMeta.TrailID != trailID {
		t.Errorf("Read() trail_id = %s, want %s", gotMeta.TrailID, trailID)
	}
	if gotMeta.Branch != "feature/test" {
		t.Errorf("Read() branch = %q, want %q", gotMeta.Branch, "feature/test")
	}
	if gotMeta.Title != "Test trail" {
		t.Errorf("Read() title = %q, want %q", gotMeta.Title, "Test trail")
	}
	if gotMeta.Status != StatusDraft {
		t.Errorf("Read() status = %q, want %q", gotMeta.Status, StatusDraft)
	}
	if len(gotMeta.Labels) != 1 || gotMeta.Labels[0] != "test" {
		t.Errorf("Read() labels = %v, want [test]", gotMeta.Labels)
	}
	if gotDisc == nil {
		t.Error("Read() discussion should not be nil")
	}
}

func TestStore_FindByBranch(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewStore(repo)

	now := time.Now()

	// Create two trails for different branches
	for _, branch := range []string{"feature/a", "feature/b"} {
		id, err := GenerateID()
		if err != nil {
			t.Fatalf("GenerateID() error = %v", err)
		}
		meta := &Metadata{
			TrailID:   id,
			Branch:    branch,
			Base:      "main",
			Title:     HumanizeBranchName(branch),
			Status:    StatusDraft,
			Author:    "test",
			Assignees: []string{},
			Labels:    []string{},
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := store.Write(meta, nil, nil, nil); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}

	// Find by branch
	found, entry, err := store.FindByBranch("feature/a")
	if err != nil {
		t.Fatalf("FindByBranch() error = %v", err)
	}
	if found == nil {
		t.Fatal("FindByBranch() returned nil, expected trail")
	}
	if found.Branch != "feature/a" {
		t.Errorf("FindByBranch() branch = %q, want %q", found.Branch, "feature/a")
	}
	// Legacy single-branch trails return nil entry
	if entry != nil {
		t.Error("FindByBranch() should return nil entry for legacy trail")
	}

	// Not found
	notFound, notFoundEntry, err := store.FindByBranch("feature/c")
	if err != nil {
		t.Fatalf("FindByBranch() error = %v", err)
	}
	if notFound != nil {
		t.Error("FindByBranch() should return nil for non-existent branch")
	}
	if notFoundEntry != nil {
		t.Error("FindByBranch() should return nil entry for non-existent branch")
	}
}

func TestStore_List(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewStore(repo)

	// List when no trails exist (branch doesn't exist yet)
	trails, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if trails != nil {
		t.Errorf("List() = %v, want nil for empty store", trails)
	}

	// Create a trail
	now := time.Now()
	id, err := GenerateID()
	if err != nil {
		t.Fatalf("GenerateID() error = %v", err)
	}
	meta := &Metadata{
		TrailID:   id,
		Branch:    "feature/test",
		Base:      "main",
		Title:     "Test",
		Status:    StatusDraft,
		Author:    "test",
		Assignees: []string{},
		Labels:    []string{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Write(meta, nil, nil, nil); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	trails, err = store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(trails) != 1 {
		t.Fatalf("List() returned %d trails, want 1", len(trails))
	}
	if trails[0].TrailID != id {
		t.Errorf("List()[0].TrailID = %s, want %s", trails[0].TrailID, id)
	}
}

func TestStore_Update(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewStore(repo)

	now := time.Now()
	id, err := GenerateID()
	if err != nil {
		t.Fatalf("GenerateID() error = %v", err)
	}
	meta := &Metadata{
		TrailID:   id,
		Branch:    "feature/test",
		Base:      "main",
		Title:     "Original",
		Status:    StatusDraft,
		Author:    "test",
		Assignees: []string{},
		Labels:    []string{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Write(meta, nil, nil, nil); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	// Update
	if err := store.Update(id, func(m *Metadata) {
		m.Title = "Updated"
		m.Status = StatusActive
		m.Labels = []string{"urgent"}
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Verify
	updated, _, _, _, err := store.Read(id)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if updated.Title != "Updated" {
		t.Errorf("Read() title = %q, want %q", updated.Title, "Updated")
	}
	if updated.Status != StatusActive {
		t.Errorf("Read() status = %q, want %q", updated.Status, StatusActive)
	}
	if len(updated.Labels) != 1 || updated.Labels[0] != "urgent" {
		t.Errorf("Read() labels = %v, want [urgent]", updated.Labels)
	}
	if !updated.UpdatedAt.After(now) {
		t.Error("Read() updated_at should be after original")
	}
}

func TestStore_Delete(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewStore(repo)

	now := time.Now()
	id, err := GenerateID()
	if err != nil {
		t.Fatalf("GenerateID() error = %v", err)
	}
	meta := &Metadata{
		TrailID:   id,
		Branch:    "feature/test",
		Base:      "main",
		Title:     "To delete",
		Status:    StatusDraft,
		Author:    "test",
		Assignees: []string{},
		Labels:    []string{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.Write(meta, nil, nil, nil); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	// Delete
	if err := store.Delete(id); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Verify it's gone
	_, _, _, _, err = store.Read(id)
	if err == nil {
		t.Error("Read() should fail after delete")
	}
}

func TestStore_ReadNonExistent(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewStore(repo)

	if err := store.EnsureBranch(); err != nil {
		t.Fatalf("EnsureBranch() error = %v", err)
	}

	_, _, _, _, err := store.Read(ID("abcdef123456"))
	if err == nil {
		t.Error("Read() should fail for non-existent trail")
	}
}

func TestStore_ReadInvalidID(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewStore(repo)

	// Invalid format: too short
	_, _, _, _, err := store.Read(ID("abc"))
	if err == nil {
		t.Error("Read() should fail for invalid trail ID")
	}

	// Path traversal attempt
	_, _, _, _, err = store.Read(ID("../../etc/pass"))
	if err == nil {
		t.Error("Read() should fail for path traversal ID")
	}
}

func TestStore_DeleteInvalidID(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewStore(repo)

	// Invalid format: uppercase hex
	err := store.Delete(ID("ABCDEF123456"))
	if err == nil {
		t.Error("Delete() should fail for invalid trail ID")
	}

	// Path traversal attempt
	err = store.Delete(ID("../../../etc"))
	if err == nil {
		t.Error("Delete() should fail for path traversal ID")
	}
}

func TestStore_AddCheckpointPreservesOtherFields(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewStore(repo)

	trailID, err := GenerateID()
	if err != nil {
		t.Fatalf("GenerateID() error = %v", err)
	}

	now := time.Now().Truncate(time.Second)
	metadata := &Metadata{
		TrailID:   trailID,
		Branch:    "feature/preserve",
		Base:      "main",
		Title:     "Preservation test",
		Body:      "Verify AddCheckpoint doesn't corrupt other fields",
		Status:    StatusActive,
		Author:    "tester",
		Assignees: []string{"alice"},
		Labels:    []string{"important"},
		CreatedAt: now,
		UpdatedAt: now,
	}
	discussion := &Discussion{Comments: []Comment{
		{ID: "c1", Author: "bob", Body: "looks good", CreatedAt: now},
	}}

	if err := store.Write(metadata, discussion, nil, nil); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	// Add a checkpoint
	firstSummary := "first checkpoint"
	cpRef := CheckpointRef{
		CheckpointID: "aabbccddeeff",
		CommitSHA:    "deadbeef1234",
		CreatedAt:    now,
		Summary:      &firstSummary,
	}
	if err := store.AddCheckpoint(trailID, cpRef); err != nil {
		t.Fatalf("AddCheckpoint() error = %v", err)
	}

	// Read back and verify metadata + discussion are unchanged
	gotMeta, gotDisc, gotCPs, _, err := store.Read(trailID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	// Metadata unchanged
	if gotMeta.Title != "Preservation test" {
		t.Errorf("metadata title changed: got %q, want %q", gotMeta.Title, "Preservation test")
	}
	if gotMeta.Body != "Verify AddCheckpoint doesn't corrupt other fields" {
		t.Errorf("metadata body changed: got %q", gotMeta.Body)
	}
	if gotMeta.Status != StatusActive {
		t.Errorf("metadata status changed: got %q, want %q", gotMeta.Status, StatusActive)
	}
	if len(gotMeta.Assignees) != 1 || gotMeta.Assignees[0] != "alice" {
		t.Errorf("metadata assignees changed: got %v", gotMeta.Assignees)
	}
	if len(gotMeta.Labels) != 1 || gotMeta.Labels[0] != "important" {
		t.Errorf("metadata labels changed: got %v", gotMeta.Labels)
	}

	// Discussion unchanged
	if len(gotDisc.Comments) != 1 {
		t.Fatalf("discussion comments count = %d, want 1", len(gotDisc.Comments))
	}
	if gotDisc.Comments[0].ID != "c1" || gotDisc.Comments[0].Body != "looks good" {
		t.Errorf("discussion comment changed: got %+v", gotDisc.Comments[0])
	}

	// Checkpoint added correctly
	if len(gotCPs.Checkpoints) != 1 {
		t.Fatalf("checkpoints count = %d, want 1", len(gotCPs.Checkpoints))
	}
	if gotCPs.Checkpoints[0].CheckpointID != "aabbccddeeff" {
		t.Errorf("checkpoint ID = %q, want %q", gotCPs.Checkpoints[0].CheckpointID, "aabbccddeeff")
	}

	// Add a second checkpoint — should prepend
	secondSummary := "second checkpoint"
	cpRef2 := CheckpointRef{
		CheckpointID: "112233445566",
		CommitSHA:    "cafebabe5678",
		CreatedAt:    now,
		Summary:      &secondSummary,
	}
	if err := store.AddCheckpoint(trailID, cpRef2); err != nil {
		t.Fatalf("AddCheckpoint() second call error = %v", err)
	}

	_, _, gotCPs2, _, err := store.Read(trailID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(gotCPs2.Checkpoints) != 2 {
		t.Fatalf("checkpoints count = %d, want 2", len(gotCPs2.Checkpoints))
	}
	if gotCPs2.Checkpoints[0].CheckpointID != "112233445566" {
		t.Errorf("newest checkpoint should be first, got %q", gotCPs2.Checkpoints[0].CheckpointID)
	}
	if gotCPs2.Checkpoints[1].CheckpointID != "aabbccddeeff" {
		t.Errorf("older checkpoint should be second, got %q", gotCPs2.Checkpoints[1].CheckpointID)
	}
}

func TestStore_WriteAndReadVerification(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewStore(repo)

	trailID, err := GenerateID()
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Truncate(time.Second)
	metadata := &Metadata{
		TrailID:   trailID,
		Title:     "Test verification",
		Status:    StatusActive,
		Author:    "tester",
		Assignees: []string{},
		Labels:    []string{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	verification := &Verification{
		Events: []VerificationEvent{
			{Kind: "pr_checks", Status: "pass", Timestamp: now, BranchID: "uuid-1"},
			{Kind: "trail_review", Status: "requested", Timestamp: now},
		},
	}

	if err := store.Write(metadata, nil, nil, verification); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	_, _, _, gotVerification, err := store.Read(trailID)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	if len(gotVerification.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(gotVerification.Events))
	}
	if gotVerification.Events[0].Kind != "pr_checks" {
		t.Errorf("expected pr_checks, got %s", gotVerification.Events[0].Kind)
	}
	if gotVerification.Events[0].Status != "pass" {
		t.Errorf("expected pass, got %s", gotVerification.Events[0].Status)
	}
	if gotVerification.Events[0].BranchID != "uuid-1" {
		t.Errorf("expected branch_id uuid-1, got %s", gotVerification.Events[0].BranchID)
	}
	if gotVerification.Events[1].Kind != "trail_review" {
		t.Errorf("expected trail_review, got %s", gotVerification.Events[1].Kind)
	}
}

func TestStore_ReadVerificationBackwardCompat(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewStore(repo)

	trailID, err := GenerateID()
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Truncate(time.Second)
	metadata := &Metadata{
		TrailID:   trailID,
		Title:     "No verification",
		Status:    StatusDraft,
		Author:    "tester",
		Assignees: []string{},
		Labels:    []string{},
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Write without verification (nil)
	if err := store.Write(metadata, nil, nil, nil); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// Read should return empty verification, not nil
	_, _, _, gotVerification, err := store.Read(trailID)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if gotVerification == nil {
		t.Fatal("expected non-nil verification")
	}
	if len(gotVerification.Events) != 0 {
		t.Errorf("expected 0 events, got %d", len(gotVerification.Events))
	}
}

func TestStore_UpdatePreservesVerification(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewStore(repo)

	trailID, err := GenerateID()
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Truncate(time.Second)
	metadata := &Metadata{
		TrailID:   trailID,
		Title:     "Update preserves verification",
		Status:    StatusActive,
		Author:    "tester",
		Assignees: []string{},
		Labels:    []string{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	verification := &Verification{
		Events: []VerificationEvent{
			{Kind: "pr_checks", Status: "pass", Timestamp: now},
		},
	}

	if err := store.Write(metadata, nil, nil, verification); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// Update metadata only — verification should be preserved
	if err := store.Update(trailID, func(m *Metadata) {
		m.Title = "Updated title"
	}); err != nil {
		t.Fatalf("update failed: %v", err)
	}

	_, _, _, gotVerification, err := store.Read(trailID)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if len(gotVerification.Events) != 1 {
		t.Fatalf("expected 1 event after update, got %d", len(gotVerification.Events))
	}
	if gotVerification.Events[0].Kind != "pr_checks" {
		t.Errorf("expected pr_checks, got %s", gotVerification.Events[0].Kind)
	}
}

func TestStore_FindByBranch_MultiBranch(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewStore(repo)

	trailID, err := GenerateID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)

	metadata := &Metadata{
		TrailID:   trailID,
		Title:     "Multi-branch trail",
		Status:    StatusActive,
		Author:    "tester",
		Assignees: []string{},
		Labels:    []string{},
		CreatedAt: now,
		UpdatedAt: now,
		Branches: []BranchEntry{
			{ID: "uuid-1", Name: "feature/auth-core", BaseBranch: "main", Status: BranchOpen, AddedAt: now},
			{ID: "uuid-2", Name: "feature/auth-api", BaseBranch: "feature/auth-core", Status: BranchOpen, AddedAt: now},
		},
	}

	if err := store.Write(metadata, nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	// Find by first branch
	found, entry, err := store.FindByBranch("feature/auth-core")
	if err != nil {
		t.Fatal(err)
	}
	if found == nil {
		t.Fatal("expected to find trail")
	}
	if entry == nil || entry.ID != "uuid-1" {
		t.Error("expected branch entry uuid-1")
	}

	// Find by second branch
	found, entry, err = store.FindByBranch("feature/auth-api")
	if err != nil {
		t.Fatal(err)
	}
	if found == nil {
		t.Fatal("expected to find trail")
	}
	if entry == nil || entry.ID != "uuid-2" {
		t.Error("expected branch entry uuid-2")
	}

	// Not found
	found, entry, err = store.FindByBranch("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if found != nil || entry != nil {
		t.Error("expected nil for nonexistent branch")
	}
}

func TestStore_AddCheckpointWithBranchID(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewStore(repo)

	trailID, err := GenerateID()
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Truncate(time.Second)

	metadata := &Metadata{
		TrailID:   trailID,
		Title:     "Multi-branch",
		Status:    StatusActive,
		Author:    "tester",
		Assignees: []string{},
		Labels:    []string{},
		CreatedAt: now,
		UpdatedAt: now,
		Branches: []BranchEntry{
			{ID: "branch-uuid-1", Name: "feature/core", BaseBranch: "main", Status: BranchOpen, AddedAt: now},
		},
	}

	if err := store.Write(metadata, nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	summary := "initial implementation"
	ref := CheckpointRef{
		CheckpointID: "aabbccddeeff",
		CommitSHA:    "1234567890ab",
		CreatedAt:    now,
		Summary:      &summary,
		BranchID:     "branch-uuid-1",
	}

	if err := store.AddCheckpoint(trailID, ref); err != nil {
		t.Fatal(err)
	}

	_, _, checkpoints, _, err := store.Read(trailID)
	if err != nil {
		t.Fatal(err)
	}

	if len(checkpoints.Checkpoints) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(checkpoints.Checkpoints))
	}
	if checkpoints.Checkpoints[0].BranchID != "branch-uuid-1" {
		t.Errorf("expected branch ID branch-uuid-1, got %s", checkpoints.Checkpoints[0].BranchID)
	}
}

func TestStore_FindByBranch_LegacyFallback(t *testing.T) {
	t.Parallel()
	repo := initTestRepo(t)
	store := NewStore(repo)

	trailID, err := GenerateID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)

	// Legacy trail with Branch field, no Branches[]
	metadata := &Metadata{
		TrailID:   trailID,
		Branch:    "feature/old-style",
		Base:      "main",
		Title:     "Legacy trail",
		Status:    StatusDraft,
		Author:    "tester",
		Assignees: []string{},
		Labels:    []string{},
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := store.Write(metadata, nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	found, entry, err := store.FindByBranch("feature/old-style")
	if err != nil {
		t.Fatal(err)
	}
	if found == nil {
		t.Fatal("expected to find legacy trail")
	}
	// entry is nil for legacy trails (no BranchEntry to return)
	if entry != nil {
		t.Error("expected nil entry for legacy trail")
	}
}
