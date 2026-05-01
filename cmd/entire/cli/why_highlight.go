package cli

import (
	"bytes"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

const (
	whyHighlightStyleName = "github-dark"
	whyTabWidth           = 4
)

func highlightWhyCodeLines(filename string, lines []string, colorEnabled bool, maxWidth int) []string {
	if len(lines) == 0 {
		return nil
	}
	lines = expandWhyCodeTabs(lines)
	plain := plainWhyCodeLines(lines, maxWidth)
	if !colorEnabled {
		return plain
	}

	lexer := lexers.Match(filename)
	if lexer == nil || lexer == lexers.Fallback {
		return plain
	}
	lexer = chroma.Coalesce(lexer)

	iterator, err := lexer.Tokenise(nil, strings.Join(lines, "\n"))
	if err != nil {
		return plain
	}

	tokenLines := chroma.SplitTokensIntoLines(iterator.Tokens())
	highlighted := make([]string, len(lines))
	copy(highlighted, plain)
	for i := 0; i < len(lines) && i < len(tokenLines); i++ {
		rendered, ok := renderWhyHighlightedTokenLine(tokenLines[i], maxWidth)
		if !ok {
			continue
		}
		highlighted[i] = rendered
	}
	return highlighted
}

func expandWhyCodeTabs(lines []string) []string {
	tab := strings.Repeat(" ", whyTabWidth)
	expanded := make([]string, len(lines))
	for i, line := range lines {
		expanded[i] = strings.ReplaceAll(line, "\t", tab)
	}
	return expanded
}

func plainWhyCodeLines(lines []string, maxWidth int) []string {
	plain := make([]string, len(lines))
	for i, line := range lines {
		if maxWidth > 0 {
			line = truncateDisplayWidth(line, maxWidth, "")
		}
		plain[i] = line
	}
	return plain
}

func renderWhyHighlightedTokenLine(tokens []chroma.Token, maxWidth int) (string, bool) {
	if len(tokens) == 0 {
		return "", true
	}

	lineTokens := make([]chroma.Token, 0, len(tokens))
	width := 0
	for _, token := range tokens {
		token.Value = strings.TrimSuffix(token.Value, "\n")
		if token.Value == "" {
			continue
		}
		if maxWidth > 0 {
			remaining := maxWidth - width
			if remaining <= 0 {
				break
			}
			token.Value = truncateDisplayWidth(token.Value, remaining, "")
			if token.Value == "" {
				break
			}
			width += lipgloss.Width(token.Value)
		}
		lineTokens = append(lineTokens, token)
	}
	if len(lineTokens) == 0 {
		return "", true
	}

	var buf bytes.Buffer
	if err := formatters.TTY16m.Format(&buf, whyHighlightStyle(), chroma.Literator(lineTokens...)); err != nil {
		return "", false
	}
	return buf.String(), true
}

func whyHighlightStyle() *chroma.Style {
	style := styles.Get(whyHighlightStyleName)
	if style == nil {
		style = styles.Fallback
	}
	return style
}
