package filter

import (
	"errors"
	"fmt"
	"strings"
)

const (
	builtInPrefix = "__ent__/"
	userPrefix    = "__ent_user__/"
	minMatchLen   = 8
)

// ValidateFilter checks that a filter is well-formed.
// isBuiltIn distinguishes built-in filters (whose replacements start with __ent__/)
// from user filters (whose replacements must start with __ent_user__/).
func ValidateFilter(f Filter, isBuiltIn bool) error {
	if f.Match == "" {
		return errors.New("filter match must be non-empty")
	}
	if f.Replace == "" {
		return errors.New("filter replace must be non-empty")
	}
	if f.Match == f.Replace {
		return errors.New("filter match and replace must be different")
	}

	// Idempotency: applying clean twice must produce the same result.
	// This means the replacement string must not itself contain the match pattern,
	// AND the match string must not contain the replacement pattern (which would
	// cause smudge to corrupt the match).
	if strings.Contains(f.Replace, f.Match) {
		return fmt.Errorf("filter replace %q must not contain match %q (not idempotent)", f.Replace, f.Match)
	}
	if strings.Contains(f.Match, f.Replace) {
		return fmt.Errorf("filter match %q must not contain replace %q (smudge would corrupt)", f.Match, f.Replace)
	}

	if isBuiltIn {
		if !strings.HasPrefix(f.Replace, builtInPrefix) {
			return fmt.Errorf("built-in filter replace must start with %q, got %q", builtInPrefix, f.Replace)
		}
	} else {
		if !strings.HasPrefix(f.Replace, userPrefix) {
			return fmt.Errorf("user filter replace must start with %q, got %q", userPrefix, f.Replace)
		}
		if len(f.Match) < minMatchLen {
			return fmt.Errorf("user filter match must be at least %d characters, got %d", minMatchLen, len(f.Match))
		}
	}

	return nil
}
