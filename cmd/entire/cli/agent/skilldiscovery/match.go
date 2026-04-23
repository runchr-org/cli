// Package skilldiscovery holds the per-agent registries (curated built-ins,
// install hints) and the keyword match helper that the `entire review`
// picker uses to discover review-adjacent skills.
package skilldiscovery

import "strings"

// reviewKeywords are the substrings that mark a skill as review-adjacent.
// Kept narrow on purpose — see the spec's "Keyword set — deliberate
// omissions" section for why "lint" is NOT here.
var reviewKeywords = []string{
	"review",
	"audit",
	"inspect",
	"critique",
	"assess",
	"security-scan",
}

// Matches reports whether the given skill name or description contains any
// review-adjacent keyword (case-insensitive).
func Matches(name, description string) bool {
	lname := strings.ToLower(name)
	ldesc := strings.ToLower(description)
	for _, kw := range reviewKeywords {
		if strings.Contains(lname, kw) || strings.Contains(ldesc, kw) {
			return true
		}
	}
	return false
}
