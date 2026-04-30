package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/go-git/go-git/v6/plumbing"
)

const (
	whyTimeMaxWidth          = 10
	whyAuthorMaxWidth        = 16
	whyCommitColumnWidth     = 10
	whyCheckpointColumnWidth = 12
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
		"%s %s %s %s %*s %s\n",
		whyStaticColumn("TIME", whyTimeMaxWidth),
		whyStaticColumn("AUTHOR", whyAuthorMaxWidth),
		whyStaticColumn("COMMIT", whyCommitColumnWidth),
		whyStaticColumn("CHECKPOINT", whyCheckpointColumnWidth),
		lineWidth,
		"LINE",
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
			"%s %s %s %s %*d %s\n",
			whyStaticColumn(whyStaticTime(row), whyTimeMaxWidth),
			whyStaticColumn(whyStaticAuthor(row), whyAuthorMaxWidth),
			whyStaticColumn(whyStaticCommit(row), whyCommitColumnWidth),
			whyStaticColumn(whyStaticCheckpoint(info), whyCheckpointColumnWidth),
			lineWidth,
			row.FinalLine,
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

func whyStaticTime(row whyBlameRow) string {
	if row.AuthorTime.IsZero() {
		return "-"
	}
	return truncateDisplayWidth(timeAgo(row.AuthorTime), whyTimeMaxWidth, "...")
}

func whyStaticAuthor(row whyBlameRow) string {
	if row.Author == "" {
		return "-"
	}
	return truncateDisplayWidth(row.Author, whyAuthorMaxWidth, "...")
}

func whyStaticCommit(row whyBlameRow) string {
	if row.CommitHash == "" {
		return "-"
	}
	return truncateDisplayWidth(row.CommitHash, whyCommitColumnWidth, "")
}

func whyStaticCheckpoint(info whyCommitInfo) string {
	if info.CheckpointID.IsEmpty() {
		return "-"
	}
	return info.CheckpointID.String()
}

func whyStaticColumn(value string, width int) string {
	value = truncateDisplayWidth(value, width, "...")
	value += strings.Repeat(" ", max(width-lipgloss.Width(value), 0))
	return value
}
