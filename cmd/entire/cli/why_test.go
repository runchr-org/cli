package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestWhyCmd_Flags(t *testing.T) {
	t.Parallel()

	cmd := newWhyCmd()

	tests := []struct {
		name      string
		shorthand string
	}{
		{name: "lines", shorthand: "L"},
		{name: "interactive", shorthand: "i"},
		{name: "no-pager"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flag := cmd.Flags().Lookup(tt.name)
			if flag == nil {
				t.Fatalf("expected --%s flag to exist", tt.name)
			}
			if flag.Shorthand != tt.shorthand {
				t.Fatalf("--%s shorthand = %q, want %q", tt.name, flag.Shorthand, tt.shorthand)
			}
		})
	}
}

func TestWhyCmd_NoPathNonInteractiveErrors(t *testing.T) {
	t.Parallel()

	cmd := newWhyCmd()
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(nil)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected no-path non-interactive command to fail")
	}
	if !strings.Contains(err.Error(), "path required") {
		t.Fatalf("expected path-required error, got: %v", err)
	}
}
