package cli

import (
	"bytes"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

func highlightWhyCodeLines(filename string, lines []string, colorEnabled bool) []string {
	if len(lines) == 0 {
		return nil
	}
	plain := append([]string(nil), lines...)
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
		rendered, ok := renderWhyHighlightedTokenLine(tokenLines[i])
		if !ok {
			continue
		}
		highlighted[i] = rendered
	}
	return highlighted
}

func renderWhyHighlightedTokenLine(tokens []chroma.Token) (string, bool) {
	if len(tokens) == 0 {
		return "", true
	}

	lineTokens := make([]chroma.Token, 0, len(tokens))
	for _, token := range tokens {
		token.Value = strings.TrimSuffix(token.Value, "\n")
		if token.Value == "" {
			continue
		}
		lineTokens = append(lineTokens, token)
	}
	if len(lineTokens) == 0 {
		return "", true
	}

	style := styles.Get("github")
	if style == nil {
		style = styles.Fallback
	}

	var buf bytes.Buffer
	if err := formatters.TTY16m.Format(&buf, style, chroma.Literator(lineTokens...)); err != nil {
		return "", false
	}
	return buf.String(), true
}
