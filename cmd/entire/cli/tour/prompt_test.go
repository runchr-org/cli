package tour

import (
	"fmt"
	"testing"
)

// TestEscapeForTags_NeutralizesClosingTags asserts that every closing
// tag the system prompt wraps content in gets escaped to a backslash
// form regardless of case, interior whitespace, or Unicode bypass
// attempts. Tightening this regex was a security-sensitive change so
// it gets table coverage with explicit \uXXXX escapes — invisible
// Unicode characters render identically to ASCII in source, and
// readers cannot tell what's being asserted without the escapes.
func TestEscapeForTags_NeutralizesClosingTags(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"lowercase state", "</state>", "<\\/state>"},
		{"lowercase commands", "</commands>", "<\\/commands>"},
		{"lowercase labs", "</labs>", "<\\/labs>"},
		{"lowercase post", "</post>", "<\\/post>"},
		{"uppercase preserves case", "</POST>", "<\\/POST>"},
		{"mixed case preserves case", "</Post>", "<\\/Post>"},
		// Whitespace inside the tag is collapsed during escape; the
		// security goal is "no remaining closing-tag pattern in the
		// payload", and `<\/post>` satisfies that regardless of what
		// whitespace the original contained.
		{"trailing whitespace", "</post >", "<\\/post>"},
		{"newline before close", "</post\n>", "<\\/post>"},
		{"tab before close", "</post\t>", "<\\/post>"},
		{"interior whitespace then case", "</  STATE  >", "<\\/STATE>"},
		{"multiple tags in one payload", "before </state> middle </post> end", "before <\\/state> middle <\\/post> end"},
		{"no match leaves payload alone", "no tags here", "no tags here"},
		{"non-target tag is left alone", "</statement>", "</statement>"},
		{"prefix non-match", "</states>", "</states>"},
		{"close-only is required", "<post>", "<post>"},

		// Unicode bypass attempts (codex adversarial-review findings).
		// Zero-width space (U+200B) inside the tag name splits the
		// literal alternation match — stripInvisibles drops \p{Cf}
		// chars before the regex sees the bytes.
		{"zero-width space in tag name", "</po\u200bst>", "<\\/post>"},
		// NO-BREAK SPACE (U+00A0) before the close. Falls in \p{Z}.
		{"no-break space before close", "</post >", "<\\/post>"},
		// Right-to-left mark (U+200F) inside the tag name. \p{Cf}.
		{"rtl mark in tag", "</p\u200fost>", "<\\/post>"},

		// Pass-3 regression: visible Unicode whitespace BETWEEN
		// letters of the tag name. Pass-2's strip only covered
		// \p{Cf}; NBSP (U+00A0) is \p{Zs} and bypassed the escape.
		// Test all four tag names so the fix isn't accidentally
		// post-specific.
		{"NBSP inside post", "</po st>", "<\\/post>"},
		{"NBSP inside state", "</st ate>", "<\\/state>"},
		{"NBSP inside commands", "</com mands>", "<\\/commands>"},
		{"NBSP inside labs", "</la bs>", "<\\/labs>"},
		// Other \p{Z} variants that had the same bypass.
		{"narrow NBSP inside tag name", "</po st>", "<\\/post>"},
		{"ideographic space inside tag name", "</po　st>", "<\\/post>"},
		// Combined visible+invisible attack.
		{"NBSP and ZWSP combined", "</p\u200bo st>", "<\\/post>"},

		// Empty payload should pass through.
		{"empty payload", "", ""},
		// Already-escaped form should not double-escape — the regex
		// requires `</tag>` not `<\/tag>` so the literal backslash
		// breaks the match. (This is a non-match assertion, not a
		// true idempotence proof; see the round-trip test below.)
		{"already-escaped form is left alone", "<\\/post>", "<\\/post>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := string(escapeForTags([]byte(tc.in)))
			if got != tc.want {
				t.Errorf("escapeForTags(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestEscapeForTags_Idempotent asserts the real idempotence property:
// applying escapeForTags twice produces the same result as applying
// it once. Distinct from "already-escaped form is left alone" — that
// only checks one specific shape; this checks the property over a
// representative input.
func TestEscapeForTags_Idempotent(t *testing.T) {
	t.Parallel()
	inputs := []string{
		"</state>",
		"</post >",
		"before </state> middle </post> end",
		"no tags here",
		"",
	}
	for _, in := range inputs {
		t.Run(fmt.Sprintf("%q", in), func(t *testing.T) {
			t.Parallel()
			once := escapeForTags([]byte(in))
			twice := escapeForTags(once)
			if string(once) != string(twice) {
				t.Errorf("escapeForTags(escapeForTags(%q)) = %q, want %q", in, twice, once)
			}
		})
	}
}

// TeststripControlSequences asserts that ANSI escapes, OSC sequences,
// and C0/C1 control bytes are removed from agent output that gets
// piped to disk on --regenerate. A compromised agent could otherwise
// embed terminal-rewriting controls into the committed tour.md and
// have them shipped to every future user of that release.
func TestStripControlSequences(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain markdown unchanged", "## Title\n\n- bullet\n", "## Title\n\n- bullet\n"},
		{"strips CSI red color", "before \x1b[31mred\x1b[0m after", "before red after"},
		{"strips OSC hyperlink", "before \x1b]8;;https://evil\x07click\x1b]8;;\x07 after", "before click after"},
		{"strips bare C0 controls", "before\x00\x07\x08after", "beforeafter"},
		// C1 controls are stripped when input is valid UTF-8 (U+0080-U+009F
		// encoded as 2 bytes). Raw single-byte 0x80-0x9F sequences are
		// invalid UTF-8 and pass through string-based regex unchanged —
		// they also can't form a terminal control sequence in any modern
		// UTF-8 terminal, so passthrough is acceptable.
		{"strips C1 controls", "before\u009bafter", "beforeafter"},
		{"preserves tab/newline/carriage-return", "line1\tcol\nline2\r\n", "line1\tcol\nline2\r\n"},
		{"strips title-rewrite OSC", "ok\x1b]0;malicious title\x07ok", "okok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := stripControlSequences(tc.in)
			if got != tc.want {
				t.Errorf("stripControlSequences(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
