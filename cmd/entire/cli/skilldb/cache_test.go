package skilldb_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/entireio/cli/cmd/entire/cli/skilldb"
)

func TestResolveSkillName_ExactMatch(t *testing.T) {
	t.Parallel()
	m := map[string]skilldb.SkillRow{
		"e2e": {Name: "e2e", SourceAgent: "claude-code"},
		"dev": {Name: "dev", SourceAgent: "claude-code"},
	}
	got, ok := skilldb.ResolveSkillName("e2e", m)
	assert.True(t, ok)
	assert.Equal(t, "e2e", got.Name)
}

func TestResolveSkillName_SubSkillPrefix(t *testing.T) {
	t.Parallel()
	m := map[string]skilldb.SkillRow{
		"e2e": {Name: "e2e", SourceAgent: "claude-code"},
	}
	// "e2e:triage" should match "e2e" via parent prefix
	got, ok := skilldb.ResolveSkillName("e2e:triage", m)
	assert.True(t, ok)
	assert.Equal(t, "e2e", got.Name)
}

func TestResolveSkillName_LastSegment(t *testing.T) {
	t.Parallel()
	m := map[string]skilldb.SkillRow{
		"writing-plans": {Name: "writing-plans", SourceAgent: "claude-code"},
	}
	// "superpowers:writing-plans" should match via last segment
	got, ok := skilldb.ResolveSkillName("superpowers:writing-plans", m)
	assert.True(t, ok)
	assert.Equal(t, "writing-plans", got.Name)
}

func TestResolveSkillName_ContainedSegment(t *testing.T) {
	t.Parallel()
	m := map[string]skilldb.SkillRow{
		"e2e": {Name: "e2e", SourceAgent: "claude-code"},
	}
	// "cli-e2e-failure-fix" contains "e2e" as a delimited segment
	got, ok := skilldb.ResolveSkillName("cli-e2e-failure-fix", m)
	assert.True(t, ok)
	assert.Equal(t, "e2e", got.Name)
}

func TestResolveSkillName_NoMatch(t *testing.T) {
	t.Parallel()
	m := map[string]skilldb.SkillRow{
		"e2e": {Name: "e2e", SourceAgent: "claude-code"},
		"dev": {Name: "dev", SourceAgent: "claude-code"},
	}
	_, ok := skilldb.ResolveSkillName("completely-unknown-skill", m)
	assert.False(t, ok)
}

func TestResolveSkillName_ShortNameNoFalsePositive(t *testing.T) {
	t.Parallel()
	m := map[string]skilldb.SkillRow{
		"a": {Name: "a", SourceAgent: "claude-code"},
	}
	// Single-char names should be skipped in substring matching to avoid false positives
	_, ok := skilldb.ResolveSkillName("alpha-beta", m)
	assert.False(t, ok)
}

func TestResolveSkillName_NoPartialWordMatch(t *testing.T) {
	t.Parallel()
	m := map[string]skilldb.SkillRow{
		"dev": {Name: "dev", SourceAgent: "claude-code"},
	}
	// "developer-tools" contains "dev" but not at a word boundary (delimiter)
	_, ok := skilldb.ResolveSkillName("developer-tools", m)
	assert.False(t, ok)
}

func TestResolveSkillName_MultipleColonSegments(t *testing.T) {
	t.Parallel()
	m := map[string]skilldb.SkillRow{
		"agent-integration": {Name: "agent-integration", SourceAgent: "claude-code"},
	}
	// "agent-integration:research" should match via parent prefix
	got, ok := skilldb.ResolveSkillName("agent-integration:research", m)
	assert.True(t, ok)
	assert.Equal(t, "agent-integration", got.Name)
}
