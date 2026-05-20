// Package review — see env.go for package-level rationale.
//
// roles.go provides role helpers used by runReview and the setup
// subcommand. The migration body lives in the settings package
// (MigrateLegacyRoles); this file thin-wraps it so review-package
// tests can exercise everything in one place.
package review

import (
	"sort"

	"github.com/entireio/cli/cmd/entire/cli/settings"
)

// NormalizeRoles enforces the at-most-one-fixer invariant. Empty roles
// upgrade to Reviewer. Duplicates: alphabetical winner keeps its role;
// the rest are demoted to Reviewer. Returns a new map.
func NormalizeRoles(in map[string]settings.ReviewConfig) map[string]settings.ReviewConfig {
	out := make(map[string]settings.ReviewConfig, len(in))
	if len(in) == 0 {
		return out
	}
	var fixerCandidates []string
	for name, cfg := range in {
		if cfg.Role == "" {
			cfg.Role = settings.RoleReviewer
		}
		if cfg.Role.IsFixer() {
			fixerCandidates = append(fixerCandidates, name)
		}
		out[name] = cfg
	}
	if len(fixerCandidates) > 1 {
		sort.Strings(fixerCandidates)
		for _, loser := range fixerCandidates[1:] {
			cfg := out[loser]
			cfg.Role = settings.RoleReviewer
			out[loser] = cfg
		}
	}
	return out
}

// MigrateLegacyRoles is a thin wrapper around settings.MigrateLegacyRoles
// to keep the review-package test surface cohesive.
func MigrateLegacyRoles(s *settings.EntireSettings) bool {
	return settings.MigrateLegacyRoles(s)
}

// ReviewersOf returns the sorted set of agent names with RoleReviewer
// or RoleBoth.
func ReviewersOf(s *settings.EntireSettings) []string {
	if s == nil {
		return nil
	}
	var out []string
	for name, cfg := range s.Review {
		if cfg.Role.IsReviewer() {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// FixerOf returns the agent name with RoleFixer or RoleBoth. Returns ""
// when no Fixer is configured. Assumes NormalizeRoles has been called;
// in the duplicate-fixer case returns the alphabetically-first.
func FixerOf(s *settings.EntireSettings) string {
	if s == nil {
		return ""
	}
	var candidates []string
	for name, cfg := range s.Review {
		if cfg.Role.IsFixer() {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Strings(candidates)
	return candidates[0]
}
