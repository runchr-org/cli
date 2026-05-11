// Package learn powers `entire learn` — a state-aware tour of the
// installed CLI, served from a pre-rendered embedded markdown for the
// default path and from a TextGenerator-capable agent for `--regenerate`
// (refreshes the committed embedded template; run by the changelog flow
// before each release).
package learn

import (
	"strings"

	"github.com/spf13/cobra"
)

// CommandNode is one node in the discovered cobra command tree.
//
// The shape mirrors what `entire learn` hands to a TextGenerator: enough
// detail for the model to write recipes for each capability without being
// told to invent any specific command name.
type CommandNode struct {
	Path        string        `json:"path"`
	Name        string        `json:"name"`
	Short       string        `json:"short,omitempty"`
	Long        string        `json:"long,omitempty"`
	Example     string        `json:"example,omitempty"`
	Aliases     []string      `json:"aliases,omitempty"`
	Hidden      bool          `json:"hidden,omitempty"`
	Deprecated  string        `json:"deprecated,omitempty"`
	Subcommands []CommandNode `json:"subcommands,omitempty"`
}

// CommandSurface is the discovered top-level command tree under `entire`.
// Hidden, deprecated, and built-in cobra plumbing (help/completion) are
// stripped — everything in this struct is something we'd render to a user.
type CommandSurface struct {
	Root CommandNode `json:"root"`
}

// Discover walks the cobra command tree rooted at root and returns the
// user-facing surface. Hidden and deprecated commands are excluded.
func Discover(root *cobra.Command) CommandSurface {
	return CommandSurface{Root: walkCommand(root, "")}
}

func walkCommand(cmd *cobra.Command, parentPath string) CommandNode {
	name := cmd.Name()
	path := strings.TrimSpace(parentPath + " " + name)
	if parentPath == "" {
		path = name
	}

	node := CommandNode{
		Path:       path,
		Name:       name,
		Short:      strings.TrimSpace(cmd.Short),
		Long:       trimDescription(cmd.Long),
		Example:    strings.TrimSpace(cmd.Example),
		Aliases:    append([]string(nil), cmd.Aliases...),
		Hidden:     cmd.Hidden,
		Deprecated: strings.TrimSpace(cmd.Deprecated),
	}

	for _, sub := range cmd.Commands() {
		if !shouldRender(sub) {
			continue
		}
		node.Subcommands = append(node.Subcommands, walkCommand(sub, path))
	}
	return node
}

// shouldRender returns true when a cobra command should appear in the
// rendered tour. We exclude:
//   - cobra-built-in plumbing (help/completion) which adds noise without
//     teaching anything Entire-specific
//   - commands explicitly marked Hidden — these are either internal
//     infrastructure (e.g. __send_analytics) or aliases the user is
//     already taught about under their canonical name
//   - deprecated commands — they still work but we don't want to teach
//     them as the recommended path
func shouldRender(cmd *cobra.Command) bool {
	if cmd.Hidden || cmd.Deprecated != "" {
		return false
	}
	switch cmd.Name() {
	case "help", "completion":
		return false
	}
	return true
}

// trimDescription collapses the verbose Long help text to its first
// substantive paragraph. The full help is still available via
// `entire <cmd> --help`; the tour just needs enough to summarize.
func trimDescription(long string) string {
	long = strings.TrimSpace(long)
	if long == "" {
		return ""
	}
	if idx := strings.Index(long, "\n\n"); idx > 0 {
		return strings.TrimSpace(long[:idx])
	}
	return long
}
