package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/go-git/go-git/v6/plumbing"
)

const (
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
		"%*s %-*s %s\n",
		lineWidth,
		"LINE",
		whyCheckpointColumnWidth,
		"CHECKPOINT",
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
			"%*d %-*s %s\n",
			lineWidth,
			row.FinalLine,
			whyCheckpointColumnWidth,
			whyStaticCheckpoint(info),
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

func whyStaticCheckpoint(info whyCommitInfo) string {
	if info.CheckpointID.IsEmpty() {
		return "-"
	}
	return info.CheckpointID.String()
}
