//go:build opf_integration

package redact

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestOPF_LiveBinary_RedactsPerson(t *testing.T) {
	opfBin := os.Getenv("OPF_BIN")
	if opfBin == "" {
		t.Skip("set OPF_BIN to the path of the opf binary to run live integration")
	}
	resetOPFConfig()
	t.Cleanup(resetOPFConfig)

	ConfigurePrivacyFilter(OPFConfig{
		Enabled:    true,
		Categories: map[string]bool{"private_person": true},
		Command:    opfBin,
		Timeout:    60,
	})

	got := StringWithPrivacyFilter(context.Background(), "Alice was born in 1990.")
	if !strings.Contains(got, "[REDACTED_PERSON]") {
		t.Errorf("OPF live: expected PERSON redaction, got %q", got)
	}
}
