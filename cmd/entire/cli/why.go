package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/interactive"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/spf13/cobra"
)

type whyOptions struct {
	Path        string
	Interactive bool
	NoPager     bool
}

func newWhyCmd() *cobra.Command {
	var opts whyOptions

	cmd := &cobra.Command{
		Use:    "why [path]",
		Short:  "Explain why a file looks the way it does",
		Hidden: true,
		Args:   cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				opts.Path = args[0]
			}
			return runWhy(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.Interactive, "interactive", "i", false, "Force interactive TUI mode")
	cmd.Flags().BoolVar(&opts.NoPager, "no-pager", false, "Disable pager output")

	return cmd
}

func runWhy(ctx context.Context, w io.Writer, _ io.Writer, opts whyOptions) error {
	canUseTUI := canRunWhyTUI(w)
	if opts.Interactive && !canUseTUI {
		return errors.New("interactive mode requires a real terminal")
	}
	if opts.Path == "" {
		if !canUseTUI {
			return errors.New("path required when not running interactively")
		}
		return errors.New("interactive file browser is not implemented yet")
	}
	if _, _, _, err := resolveWhyPath(ctx, opts.Path); err != nil {
		return err
	}
	return errors.New("entire why is not implemented yet")
}

func canRunWhyTUI(w io.Writer) bool {
	return !IsAccessibleMode() && interactive.IsTerminalWriter(w) && interactive.CanPromptInteractively()
}

func resolveWhyPath(ctx context.Context, input string) (string, string, string, error) {
	repoRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		return "", "", "", fmt.Errorf("not a git repository: %w", err)
	}
	repoRoot = normalizeWhyPathForRel(repoRoot)

	absPath := input
	if !filepath.IsAbs(absPath) {
		absPath, err = filepath.Abs(absPath)
		if err != nil {
			return "", "", "", fmt.Errorf("resolving path %q: %w", input, err)
		}
	}
	absPath = normalizeWhyPathForRel(absPath)

	relPath, err := filepath.Rel(repoRoot, absPath)
	if err != nil {
		return "", "", "", fmt.Errorf("resolving path %q relative to repository: %w", input, err)
	}
	if relPath == "." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) || relPath == ".." {
		return "", "", "", fmt.Errorf("path %q is outside the repository", input)
	}

	return repoRoot, filepath.ToSlash(relPath), absPath, nil
}

func normalizeWhyPathForRel(path string) string {
	cleaned := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		return resolved
	}
	dir := filepath.Dir(cleaned)
	base := filepath.Base(cleaned)
	if resolvedDir, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Join(resolvedDir, base)
	}
	return cleaned
}
