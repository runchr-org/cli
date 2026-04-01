package mcpserver

import (
	"context"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/memoryloop"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

func TestNewServer_RegistersTools(t *testing.T) {
	t.Parallel()
	s := NewServer()
	if s == nil {
		t.Fatal("NewServer() returned nil")
	}
}

func setupTestRepo(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "f.txt", "init")
	testutil.GitAdd(t, tmpDir, "f.txt")
	testutil.GitCommit(t, tmpDir, "init")
	t.Chdir(tmpDir)
	paths.ClearWorktreeRootCache()
}

func setupTestState(t *testing.T, mode memoryloop.Mode, records []memoryloop.MemoryRecord) {
	t.Helper()
	state := &memoryloop.State{
		Store: &memoryloop.Store{
			Version:     1,
			Mode:        mode,
			MaxInjected: 10,
			Records:     records,
		},
	}
	if err := memoryloop.SaveState(context.Background(), state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
}

func TestCheckModeGate_Off(t *testing.T) {
	setupTestRepo(t)
	setupTestState(t, memoryloop.ModeOff, nil)

	_, err := checkModeGate(context.Background())
	if err == nil {
		t.Fatal("expected error for mode=off, got nil")
	}
}

func TestCheckModeGate_Manual(t *testing.T) {
	setupTestRepo(t)
	records := []memoryloop.MemoryRecord{
		{ID: "test", Title: "Test", Body: "body", Kind: memoryloop.KindRepoRule, Status: memoryloop.StatusActive},
	}
	setupTestState(t, memoryloop.ModeManual, records)

	got, err := checkModeGate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error for mode=manual: %v", err)
	}
	if got.Store.Mode != memoryloop.ModeManual {
		t.Errorf("mode = %s, want manual", got.Store.Mode)
	}
}

func TestCheckModeGate_Auto(t *testing.T) {
	setupTestRepo(t)
	setupTestState(t, memoryloop.ModeAuto, nil)

	got, err := checkModeGate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error for mode=auto: %v", err)
	}
	if got.Store.Mode != memoryloop.ModeAuto {
		t.Errorf("mode = %s, want auto", got.Store.Mode)
	}
}
