// Package filter provides clean/smudge transcript filtering for path normalization.
// Transcripts stored in checkpoints contain absolute paths (repo root, home directory)
// which are machine-specific. This package normalizes paths on store (clean) and
// restores them on read (smudge), similar to git's clean/smudge filter pipeline.
package filter

import (
	"bytes"

	"github.com/entireio/cli/cmd/entire/cli/settings"
)

// Filter defines a single find-and-replace pair used during clean/smudge.
// Clean replaces Match with Replace; Smudge reverses the substitution.
type Filter struct {
	Match   string // literal string to find
	Replace string // literal string to substitute
}

// Clean applies the filter in the "store" direction: Match → Replace.
func (f Filter) Clean(data []byte) []byte {
	return bytes.ReplaceAll(data, []byte(f.Match), []byte(f.Replace))
}

// Smudge applies the filter in the "restore" direction: Replace → Match.
func (f Filter) Smudge(data []byte) []byte {
	return bytes.ReplaceAll(data, []byte(f.Replace), []byte(f.Match))
}

// Pipeline holds an ordered list of filters and applies them collectively.
// A nil *Pipeline is safe to use — all methods are no-ops.
type Pipeline struct {
	filters []Filter
}

// NewPipeline creates a filter pipeline with built-in path filters and optional user filters.
// Built-in filters (applied first, most-specific first):
//  1. repoRoot → __ent__/repo
//  2. homeDir  → __ent__/home
//
// User filters are appended after built-ins.
func NewPipeline(repoRoot, homeDir string, userFilters []settings.TranscriptFilter) (*Pipeline, error) {
	var filters []Filter

	// Built-in filters, most-specific first (repo root before home dir,
	// since repo root is typically under home dir).
	if repoRoot != "" {
		f := Filter{Match: repoRoot, Replace: "__ent__/repo"}
		if err := ValidateFilter(f, true); err != nil {
			return nil, err
		}
		filters = append(filters, f)
	}
	if homeDir != "" {
		f := Filter{Match: homeDir, Replace: "__ent__/home"}
		if err := ValidateFilter(f, true); err != nil {
			return nil, err
		}
		filters = append(filters, f)
	}

	// User filters
	for _, uf := range userFilters {
		f := Filter{
			Match:   uf.Match,
			Replace: "__ent_user__/" + uf.Key,
		}
		if err := ValidateFilter(f, false); err != nil {
			return nil, err
		}
		filters = append(filters, f)
	}

	return &Pipeline{filters: filters}, nil
}

// Clean applies all filters in order (store direction).
// Safe to call on a nil *Pipeline (returns data unchanged).
func (p *Pipeline) Clean(data []byte) []byte {
	if p == nil {
		return data
	}
	for _, f := range p.filters {
		data = f.Clean(data)
	}
	return data
}

// Smudge applies all filters in reverse order (restore direction).
// Safe to call on a nil *Pipeline (returns data unchanged).
func (p *Pipeline) Smudge(data []byte) []byte {
	if p == nil {
		return data
	}
	for i := len(p.filters) - 1; i >= 0; i-- {
		data = p.filters[i].Smudge(data)
	}
	return data
}

// CleanString applies Clean to a string value.
// Safe to call on a nil *Pipeline (returns s unchanged).
func (p *Pipeline) CleanString(s string) string {
	if p == nil {
		return s
	}
	return string(p.Clean([]byte(s)))
}

// SmudgeString applies Smudge to a string value.
// Safe to call on a nil *Pipeline (returns s unchanged).
func (p *Pipeline) SmudgeString(s string) string {
	if p == nil {
		return s
	}
	return string(p.Smudge([]byte(s)))
}
