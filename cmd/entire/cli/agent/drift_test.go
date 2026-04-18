package agent

import (
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/versioninfo"
)

func TestNormalizeSemver(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in, want string
	}{
		{"", "v0.0.0"},
		{"dev", "v0.0.0"},
		{"0.5.3", "v0.5.3"},
		{"v0.5.3", "v0.5.3"},
		{"  1.2.3  ", "v1.2.3"},
		{"0.5.3-rc.1", "v0.5.3-rc.1"},
		{"garbage", "v0.0.0"},
	}
	for _, tc := range cases {
		got := normalizeSemver(tc.in)
		if got != tc.want {
			t.Errorf("normalizeSemver(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestCheckHookDrift_DevBuildShortCircuit pins the safety net: running an
// unreleased binary must never produce drift warnings. Developers sit on
// branches where versioninfo.Version is "dev", and we don't want to drown
// them in noise.
//
// NOTE: Not parallel because it swaps a global (versioninfo.Version).
func TestCheckHookDrift_DevBuildShortCircuit(t *testing.T) {
	original := versioninfo.Version
	t.Cleanup(func() { versioninfo.Version = original })

	versioninfo.Version = "dev"
	if reports := CheckHookDrift(t.Context()); reports != nil {
		t.Fatalf("expected nil reports for dev build, got %v", reports)
	}
}
