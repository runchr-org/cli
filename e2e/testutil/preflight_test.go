package testutil

import "testing"

func TestFindHookDrift(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		out       string
		wantOK    bool
		wantMatch string
	}{
		{
			name:   "clean output passes",
			out:    "✓ Metadata branches: OK\n✓ Codex hook trust: OK\nNo stuck sessions found.\n",
			wantOK: false,
		},
		{
			name:      "stale hooks file flagged",
			out:       "Codex hooks: OUT OF DATE\n  1 hook(s) the CLI installs today aren't declared in .codex/hooks.json:\n",
			wantOK:    true,
			wantMatch: "OUT OF DATE",
		},
		{
			name:      "trust review flagged",
			out:       "Codex hook trust: REVIEW NEEDED\n  1 hook(s) declared in .codex/hooks.json have no trusted_hash entry yet:\n",
			wantOK:    true,
			wantMatch: "REVIEW NEEDED",
		},
		{
			name:   "unrelated v2 generation issues do not trip preflight",
			out:    "v2 generations: 3 issue(s) found\nError: v2 generation health check failed\n",
			wantOK: false,
		},
		{
			name:      "first matching marker wins",
			out:       "Codex hooks: OUT OF DATE\n...\nCodex hook trust: REVIEW NEEDED\n",
			wantOK:    true,
			wantMatch: "OUT OF DATE",
		},
	}

	for _, tc := range cases {

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			marker, ok := findHookDrift(tc.out)
			if ok != tc.wantOK {
				t.Fatalf("findHookDrift returned ok=%v, want %v\noutput: %q", ok, tc.wantOK, tc.out)
			}
			if marker != tc.wantMatch {
				t.Fatalf("findHookDrift returned %q, want %q", marker, tc.wantMatch)
			}
		})
	}
}
