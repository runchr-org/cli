package skilldb

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Scope constants for skill discovery.
const (
	ScopeRepo     = "repo"
	ScopePersonal = "personal"
	ScopePlugin   = "plugin"
)

// DiscoveredSkill represents a skill file found in an agent's config directory.
type DiscoveredSkill struct {
	Name        string // skill name (e.g., "e2e", "dev")
	SourceAgent string // "claude-code" or "gemini-cli"
	Path        string // relative path from base dir (repo root or home)
	Kind        string // "skill", "command", or "agent-def"
	Scope       string // "repo" or "personal"
}

// DiscoverSkills scans both the repo root and user home for skill files.
// Missing directories are silently skipped.
func DiscoverSkills(repoRoot string) ([]DiscoveredSkill, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "" // skip personal skills if home dir unavailable
	}

	var skills []DiscoveredSkill

	// Repo skills
	repoSkills, err := discoverSkillsIn(repoRoot, repoRoot, ScopeRepo)
	if err != nil {
		return nil, fmt.Errorf("repo skills: %w", err)
	}
	skills = append(skills, repoSkills...)

	// Personal skills (from ~/.claude/, ~/.gemini/)
	if homeDir != "" {
		personalSkills, pErr := discoverSkillsIn(homeDir, homeDir, ScopePersonal)
		if pErr != nil {
			return nil, fmt.Errorf("personal skills: %w", pErr)
		}
		// Deduplicate: skip personal skills that have the same name+agent as a repo skill.
		repoNames := make(map[string]bool, len(repoSkills))
		for _, s := range repoSkills {
			repoNames[s.Name+"|"+s.SourceAgent] = true
		}
		for _, s := range personalSkills {
			if !repoNames[s.Name+"|"+s.SourceAgent] {
				skills = append(skills, s)
			}
		}
	}

	// Plugin skills (from ~/.claude/plugins/cache/).
	if homeDir != "" {
		pluginCacheDir := filepath.Join(homeDir, ".claude", "plugins", "cache")
		pluginSkills := discoverPluginSkills(pluginCacheDir)
		// Deduplicate: skip plugin skills that overlap with repo or personal skills.
		existingNames := make(map[string]bool, len(skills))
		for _, s := range skills {
			existingNames[s.Name+"|"+s.SourceAgent] = true
		}
		for _, s := range pluginSkills {
			if !existingNames[s.Name+"|"+s.SourceAgent] {
				skills = append(skills, s)
				existingNames[s.Name+"|"+s.SourceAgent] = true
			}
		}
	}

	sort.Slice(skills, func(i, j int) bool {
		// Repo skills first, then personal, then plugin.
		if skills[i].Scope != skills[j].Scope {
			return skills[i].Scope < skills[j].Scope
		}
		if skills[i].SourceAgent != skills[j].SourceAgent {
			return skills[i].SourceAgent < skills[j].SourceAgent
		}
		return skills[i].Name < skills[j].Name
	})

	return skills, nil
}

func discoverSkillsIn(baseDir, relBase, scope string) ([]DiscoveredSkill, error) {
	var skills []DiscoveredSkill

	collectors := []struct {
		pattern     string
		sourceAgent string
		kind        string
		nameFunc    func(match string) string
		readContent bool
	}{
		{
			pattern:     filepath.Join(baseDir, ".claude", "skills", "*", "SKILL.md"),
			sourceAgent: "claude-code",
			kind:        "skill",
			nameFunc:    func(match string) string { return filepath.Base(filepath.Dir(match)) },
		},
		{
			pattern:     filepath.Join(baseDir, ".claude", "commands", "*.md"),
			sourceAgent: "claude-code",
			kind:        "command",
			nameFunc:    func(match string) string { return strings.TrimSuffix(filepath.Base(match), ".md") },
		},
		{
			pattern:     filepath.Join(baseDir, ".codex", "skills", "*", "SKILL.md"),
			sourceAgent: "codex",
			kind:        "skill",
			nameFunc:    func(match string) string { return filepath.Base(filepath.Dir(match)) },
		},
		{
			pattern:     filepath.Join(baseDir, ".gemini", "agents", "*.md"),
			sourceAgent: "gemini-cli",
			kind:        "agent-def",
			readContent: true,
			nameFunc:    func(match string) string { return strings.TrimSuffix(filepath.Base(match), ".md") },
		},
		{
			pattern:     filepath.Join(baseDir, ".gemini", "commands", "*.md"),
			sourceAgent: "gemini-cli",
			kind:        "command",
			readContent: true,
			nameFunc:    func(match string) string { return strings.TrimSuffix(filepath.Base(match), ".md") },
		},
	}

	for _, c := range collectors {
		matches, err := filepath.Glob(c.pattern)
		if err != nil {
			return nil, fmt.Errorf("globbing %s: %w", c.pattern, err)
		}

		for _, match := range matches {
			name := c.nameFunc(match)

			if c.readContent {
				content, err := os.ReadFile(match) //nolint:gosec // match comes from filepath.Glob, not user input
				if err != nil {
					return nil, fmt.Errorf("reading %s: %w", match, err)
				}
				if yamlName := extractYAMLName(string(content)); yamlName != "" {
					name = yamlName
				}
			}

			relPath, err := filepath.Rel(relBase, match)
			if err != nil {
				return nil, fmt.Errorf("computing relative path for %s: %w", match, err)
			}

			skills = append(skills, DiscoveredSkill{
				Name:        name,
				SourceAgent: c.sourceAgent,
				Path:        relPath,
				Kind:        c.kind,
				Scope:       scope,
			})
		}
	}

	return skills, nil
}

// discoverPluginSkills scans the Claude plugin cache for skills and commands.
// Directory structure: {cacheDir}/{org}/{plugin-name}/{version}/skills/*/SKILL.md
// For each plugin, the latest version directory (by sort order) is used.
// Missing or unreadable directories are silently skipped.
func discoverPluginSkills(cacheDir string) []DiscoveredSkill {
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		return nil
	}

	var skills []DiscoveredSkill

	orgs, err := os.ReadDir(cacheDir)
	if err != nil {
		return nil
	}

	for _, org := range orgs {
		if !org.IsDir() {
			continue
		}
		orgDir := filepath.Join(cacheDir, org.Name())
		plugins, err := os.ReadDir(orgDir)
		if err != nil {
			continue
		}

		for _, plugin := range plugins {
			if !plugin.IsDir() {
				continue
			}
			pluginDir := filepath.Join(orgDir, plugin.Name())
			latestDir := latestVersionDir(pluginDir)
			if latestDir == "" {
				continue
			}

			skills = append(skills, discoverInPluginVersion(latestDir)...)
		}
	}

	return skills
}

// latestVersionDir returns the path of the latest version subdirectory.
func latestVersionDir(pluginDir string) string {
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		return ""
	}

	var latest string
	for _, e := range entries {
		if e.IsDir() {
			latest = e.Name() // relies on ReadDir returning sorted order
		}
	}
	if latest == "" {
		return ""
	}
	return filepath.Join(pluginDir, latest)
}

// discoverInPluginVersion finds skills, commands, and agent-defs in a plugin version directory.
func discoverInPluginVersion(versionDir string) []DiscoveredSkill {
	var skills []DiscoveredSkill

	// Skills: {versionDir}/skills/{name}/SKILL.md
	skillsDir := filepath.Join(versionDir, "skills")
	if entries, err := os.ReadDir(skillsDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			skillFile := filepath.Join(skillsDir, e.Name(), "SKILL.md")
			if _, err := os.Stat(skillFile); err == nil {
				skills = append(skills, DiscoveredSkill{
					Name:        e.Name(),
					SourceAgent: "claude-code",
					Path:        skillFile,
					Kind:        "skill",
					Scope:       ScopePlugin,
				})
			}
		}
	}

	// Commands: {versionDir}/commands/{name}.md
	cmdsDir := filepath.Join(versionDir, "commands")
	if matches, err := filepath.Glob(filepath.Join(cmdsDir, "*.md")); err == nil {
		for _, match := range matches {
			skills = append(skills, DiscoveredSkill{
				Name:        strings.TrimSuffix(filepath.Base(match), ".md"),
				SourceAgent: "claude-code",
				Path:        match,
				Kind:        "command",
				Scope:       ScopePlugin,
			})
		}
	}

	// Agent definitions: {versionDir}/agents/{name}.md
	agentsDir := filepath.Join(versionDir, "agents")
	if matches, err := filepath.Glob(filepath.Join(agentsDir, "*.md")); err == nil {
		for _, match := range matches {
			skills = append(skills, DiscoveredSkill{
				Name:        strings.TrimSuffix(filepath.Base(match), ".md"),
				SourceAgent: "claude-code",
				Path:        match,
				Kind:        "agent-def",
				Scope:       ScopePlugin,
			})
		}
	}

	return skills
}

// extractYAMLName looks for a name field in YAML frontmatter delimited by "---".
func extractYAMLName(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}

	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			break
		}
		if strings.HasPrefix(trimmed, "name:") {
			value := strings.TrimPrefix(trimmed, "name:")
			return strings.TrimSpace(value)
		}
	}

	return ""
}
