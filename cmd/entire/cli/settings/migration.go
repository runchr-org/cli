package settings

import "sort"

// MigrateLegacyRoles upgrades pre-role ReviewConfig entries in-place.
// Idempotent: re-running on an already-migrated map is a no-op.
// Returns true if any entry was modified.
//
// Rules:
//   - Entries with non-empty Role are left untouched.
//   - The agent named in s.ReviewFixAgent becomes:
//   - RoleBoth if it has Skills or Prompt configured
//   - RoleFixer otherwise
//   - All other entries with empty Role become RoleReviewer.
//   - At-most-one-fixer is enforced (alphabetical winner if duplicates exist).
func MigrateLegacyRoles(s *EntireSettings) bool {
	if s == nil || len(s.Review) == 0 {
		return false
	}
	anyEmpty := false
	for _, cfg := range s.Review {
		if cfg.Role == "" {
			anyEmpty = true
			break
		}
	}
	if !anyEmpty {
		return false
	}

	for name, cfg := range s.Review {
		if cfg.Role != "" {
			continue
		}
		if name == s.ReviewFixAgent {
			if len(cfg.Skills) > 0 || cfg.Prompt != "" {
				cfg.Role = RoleBoth
			} else {
				cfg.Role = RoleFixer
			}
		} else {
			cfg.Role = RoleReviewer
		}
		s.Review[name] = cfg
	}

	// Enforce at-most-one-fixer (alphabetical winner).
	var fixers []string
	for name, cfg := range s.Review {
		if cfg.Role.IsFixer() {
			fixers = append(fixers, name)
		}
	}
	if len(fixers) > 1 {
		sort.Strings(fixers)
		for _, loser := range fixers[1:] {
			cfg := s.Review[loser]
			cfg.Role = RoleReviewer
			s.Review[loser] = cfg
		}
	}
	return true
}
