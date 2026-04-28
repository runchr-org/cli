package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/stringutil"

	"github.com/go-git/go-git/v6/plumbing"
)

const (
	whyCommitColumnWidth     = 8
	whyAuthorColumnWidth     = 16
	whyCheckpointColumnWidth = 12
	whyAgentColumnWidth      = 14
)

type whyViewData struct {
	GitPath string
	Rows    []whyBlameRow
	Blocks  []whyBlameBlock
	Commits map[plumbing.Hash]whyCommitInfo
}

func renderWhyStatic(data whyViewData) string {
	var sb strings.Builder

	lineWidth := whyLineColumnWidth(data.Rows)
	fmt.Fprintf(
		&sb,
		"%*s %-*s %-*s %-*s %-*s %s\n",
		lineWidth,
		"LINE",
		whyCommitColumnWidth,
		"COMMIT",
		whyAuthorColumnWidth,
		"AUTHOR",
		whyCheckpointColumnWidth,
		"CHECKPOINT",
		whyAgentColumnWidth,
		"AGENT",
		"CODE",
	)

	for _, row := range data.Rows {
		hash := plumbing.NewHash(row.CommitHash)
		info, ok := data.Commits[hash]
		if !ok {
			info = whyCommitInfo{Hash: hash}
		}

		fmt.Fprintf(
			&sb,
			"%*d %-*s %-*s %-*s %-*s %s\n",
			lineWidth,
			row.FinalLine,
			whyCommitColumnWidth,
			whyStaticCommit(hash),
			whyAuthorColumnWidth,
			whyStaticColumn(whyStaticAuthor(row, info), whyAuthorColumnWidth),
			whyCheckpointColumnWidth,
			whyStaticCheckpoint(info),
			whyAgentColumnWidth,
			whyStaticColumn(whyStaticAgent(info), whyAgentColumnWidth),
			row.Source,
		)
	}

	return sb.String()
}

func whyLineColumnWidth(rows []whyBlameRow) int {
	width := len("LINE")
	for _, row := range rows {
		lineWidth := len(strconv.Itoa(row.FinalLine))
		if lineWidth > width {
			width = lineWidth
		}
	}
	return width
}

func whyStaticCommit(hash plumbing.Hash) string {
	full := hash.String()
	if len(full) <= whyCommitColumnWidth {
		return full
	}
	return full[:whyCommitColumnWidth]
}

func whyStaticAuthor(row whyBlameRow, info whyCommitInfo) string {
	if info.Author != "" {
		return info.Author
	}
	if row.Author != "" {
		return row.Author
	}
	return "-"
}

func whyStaticCheckpoint(info whyCommitInfo) string {
	if info.CheckpointID.IsEmpty() {
		return "-"
	}
	return info.CheckpointID.String()
}

func whyStaticAgent(info whyCommitInfo) string {
	if info.Checkpoint.Agent == "" {
		return "-"
	}
	return string(info.Checkpoint.Agent)
}

func whyStaticColumn(value string, width int) string {
	if value == "" {
		value = "-"
	}
	return stringutil.TruncateRunes(value, width, "...")
}
