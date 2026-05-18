//go:build integration

package integration

import (
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestV11DualWrite_EndToEnd verifies the v1.1 dual-write contract end-to-end:
//
//  1. A condensation writes the legacy branch and the two new custom refs.
//  2. refs/entire/checkpoints/v1/full is aliased to the legacy branch's
//     commit hash (write-alias contract).
//  3. refs/entire/checkpoints/v1/main carries an independent commit chain
//     with the compact tree.
//  4. PrePush sends all three refs to the bare remote.
//  5. A default `git clone` does NOT pull the v1.1 custom refs (they live
//     under refs/entire/, not refs/heads/).
func TestV11DualWrite_EndToEnd(t *testing.T) {
	t.Parallel()
	env := NewFeatureBranchEnv(t)

	bareDir := env.SetupBareRemote()

	// Drive one condensation through a normal session→commit flow.
	session := env.NewSession()
	transcriptPath := session.CreateTranscript("Add hello.go", []FileChange{
		{Path: "hello.go", Content: "package main\n"},
	})
	require.NoError(t, env.SimulateUserPromptSubmitWithPromptAndTranscriptPath(
		session.ID, "Add hello.go", transcriptPath))

	env.WriteFile("hello.go", "package main\n")
	env.GitAdd("hello.go")

	require.NoError(t, env.SimulateStop(session.ID, transcriptPath))
	env.GitCommitWithShadowHooks("Add hello.go", "hello.go")

	// All three refs must exist locally.
	assert.True(t, env.BranchExists(paths.MetadataBranchName),
		"legacy branch must exist after condensation")
	assert.True(t, env.RefExists(paths.MetadataCompactRefName),
		"refs/entire/checkpoints/v1/main must exist after condensation")
	assert.True(t, env.RefExists(paths.MetadataFullRefName),
		"refs/entire/checkpoints/v1/full must exist after condensation")

	// v1/full must point at the same commit hash as the legacy branch.
	legacyHash := env.GetRefHash("refs/heads/" + paths.MetadataBranchName)
	fullRefHash := env.GetRefHash(paths.MetadataFullRefName)
	assert.Equal(t, legacyHash, fullRefHash,
		"refs/entire/checkpoints/v1/full must alias the legacy branch")

	// v1/main is an independent commit chain.
	compactHash := env.GetRefHash(paths.MetadataCompactRefName)
	assert.NotEqual(t, legacyHash, compactHash,
		"refs/entire/checkpoints/v1/main is an independent commit chain")

	// Tree-shape spot-checks.
	cpIDStr := env.GetLatestCheckpointIDFromHistory()
	require.NotEmpty(t, cpIDStr)
	cpPath := cpIDStr[:2] + "/" + cpIDStr[2:]

	_, hasFullJSONL := env.ReadFileFromBranch(paths.MetadataBranchName,
		cpPath+"/0/"+paths.TranscriptFileName)
	assert.True(t, hasFullJSONL, "legacy branch must contain full.jsonl")

	_, hasFullJSONLOnFullRef := env.ReadFileFromRef(paths.MetadataFullRefName,
		cpPath+"/0/"+paths.TranscriptFileName)
	assert.True(t, hasFullJSONLOnFullRef,
		"v1/full ref (aliased to legacy) must also contain full.jsonl")

	_, hasCompactJSONL := env.ReadFileFromRef(paths.MetadataCompactRefName,
		cpPath+"/0/"+paths.CompactTranscriptFileName)
	assert.True(t, hasCompactJSONL, "compact ref must contain transcript.jsonl")

	// Push to the bare remote.
	env.RunPrePush("origin")

	// All three refs must now exist on the bare remote.
	assert.True(t, env.BranchExistsOnRemote(bareDir, paths.MetadataBranchName),
		"legacy branch must be pushed to remote")
	assert.True(t, env.RefExistsOnRemote(bareDir, paths.MetadataCompactRefName),
		"refs/entire/checkpoints/v1/main must be pushed to remote")
	assert.True(t, env.RefExistsOnRemote(bareDir, paths.MetadataFullRefName),
		"refs/entire/checkpoints/v1/full must be pushed to remote")

	// A default `git clone` pulls only refs/heads/* — the v1.1 custom refs
	// must NOT come along by default. (Verifies the "invisible / not in
	// default clone" win.)
	fresh := env.CloneFrom(bareDir)
	// The legacy branch arrives as a remote-tracking ref (origin/<name>)
	// since `git clone --branch <feature>` only materializes one local branch.
	assert.True(t, fresh.RefExists("refs/remotes/origin/"+paths.MetadataBranchName),
		"legacy branch should be pulled by default clone (as origin/entire/checkpoints/v1)")
	// CloneFrom by default doesn't fetch refs outside refs/heads/, so the
	// new custom refs should not be present in the fresh clone yet.
	assert.False(t, fresh.RefExists(paths.MetadataCompactRefName),
		"refs/entire/checkpoints/v1/main must NOT be in a default clone")
	assert.False(t, fresh.RefExists(paths.MetadataFullRefName),
		"refs/entire/checkpoints/v1/full must NOT be in a default clone")
}
