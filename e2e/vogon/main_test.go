package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParsePrompt_Push(t *testing.T) {
	t.Parallel()

	t.Run("create commit push sequence", func(t *testing.T) {
		t.Parallel()
		actions := parsePrompt("create a file at hello.txt about greetings, then commit and push")
		assert.Equal(t, []string{"create", "commit", "push"}, actionKinds(actions))
	})

	t.Run("negative does not push", func(t *testing.T) {
		t.Parallel()
		actions := parsePrompt("create a file at foo.txt about foo, commit. Do not push.")
		assert.Equal(t, []string{"create", "commit"}, actionKinds(actions))
	})

	t.Run("numbered steps", func(t *testing.T) {
		t.Parallel()
		actions := parsePrompt("(1) create a file at step.txt about steps (2) commit (3) push")
		assert.Equal(t, []string{"create", "commit", "push"}, actionKinds(actions))
	})
}

func actionKinds(actions []action) []string {
	kinds := make([]string, len(actions))
	for i, a := range actions {
		kinds[i] = a.kind
	}
	return kinds
}
