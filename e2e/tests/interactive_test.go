//go:build e2e

package tests

import (
	"context"
	"testing"
	"time"

	"github.com/entireio/cli/e2e/testutil"
)

// TestInteractiveMidTurnCommit: agent creates a file and commits it within a
// single interactive turn. This exercises the TUI event path for turn-end
// detection — unlike RunPrompt (which uses non-interactive mode where process
// exit forces cleanup), the interactive session stays alive and must receive
// the idle event to trigger turn-end and transcript export.
//
// This is a regression test for OpenCode where session.status idle events
// were not reaching the plugin in TUI mode, causing turn-end to never fire,
// transcript export to never run, and condensation to fail with
// "transcript not found".
func TestInteractiveMidTurnCommit(t *testing.T) {
	testutil.ForEachAgent(t, 3*time.Minute, func(t *testing.T, s *testutil.RepoState, ctx context.Context) {
		prompt := s.Agent.PromptPattern()

		// External agents (roger-roger) use different hook mechanisms and have
		// WaitFor settle issues with short interactive output — skip them.
		if s.IsExternalAgent() {
			t.Skipf("skipping interactive mid-turn commit for external agent %s", s.Agent.Name())
		}

		session := s.StartSession(t, ctx)
		if session == nil {
			t.Skipf("agent %s does not support interactive mode", s.Agent.Name())
		}

		s.WaitFor(t, session, prompt, 30*time.Second)

		// Single turn: create a file and commit it. The agent commits mid-turn,
		// then finishes (gives summary). The turn-end hook must fire after the
		// agent goes idle — in TUI mode this depends on the plugin receiving
		// the idle event, not on process exit.
		s.Send(t, session, "create a markdown file at docs/red.md with a paragraph about the colour red, then commit it. Do not ask for confirmation, just make the change.")
		s.WaitFor(t, session, prompt, 90*time.Second)

		testutil.AssertFileExists(t, s.Dir, "docs/*.md")
		testutil.AssertNewCommits(t, s, 1)

		testutil.WaitForCheckpoint(t, s, 15*time.Second)
		testutil.AssertCommitLinkedToCheckpoint(t, s.Dir, "HEAD")
		testutil.AssertNoShadowBranches(t, s.Dir)
	})
}

func TestInteractiveMultiStep(t *testing.T) {
	testutil.ForEachAgent(t, 3*time.Minute, func(t *testing.T, s *testutil.RepoState, ctx context.Context) {
		prompt := s.Agent.PromptPattern()

		session := s.StartSession(t, ctx)
		if session == nil {
			t.Skipf("agent %s does not support interactive mode", s.Agent.Name())
		}

		s.WaitFor(t, session, prompt, 30*time.Second)

		s.Send(t, session, "create a markdown file at docs/red.md with a paragraph about the colour red. Do not ask for confirmation, just make the change.")
		s.WaitFor(t, session, prompt, 60*time.Second)
		testutil.WaitForFileExists(t, s.Dir, "docs/*.md", 30*time.Second)

		s.Send(t, session, "now commit it")
		s.WaitFor(t, session, prompt, 60*time.Second)
		testutil.AssertNewCommits(t, s, 1)

		testutil.WaitForCheckpoint(t, s, 15*time.Second)
		testutil.AssertCommitLinkedToCheckpoint(t, s.Dir, "HEAD")
		testutil.AssertNoShadowBranches(t, s.Dir)
	})
}
