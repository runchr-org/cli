package claudecode

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/skilldiscovery"
	"github.com/entireio/cli/cmd/entire/cli/logging"
)

// DiscoverReviewSkills walks ~/.claude/plugins/cache/ and ~/.claude/skills/
// for SKILL.md files whose name or description indicates review-adjacent
// intent. Returns (nil, nil) when HOME is unreadable or directories are
// missing — discovery is best-effort.
//
//nolint:unparam // error return is part of SkillDiscoverer contract; future implementations may report hard failures
func (c *ClaudeCodeAgent) DiscoverReviewSkills(ctx context.Context) ([]agent.DiscoveredSkill, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		logging.Debug(ctx, "claude-code discovery: UserHomeDir failed", slog.String("error", err.Error()))
		return nil, nil
	}

	var found []agent.DiscoveredSkill
	found = append(found, scanPluginCache(ctx, filepath.Join(home, ".claude", "plugins", "cache"))...)
	found = append(found, scanUserSkills(ctx, filepath.Join(home, ".claude", "skills"))...)
	if len(found) == 0 {
		return nil, nil
	}
	return found, nil
}

// scanPluginCache walks <root>/<marketplace>/<plugin>/<version>/skills/<skill>/SKILL.md
func scanPluginCache(ctx context.Context, root string) []agent.DiscoveredSkill {
	entries, err := os.ReadDir(root)
	if err != nil {
		logging.Debug(ctx, "claude-code discovery: plugin cache unreadable",
			slog.String("root", root), slog.String("error", err.Error()))
		return nil
	}
	var found []agent.DiscoveredSkill
	for _, marketEntry := range entries {
		if !marketEntry.IsDir() {
			continue
		}
		marketRoot := filepath.Join(root, marketEntry.Name())
		pluginEntries, err := os.ReadDir(marketRoot)
		if err != nil {
			continue
		}
		for _, pluginEntry := range pluginEntries {
			if !pluginEntry.IsDir() {
				continue
			}
			pluginName := pluginEntry.Name()
			pluginRoot := filepath.Join(marketRoot, pluginName)
			versionEntries, err := os.ReadDir(pluginRoot)
			if err != nil {
				continue
			}
			for _, verEntry := range versionEntries {
				if !verEntry.IsDir() {
					continue
				}
				skillsRoot := filepath.Join(pluginRoot, verEntry.Name(), "skills")
				found = append(found, readSkillsDir(ctx, skillsRoot, pluginName)...)
			}
		}
	}
	return found
}

// scanUserSkills walks ~/.claude/skills/<skill>/SKILL.md.
func scanUserSkills(ctx context.Context, root string) []agent.DiscoveredSkill {
	return readSkillsDir(ctx, root, "" /* no plugin prefix */)
}

// readSkillsDir reads each skill subdirectory's SKILL.md, parses frontmatter,
// and emits a DiscoveredSkill if Matches() returns true.
func readSkillsDir(ctx context.Context, dir, pluginName string) []agent.DiscoveredSkill {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var found []agent.DiscoveredSkill
	for _, skillEntry := range entries {
		if !skillEntry.IsDir() {
			continue
		}
		skillDir := filepath.Join(dir, skillEntry.Name())
		skillFile := filepath.Join(skillDir, "SKILL.md")
		data, err := os.ReadFile(skillFile) //nolint:gosec // G304: skillFile is constructed from a ReadDir walk under HOME, not user input
		if err != nil {
			continue
		}
		name, description, parseErr := parseSkillFrontmatter(data)
		if parseErr != nil {
			logging.Debug(ctx, "claude-code discovery: skipping malformed SKILL.md",
				slog.String("path", skillFile), slog.String("error", parseErr.Error()))
			continue
		}
		if name == "" {
			name = skillEntry.Name()
		}
		invocation := "/" + name
		if pluginName != "" {
			invocation = "/" + pluginName + ":" + name
		}
		if !skilldiscovery.Matches(invocation, description) {
			continue
		}
		found = append(found, agent.DiscoveredSkill{
			Name:        invocation,
			Description: description,
			SourcePath:  skillFile,
		})
	}
	return found
}

// parseSkillFrontmatter extracts `name:` and `description:` from a minimal
// YAML frontmatter block. Purpose-built for the tiny subset of YAML these
// SKILL.md files actually use.
func parseSkillFrontmatter(data []byte) (name, description string, err error) {
	s := string(data)
	if !strings.HasPrefix(s, "---\n") && !strings.HasPrefix(s, "---\r\n") {
		return "", "", errors.New("no frontmatter delimiter")
	}
	body := strings.TrimPrefix(strings.TrimPrefix(s, "---\r\n"), "---\n")
	end := strings.Index(body, "\n---")
	if end < 0 {
		return "", "", errors.New("no closing frontmatter delimiter")
	}
	for _, line := range strings.Split(body[:end], "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		} else if strings.HasPrefix(line, "description:") {
			description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		}
	}
	return name, description, nil
}
