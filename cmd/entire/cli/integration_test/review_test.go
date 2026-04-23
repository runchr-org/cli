//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/session"
)

// TestReview_MarkerAdoptionCondensesReviewMetadataOnNextCommit exercises
// the full adoption pipeline: a pending marker written before the agent
// session is adopted on UserPromptSubmit, then condensed into checkpoint
// metadata on the next git commit. The marker is written directly rather
// than through a CLI subcommand so the test focuses on adoption behavior,
// not on CLI wiring.
func TestReview_MarkerAdoptionCondensesReviewMetadataOnNextCommit(t *testing.T) {
	t.Parallel()

	env := NewFeatureBranchEnv(t)
	enableReviewAgent(t, env, "claude-code")

	env.WritePendingReviewMarker(
		"claude-code",
		[]string{"/pr-review-toolkit:review-pr", "/test-auditor"},
		env.GetHeadHash(),
	)

	sess := env.NewSession()
	reviewPrompt := "review the branch for correctness and missing tests"
	if err := env.SimulateUserPromptSubmitWithPrompt(sess.ID, reviewPrompt); err != nil {
		t.Fatalf("SimulateUserPromptSubmitWithPrompt failed: %v", err)
	}

	state, err := env.GetSessionState(sess.ID)
	if err != nil {
		t.Fatalf("GetSessionState failed: %v", err)
	}
	if state == nil {
		t.Fatal("expected review session state to be created")
	}
	if state.Kind != session.KindAgentReview {
		t.Fatalf("state.Kind = %q, want %q", state.Kind, session.KindAgentReview)
	}
	if len(state.ReviewSkills) != 2 || state.ReviewSkills[0] != "/pr-review-toolkit:review-pr" || state.ReviewSkills[1] != "/test-auditor" {
		t.Fatalf("state.ReviewSkills = %v", state.ReviewSkills)
	}
	if !strings.Contains(state.ReviewPrompt, "/pr-review-toolkit:review-pr") || !strings.Contains(state.ReviewPrompt, "/test-auditor") {
		t.Fatalf("state.ReviewPrompt missing configured skills: %q", state.ReviewPrompt)
	}

	env.WriteFile("review_target.go", "package main\n\nfunc ReviewTarget() string { return \"ok\" }\n")
	sess.CreateTranscript(reviewPrompt, []FileChange{
		{Path: "review_target.go", Content: "package main\n\nfunc ReviewTarget() string { return \"ok\" }\n"},
	})
	if err := env.SimulateStop(sess.ID, sess.TranscriptPath); err != nil {
		t.Fatalf("SimulateStop failed: %v", err)
	}

	env.GitCommitWithShadowHooks("add review target", "review_target.go")

	checkpointID := env.GetCheckpointIDFromCommitMessage(env.GetHeadHash())
	if checkpointID == "" {
		t.Fatal("expected Entire-Checkpoint trailer on HEAD after commit")
	}

	summary := readCheckpointSummary(t, env, checkpointID)
	if !summary.HasReview {
		t.Fatalf("summary.HasReview = false for checkpoint %s", checkpointID)
	}

	metadata := readSessionMetadata(t, env, checkpointID)
	if metadata.SessionID != sess.ID {
		t.Fatalf("metadata.SessionID = %q, want %q", metadata.SessionID, sess.ID)
	}
	if metadata.Kind != string(session.KindAgentReview) {
		t.Fatalf("metadata.Kind = %q, want %q", metadata.Kind, session.KindAgentReview)
	}
	if len(metadata.ReviewSkills) != 2 || metadata.ReviewSkills[0] != "/pr-review-toolkit:review-pr" || metadata.ReviewSkills[1] != "/test-auditor" {
		t.Fatalf("metadata.ReviewSkills = %v", metadata.ReviewSkills)
	}
	if metadata.ReviewPrompt != state.ReviewPrompt {
		t.Fatalf("metadata.ReviewPrompt = %q, want %q", metadata.ReviewPrompt, state.ReviewPrompt)
	}
}

func TestReviewAttach_TagsAttachedSessionAsReview(t *testing.T) {
	t.Parallel()

	env := NewFeatureBranchEnv(t)

	sessionID := "review-attach-session"
	tb := NewTranscriptBuilder()
	tb.AddUserMessage("review the branch for security regressions")
	tb.AddAssistantMessage("I found a few areas to check.")
	transcriptPath := filepath.Join(env.ClaudeProjectDir, sessionID+".jsonl")
	if err := tb.WriteToFile(transcriptPath); err != nil {
		t.Fatalf("failed to write transcript: %v", err)
	}

	output := env.RunCLI("review", "attach", sessionID, "--force", "--agent", "claude-code", "--skills", "/pr-review-toolkit:review-pr")
	if !strings.Contains(output, "Attached session") {
		t.Fatalf("expected attached session output, got:\n%s", output)
	}

	checkpointID := env.GetCheckpointIDFromCommitMessage(env.GetHeadHash())
	if checkpointID == "" {
		t.Fatal("expected Entire-Checkpoint trailer on HEAD after review attach")
	}

	state, err := env.GetSessionState(sessionID)
	if err != nil {
		t.Fatalf("GetSessionState failed: %v", err)
	}
	if state == nil {
		t.Fatal("expected attached session state to be created")
	}
	if state.Kind != session.KindAgentReview {
		t.Fatalf("state.Kind = %q, want %q", state.Kind, session.KindAgentReview)
	}
	if len(state.ReviewSkills) != 1 || state.ReviewSkills[0] != "/pr-review-toolkit:review-pr" {
		t.Fatalf("state.ReviewSkills = %v", state.ReviewSkills)
	}
	if state.ReviewPrompt != "review the branch for security regressions" {
		t.Fatalf("state.ReviewPrompt = %q", state.ReviewPrompt)
	}

	summary := readCheckpointSummary(t, env, checkpointID)
	if !summary.HasReview {
		t.Fatalf("summary.HasReview = false for checkpoint %s", checkpointID)
	}

	metadata := readSessionMetadata(t, env, checkpointID)
	if metadata.Kind != string(session.KindAgentReview) {
		t.Fatalf("metadata.Kind = %q, want %q", metadata.Kind, session.KindAgentReview)
	}
	if len(metadata.ReviewSkills) != 1 || metadata.ReviewSkills[0] != "/pr-review-toolkit:review-pr" {
		t.Fatalf("metadata.ReviewSkills = %v", metadata.ReviewSkills)
	}
	if metadata.ReviewPrompt != "review the branch for security regressions" {
		t.Fatalf("metadata.ReviewPrompt = %q", metadata.ReviewPrompt)
	}
}

func TestReview_MarkerAdoption_WrongAgentDoesNotAdoptMarker(t *testing.T) {
	t.Parallel()

	env := NewFeatureBranchEnv(t)
	enableReviewAgent(t, env, "claude-code")
	enableReviewAgent(t, env, "gemini")

	env.WritePendingReviewMarker(
		"claude-code",
		[]string{"/pr-review-toolkit:review-pr"},
		env.GetHeadHash(),
	)

	geminiSession := env.NewGeminiSession()
	if err := env.SimulateGeminiBeforeAgent(geminiSession.ID); err != nil {
		t.Fatalf("SimulateGeminiBeforeAgent failed: %v", err)
	}

	geminiState, err := env.GetSessionState(geminiSession.ID)
	if err != nil {
		t.Fatalf("GetSessionState for Gemini session failed: %v", err)
	}
	if geminiState == nil {
		t.Fatal("expected Gemini session state to be created")
	}
	if geminiState.Kind != "" {
		t.Fatalf("geminiState.Kind = %q, want empty", geminiState.Kind)
	}
	if _, err := os.Stat(pendingReviewMarkerPath(env)); err != nil {
		t.Fatalf("expected pending review marker to remain after wrong-agent session: %v", err)
	}

	claudeSession := env.NewSession()
	if err := env.SimulateUserPromptSubmitWithPrompt(claudeSession.ID, "run the configured review"); err != nil {
		t.Fatalf("SimulateUserPromptSubmitWithPrompt failed: %v", err)
	}

	claudeState, err := env.GetSessionState(claudeSession.ID)
	if err != nil {
		t.Fatalf("GetSessionState for Claude session failed: %v", err)
	}
	if claudeState == nil {
		t.Fatal("expected Claude session state to be created")
	}
	if claudeState.Kind != session.KindAgentReview {
		t.Fatalf("claudeState.Kind = %q, want %q", claudeState.Kind, session.KindAgentReview)
	}
	if _, err := os.Stat(pendingReviewMarkerPath(env)); !os.IsNotExist(err) {
		t.Fatalf("expected marker to be cleared after correct-agent adoption, err=%v", err)
	}
}

func TestReview_MarkerAdoption_StaleMarkerIsIgnoredAfterHeadMoves(t *testing.T) {
	t.Parallel()

	env := NewFeatureBranchEnv(t)
	enableReviewAgent(t, env, "claude-code")

	env.WritePendingReviewMarker(
		"claude-code",
		[]string{"/pr-review-toolkit:review-pr"},
		env.GetHeadHash(),
	)

	env.WriteFile("head_move.txt", "new head\n")
	env.GitAdd("head_move.txt")
	env.GitCommit("move head before review session starts")

	sess := env.NewSession()
	if err := env.SimulateUserPromptSubmitWithPrompt(sess.ID, "run the configured review"); err != nil {
		t.Fatalf("SimulateUserPromptSubmitWithPrompt failed: %v", err)
	}

	state, err := env.GetSessionState(sess.ID)
	if err != nil {
		t.Fatalf("GetSessionState failed: %v", err)
	}
	if state == nil {
		t.Fatal("expected session state to be created")
	}
	if state.Kind != "" {
		t.Fatalf("state.Kind = %q, want empty because marker is stale", state.Kind)
	}
	if _, err := os.Stat(pendingReviewMarkerPath(env)); !os.IsNotExist(err) {
		t.Fatalf("expected stale marker to be cleared, err=%v", err)
	}
}

// TestReview_MissingSkillAtSpawn_ErrorsCleanly pins the runtime verification
// guard: a settings file naming a nonexistent skill must cause entire review
// to exit non-zero with a clear stderr message and leave no pending marker.
func TestReview_MissingSkillAtSpawn_ErrorsCleanly(t *testing.T) {
	t.Parallel()

	env := NewFeatureBranchEnv(t)
	enableReviewAgent(t, env, "claude-code")

	env.WriteSettings(map[string]any{
		"review": map[string]any{
			"claude-code": map[string]any{
				"skills": []string{"/nonexistent:skill-xyz"},
			},
		},
	})

	output, exitErr := env.RunCLIWithError("review")
	if exitErr == nil {
		t.Fatalf("expected non-zero exit; output:\n%s", output)
	}
	if !strings.Contains(output, "not installed") {
		t.Errorf("stderr should mention skill not installed; got:\n%s", output)
	}
	if _, err := os.Stat(filepath.Join(env.RepoDir, ".git", "entire-sessions", "review-pending.json")); !os.IsNotExist(err) {
		t.Errorf("pending marker should not exist; stat err=%v", err)
	}
}

func enableReviewAgent(t *testing.T, env *TestEnv, name string) {
	t.Helper()
	env.RunCLI("enable", "--agent", name, "--telemetry=false")
}

func pendingReviewMarkerPath(env *TestEnv) string {
	return filepath.Join(env.RepoDir, ".git", "entire-sessions", "review-pending.json")
}

func readCheckpointSummary(t *testing.T, env *TestEnv, checkpointID string) checkpoint.CheckpointSummary {
	t.Helper()

	content, found := env.ReadFileFromBranch(paths.MetadataBranchName, CheckpointSummaryPath(checkpointID))
	if !found {
		t.Fatalf("checkpoint summary not found for %s", checkpointID)
	}

	var summary checkpoint.CheckpointSummary
	if err := json.Unmarshal([]byte(content), &summary); err != nil {
		t.Fatalf("failed to parse checkpoint summary: %v\n%s", err, content)
	}
	return summary
}

func readSessionMetadata(t *testing.T, env *TestEnv, checkpointID string) checkpoint.CommittedMetadata {
	t.Helper()

	content, found := env.ReadFileFromBranch(paths.MetadataBranchName, SessionMetadataPath(checkpointID))
	if !found {
		t.Fatalf("session metadata not found for %s", checkpointID)
	}

	var metadata checkpoint.CommittedMetadata
	if err := json.Unmarshal([]byte(content), &metadata); err != nil {
		t.Fatalf("failed to parse session metadata: %v\n%s", err, content)
	}
	return metadata
}
