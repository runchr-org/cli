package tour

import "testing"

// TestEscapeForTags_NeutralizesClosingTags asserts that every closing
// tag the system prompt wraps content in gets escaped to a backslash
// form regardless of case or interior whitespace. The prompt's threat
// model is "untrusted feed/tree content can't break out of its
// <state>/<commands>/<labs>/<post> tag wrapper" — tightening this
// regex was a security-sensitive change so it gets table coverage.
func TestEscapeForTags_NeutralizesClosingTags(t *testing.T) {
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
		// Zero-width space inside the tag name splits the literal
		// alternation match — we strip invisible chars first so the
		// regex sees `</post>` and escapes it.
		{"zero-width space in tag name", "</po​st>", "<\\/post>"},
		// NO-BREAK SPACE between name and >. \s only matches ASCII; the
		// extended class catches \p{Z}.
		{"no-break space before close", "</post >", "<\\/post>"},
		// Right-to-left mark inside the tag — \p{Cf} format chars are
		// stripped before regex match.
		{"rtl mark in tag", "</p‏ost>", "<\\/post>"},
		// Pass-3 regression: NBSP between letters of the tag name.
		// Pass-2's strip only covered \p{Cf}, so this bypassed.
		// The \p{Z} branch in stripInvisibles now catches it.
		{"NBSP inside tag name", "</po st>", "<\\/post>"},
		{"NARROW NBSP inside tag name", "</po st>", "<\\/post>"},
		{"IDEOGRAPHIC SPACE inside tag name", "</po　st>", "<\\/post>"},
		// Combined visible+invisible attack.
		{"NBSP and ZWSP combined", "</p​o st>", "<\\/post>"},
		// Empty payload should pass through.
		{"empty payload", "", ""},
		// Idempotence: escaping twice equals escaping once.
		{"idempotent on already-escaped", "<\\/post>", "<\\/post>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(escapeForTags([]byte(tc.in)))
			if got != tc.want {
				t.Errorf("escapeForTags(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestStripControlSequences asserts that ANSI escapes, OSC sequences,
// and C0/C1 control bytes are removed from agent output that gets
// piped to disk on --regenerate. A compromised agent could otherwise
// embed terminal-rewriting controls into the committed tour.md and
// have them shipped to every future user of that release.
func TestStripControlSequences(t *testing.T) {
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
		{"strips C1 controls", "beforeafter", "beforeafter"},
		{"preserves tab/newline/carriage-return", "line1\tcol\nline2\r\n", "line1\tcol\nline2\r\n"},
		{"strips title-rewrite OSC", "ok\x1b]0;malicious title\x07ok", "okok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := StripControlSequences(tc.in)
			if got != tc.want {
				t.Errorf("StripControlSequences(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
