package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/strategy"
	"github.com/entireio/cli/cmd/entire/cli/stringutil"
	"github.com/entireio/cli/cmd/entire/cli/summarize"
	"github.com/entireio/cli/redact"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/spf13/cobra"
)

const defaultSkillGenerationTimeout = 2 * time.Minute

var (
	skillGenerationTimeout        = defaultSkillGenerationTimeout
	skillNameFrontmatterRE        = regexp.MustCompile(`(?m)^name:\s*\S+`)
	skillDescriptionFrontmatterRE = regexp.MustCompile(`(?m)^description:\s*\S+`)
)

type skillGenerateOptions struct {
	SessionID string
	Output    string
	Force     bool
}

type skillSource struct {
	SessionID    string
	Description  string
	AgentType    types.AgentType
	Model        string
	FilesTouched []string
	Transcript   []byte
}

func newSkillGenerateCmd() *cobra.Command {
	var opts skillGenerateOptions

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate a skill draft from a session",
		Long: `Generate a reusable skill draft from an Entire session.

The command reads the session transcript, condenses it, and asks the configured
summary provider to produce a Codex-compatible SKILL.md.

Examples:
  entire skill generate
  entire skill generate --session <session-id>
  entire skill generate --session <session-id> --output ./my-skill
  entire skill generate --session <session-id> --force`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if checkDisabledGuard(cmd.Context(), cmd.OutOrStdout()) {
				return nil
			}
			return runSkillGenerate(cmd.Context(), cmd.OutOrStdout(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.SessionID, "session", "", "Session ID or prefix to generate from")
	cmd.Flags().StringVarP(&opts.Output, "output", "o", "", "Output skill directory (default: ./<session-description>-skill)")
	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Overwrite an existing output directory")

	return cmd
}

func runSkillGenerate(ctx context.Context, w io.Writer, opts skillGenerateOptions) error {
	sessionID := strings.TrimSpace(opts.SessionID)
	if sessionID == "" {
		sessionID = strategy.FindMostRecentSession(ctx)
		if sessionID == "" {
			return errors.New("no current session found in this worktree; pass --session <id> to generate from a specific session")
		}
		fmt.Fprintf(w, "Using current session: %s\n", sessionID)
	}

	source, err := loadSkillSourceForSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if len(source.Transcript) == 0 {
		return fmt.Errorf("session %s has no transcript data to generate from", source.SessionID)
	}

	provider, err := resolveSkillGenerationProvider(ctx, w)
	if err != nil {
		return err
	}
	if provider.TextGenerator == nil {
		return fmt.Errorf("provider %s does not support text generation", provider.DisplayName)
	}

	skillMarkdown, err := generateSkillMarkdown(ctx, source, provider)
	if err != nil {
		return err
	}

	outputDir := opts.Output
	if outputDir == "" {
		outputDir = defaultSkillOutputDir(source)
	}
	outputDir, err = filepath.Abs(outputDir)
	if err != nil {
		return fmt.Errorf("resolving output path: %w", err)
	}

	if err := writeGeneratedSkill(outputDir, skillMarkdown, opts.Force); err != nil {
		return err
	}

	fmt.Fprintf(w, "Generated skill draft: %s\n", filepath.Join(outputDir, "SKILL.md"))
	fmt.Fprint(w, formatSummaryProviderDetails(provider))
	return nil
}

func loadSkillSourceForSession(ctx context.Context, sessionIDPrefix string) (*skillSource, error) {
	sess, err := strategy.GetSession(ctx, sessionIDPrefix)
	if err != nil {
		return nil, fmt.Errorf("finding session %q: %w", sessionIDPrefix, err)
	}

	if len(sess.Checkpoints) > 0 {
		return loadSkillSourceFromCommittedSession(ctx, sess)
	}

	return loadSkillSourceFromActiveSession(ctx, sess)
}

func loadSkillSourceFromCommittedSession(ctx context.Context, sess *strategy.Session) (*skillSource, error) {
	checkpoints := append([]strategy.Checkpoint(nil), sess.Checkpoints...)
	sort.Slice(checkpoints, func(i, j int) bool {
		return checkpoints[i].Timestamp.After(checkpoints[j].Timestamp)
	})

	repo, err := openRepository(ctx)
	if err != nil {
		return nil, fmt.Errorf("opening repository: %w", err)
	}

	v1Store := checkpoint.NewGitStore(repo)
	v2Store := checkpoint.NewV2GitStore(repo, "")
	preferV2 := settings.IsCheckpointsV2Enabled(ctx)

	var lastErr error
	for _, cp := range checkpoints {
		if cp.CheckpointID.IsEmpty() {
			continue
		}
		reader, summary, err := checkpoint.ResolveCommittedReaderForCheckpoint(ctx, cp.CheckpointID, v1Store, v2Store, preferV2)
		if err != nil {
			lastErr = err
			continue
		}
		content, err := readSessionContentByIDFromReader(ctx, reader, cp.CheckpointID, sess.ID, summary)
		if err != nil {
			lastErr = err
			continue
		}
		return &skillSource{
			SessionID:    sess.ID,
			Description:  sess.Description,
			AgentType:    content.Metadata.Agent,
			Model:        content.Metadata.Model,
			FilesTouched: content.Metadata.FilesTouched,
			Transcript:   content.Transcript,
		}, nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("reading session transcript: %w", lastErr)
	}
	return nil, fmt.Errorf("session %s has no committed checkpoints with transcript data", sess.ID)
}

func readSessionContentByIDFromReader(ctx context.Context, reader checkpoint.CommittedReader, cpID id.CheckpointID, sessionID string, summary *checkpoint.CheckpointSummary) (*checkpoint.SessionContent, error) {
	if summary == nil || len(summary.Sessions) == 0 {
		return nil, checkpoint.ErrCheckpointNotFound
	}
	var lastErr error
	for i := range summary.Sessions {
		content, err := reader.ReadSessionContent(ctx, cpID, i)
		if err != nil {
			lastErr = err
			continue
		}
		if content.Metadata.SessionID == sessionID {
			return content, nil
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("session %s not found in checkpoint %s", sessionID, cpID)
}

func loadSkillSourceFromActiveSession(ctx context.Context, sess *strategy.Session) (*skillSource, error) {
	state, err := strategy.LoadSessionState(ctx, sess.ID)
	if err != nil {
		return nil, fmt.Errorf("loading session state: %w", err)
	}
	if state == nil {
		return nil, fmt.Errorf("session %s has no committed checkpoints and no active session state", sess.ID)
	}

	repo, err := openRepository(ctx)
	if err != nil {
		return nil, fmt.Errorf("opening repository: %w", err)
	}

	shadowBranchName := checkpoint.ShadowBranchNameForCommit(state.BaseCommit, state.WorktreeID)
	ref, err := repo.Reference(plumbing.NewBranchReferenceName(shadowBranchName), true)
	if err != nil {
		return nil, fmt.Errorf("reading shadow branch %s: %w", shadowBranchName, err)
	}
	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Errorf("reading shadow commit: %w", err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("reading shadow tree: %w", err)
	}

	transcriptPath := filepath.ToSlash(filepath.Join(paths.SessionMetadataDirFromSessionID(sess.ID), paths.TranscriptFileName))
	file, err := tree.File(transcriptPath)
	if err != nil {
		return nil, fmt.Errorf("session %s has no transcript on shadow branch %s: %w", sess.ID, shadowBranchName, err)
	}
	transcript, err := file.Contents()
	if err != nil {
		return nil, fmt.Errorf("reading transcript from shadow branch: %w", err)
	}

	return &skillSource{
		SessionID:    sess.ID,
		Description:  sess.Description,
		AgentType:    state.AgentType,
		Model:        state.ModelName,
		FilesTouched: state.FilesTouched,
		Transcript:   []byte(transcript),
	}, nil
}

func generateSkillMarkdown(ctx context.Context, source *skillSource, provider *checkpointSummaryProvider) (string, error) {
	condensed, err := summarize.BuildCondensedTranscriptFromBytes(redact.AlreadyRedacted(source.Transcript), source.AgentType)
	if err != nil {
		return "", fmt.Errorf("condensing transcript: %w", err)
	}
	if len(condensed) == 0 {
		return "", errors.New("session transcript has no content to generate from")
	}

	prompt := buildSkillGenerationPrompt(source, summarize.FormatCondensedTranscript(summarize.Input{
		Transcript:   condensed,
		FilesTouched: source.FilesTouched,
	}))

	timeoutCtx, cancel := context.WithTimeout(ctx, skillGenerationTimeout)
	defer cancel()

	result, err := provider.TextGenerator.GenerateText(timeoutCtx, prompt, provider.Model)
	if err != nil {
		if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("skill generation timed out after %s: %w", skillGenerationTimeout, err)
		}
		return "", fmt.Errorf("provider text generation failed: %w", err)
	}

	result = stripMarkdownFence(strings.TrimSpace(result))
	if err := validateGeneratedSkillMarkdown(result); err != nil {
		return "", err
	}
	return result, nil
}

func buildSkillGenerationPrompt(source *skillSource, condensedTranscript string) string {
	var b strings.Builder
	fmt.Fprintln(&b, "Generate a Codex-compatible SKILL.md from the development session below.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Return only the complete SKILL.md content. Do not wrap it in a Markdown fence.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Requirements:")
	fmt.Fprintln(&b, "- Start with YAML frontmatter containing name and description.")
	fmt.Fprintln(&b, "- Make the skill concise and reusable, not a session recap.")
	fmt.Fprintln(&b, "- Preserve durable workflow, validation, codebase, and tool-use lessons.")
	fmt.Fprintln(&b, "- Do not include secrets, private credentials, raw logs, or unnecessary transcript detail.")
	fmt.Fprintln(&b, "- Do not create extra files or reference bundled resources unless they are essential.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Session metadata:")
	fmt.Fprintf(&b, "- Session ID: %s\n", source.SessionID)
	if source.Description != "" && source.Description != strategy.NoDescription {
		fmt.Fprintf(&b, "- Description: %s\n", source.Description)
	}
	if source.AgentType != "" {
		fmt.Fprintf(&b, "- Agent: %s\n", source.AgentType)
	}
	if source.Model != "" {
		fmt.Fprintf(&b, "- Model: %s\n", source.Model)
	}
	if len(source.FilesTouched) > 0 {
		fmt.Fprintf(&b, "- Files touched: %s\n", strings.Join(source.FilesTouched, ", "))
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Condensed transcript:")
	fmt.Fprintln(&b, condensedTranscript)
	return b.String()
}

func validateGeneratedSkillMarkdown(content string) error {
	if content == "" {
		return errors.New("provider returned empty skill content")
	}
	if !strings.HasPrefix(content, "---\n") {
		return errors.New("provider returned invalid skill content: missing YAML frontmatter")
	}
	if !strings.Contains(content[4:], "\n---\n") {
		return errors.New("provider returned invalid skill content: frontmatter is not closed")
	}
	if !skillNameFrontmatterRE.MatchString(content) {
		return errors.New("provider returned invalid skill content: frontmatter missing name")
	}
	if !skillDescriptionFrontmatterRE.MatchString(content) {
		return errors.New("provider returned invalid skill content: frontmatter missing description")
	}
	return nil
}

func stripMarkdownFence(content string) string {
	if !strings.HasPrefix(content, "```") {
		return content
	}
	lines := strings.Split(content, "\n")
	if len(lines) < 3 {
		return content
	}
	if strings.HasPrefix(lines[0], "```") && strings.TrimSpace(lines[len(lines)-1]) == "```" {
		return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
	}
	return content
}

func defaultSkillOutputDir(source *skillSource) string {
	base := source.Description
	if base == "" || base == strategy.NoDescription {
		base = source.SessionID
	}
	slug := slugifySkillName(base)
	if slug == "" {
		slug = "generated"
	}
	return slug + "-skill"
}

func slugifySkillName(s string) string {
	s = strings.ToLower(stringutil.CollapseWhitespace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
		if b.Len() >= 48 {
			break
		}
	}
	return strings.Trim(strings.TrimSpace(b.String()), "-")
}

func writeGeneratedSkill(outputDir, skillMarkdown string, force bool) error {
	if outputDir == "" {
		return errors.New("output directory cannot be empty")
	}
	if info, err := os.Stat(outputDir); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("output path exists and is not a directory: %s", outputDir)
		}
		entries, readErr := os.ReadDir(outputDir)
		if readErr != nil {
			return fmt.Errorf("reading output directory: %w", readErr)
		}
		if len(entries) > 0 && !force {
			return fmt.Errorf("output directory is not empty: %s (use --force to overwrite SKILL.md)", outputDir)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking output directory: %w", err)
	}

	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	target := filepath.Join(outputDir, "SKILL.md")
	if _, err := os.Stat(target); err == nil && !force {
		return fmt.Errorf("SKILL.md already exists: %s (use --force to overwrite)", target)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("checking SKILL.md: %w", err)
	}

	if !strings.HasSuffix(skillMarkdown, "\n") {
		skillMarkdown += "\n"
	}
	if err := os.WriteFile(target, []byte(skillMarkdown), 0o600); err != nil {
		return fmt.Errorf("writing SKILL.md: %w", err)
	}
	return nil
}
