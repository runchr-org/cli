package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/interactive"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/perf"
	"github.com/spf13/cobra"
)

type whyOptions struct {
	Path string
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
			if opts.Path != "" && !canRunWhyTUI(cmd.OutOrStdout()) {
				cleanup := initWhyLogging(cmd.Context())
				defer cleanup()
			}
			return runWhy(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts)
		},
	}

	return cmd
}

func runWhy(ctx context.Context, w io.Writer, _ io.Writer, opts whyOptions) (err error) {
	ctx, span := perf.Start(ctx, "why",
		slog.String("path", opts.Path),
		slog.Bool("has_path", opts.Path != ""))
	defer func() {
		span.RecordError(err)
		span.End()
	}()

	_, modeSpan := perf.Start(ctx, "detect_mode")
	canUseTUI := canRunWhyTUI(w)
	modeSpan.End()
	if opts.Path == "" {
		if !canUseTUI {
			return errors.New("path required when not running interactively")
		}
		return errors.New("interactive file browser is not implemented yet")
	}
	if canUseTUI {
		return errors.New("interactive why overview is not implemented yet")
	}

	_, resolveSpan := perf.Start(ctx, "resolve_path")
	repoRoot, gitPath, _, err := resolveWhyPath(ctx, opts.Path)
	if err != nil {
		resolveSpan.RecordError(err)
		resolveSpan.End()
		return err
	}
	resolveSpan.End()

	data, err := loadWhyViewData(ctx, repoRoot, gitPath)
	if err != nil {
		return err
	}

	_, renderSpan := perf.Start(ctx, "render_static")
	content := renderWhyStatic(data)
	renderSpan.End()

	_, outputSpan := perf.Start(ctx, "write_output")
	outputExplainContent(w, content, false)
	outputSpan.End()
	return nil
}

var canRunWhyTUI = defaultCanRunWhyTUI

func defaultCanRunWhyTUI(w io.Writer) bool {
	return !IsAccessibleMode() && interactive.IsTerminalWriter(w) && interactive.CanPromptInteractively()
}

func initWhyLogging(ctx context.Context) func() {
	if _, err := paths.WorktreeRoot(ctx); err != nil {
		return func() {}
	}
	logging.SetLogLevelGetter(GetLogLevel)
	if err := logging.Init(ctx, ""); err != nil {
		return func() {}
	}
	return logging.Close
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

func loadWhyViewData(ctx context.Context, repoRoot, gitPath string) (whyViewData, error) {
	_, blameSpan := perf.Start(ctx, "git_blame")
	blameOutput, err := runGitBlame(ctx, repoRoot, gitPath)
	if err != nil {
		blameSpan.RecordError(err)
		blameSpan.End()
		return whyViewData{}, err
	}
	blameSpan.End()

	_, parseSpan := perf.Start(ctx, "parse_blame")
	lines, err := parseBlamePorcelain(blameOutput)
	if err != nil {
		parseSpan.RecordError(err)
		parseSpan.End()
		return whyViewData{}, fmt.Errorf("parse git blame output: %w", err)
	}
	parseSpan.End()

	_, buildRowsSpan := perf.Start(ctx, "build_rows")
	blocks := collapseWhyBlameBlocks(lines)
	rows := buildWhyBlameRows(lines, blocks)
	buildRowsSpan.End()

	_, openRepoSpan := perf.Start(ctx, "open_repository")
	repo, err := openRepository(ctx)
	if err != nil {
		openRepoSpan.RecordError(err)
		openRepoSpan.End()
		return whyViewData{}, fmt.Errorf("open repository: %w", err)
	}
	openRepoSpan.End()

	_, lookupSpan := perf.Start(ctx, "init_checkpoint_lookup")
	lookup, err := newWhyCheckpointLookup(ctx, repo)
	if err != nil {
		lookupSpan.RecordError(err)
		lookupSpan.End()
		return whyViewData{}, fmt.Errorf("initialize checkpoint lookup: %w", err)
	}
	lookupSpan.End()

	_, enrichSpan := perf.Start(ctx, "enrich_commits")
	commits := enrichWhyCommits(ctx, repo, lookup, blocks)
	enrichSpan.End()

	return whyViewData{
		GitPath: gitPath,
		Rows:    rows,
		Blocks:  blocks,
		Commits: commits,
	}, nil
}
