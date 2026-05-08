package auth

import (
	"net/http"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/api"
)

// Test-local mirrors of the v1 / v2 client_id values, so assertions
// don't repeat the same string literal across multiple tests (goconst).
// Both providers now share the same client_id; the constants are kept
// distinct so a future divergence (or a regression that re-splits them)
// shows up at a single edit site.
const (
	wantClientIDV1 = "entire-cli"
	wantClientIDV2 = "entire-cli"
)

// resolveProvider is a pure function — no env reads — so the routing
// table can be exercised without t.Setenv (and without the
// process-wide sync.Once in CurrentProvider freezing the first
// observation forever).

func TestResolveProvider_DefaultsToV1(t *testing.T) {
	t.Parallel()
	p := resolveProvider("")
	if p.ClientID != wantClientIDV1 || p.DeviceCodePath != "/oauth/device/code" || p.TokenPath != "/oauth/token" {
		t.Fatalf("default provider = %+v, want v1 config", p)
	}
	if p.AuthTokensPath != "/api/v1/auth/tokens" {
		t.Fatalf("default AuthTokensPath = %q, want /api/v1/auth/tokens", p.AuthTokensPath)
	}
}

func TestResolveProvider_V1Explicit(t *testing.T) {
	t.Parallel()
	p := resolveProvider("v1")
	if p.ClientID != wantClientIDV1 {
		t.Fatalf("v1 ClientID = %q", p.ClientID)
	}
	// v1 is single-host (entire.io); no STS surface, same-host shortcut
	// always wins. Empty STSPath is the contract.
	if p.STSPath != "" {
		t.Fatalf("v1 STSPath = %q, want empty (single-host, no STS)", p.STSPath)
	}
}

func TestResolveProvider_V2(t *testing.T) {
	t.Parallel()
	p := resolveProvider("v2")
	if p.ClientID != wantClientIDV2 {
		t.Fatalf("v2 ClientID = %q, want %s", p.ClientID, wantClientIDV2)
	}
	if p.DeviceCodePath != "/api/auth/oauth/device/code" {
		t.Fatalf("v2 DeviceCodePath = %q", p.DeviceCodePath)
	}
	if p.TokenPath != "/api/auth/token" {
		t.Fatalf("v2 TokenPath = %q", p.TokenPath)
	}
	if p.STSPath != "/api/authz/sts/token" {
		t.Fatalf("v2 STSPath = %q", p.STSPath)
	}
	if p.AuthTokensPath != "/api/auth/tokens" {
		t.Fatalf("v2 AuthTokensPath = %q, want /api/auth/tokens", p.AuthTokensPath)
	}
}

func TestResolveProvider_UnknownDefaultsToV1(t *testing.T) {
	t.Parallel()
	p := resolveProvider("v999")
	if p.ClientID != wantClientIDV1 {
		t.Fatalf("unknown version should default to v1; got ClientID = %q", p.ClientID)
	}
}

func TestResolveProvider_TrimsWhitespace(t *testing.T) {
	t.Parallel()
	p := resolveProvider("  v2  ")
	if p.ClientID != wantClientIDV2 {
		t.Fatalf("whitespace-padded v2 ClientID = %q, want %s", p.ClientID, wantClientIDV2)
	}
}

// TestSetProviderForTest_OverridesCurrentProvider locks in the test
// seam: any test that pins a provider via SetProviderForTest must see
// it from CurrentProvider regardless of process-wide singleton state.
func TestSetProviderForTest_OverridesCurrentProvider(t *testing.T) {
	pinned := Provider{
		ClientID:       "test-client",
		DeviceCodePath: "/test/device",
		TokenPath:      "/test/token",
		STSPath:        "/test/sts",
		AuthTokensPath: "/test/tokens",
	}
	SetProviderForTest(t, pinned)

	got := CurrentProvider()
	if got != pinned {
		t.Fatalf("CurrentProvider() = %+v, want %+v", got, pinned)
	}
}

func TestNewClient_HonoursPinnedProvider(t *testing.T) {
	t.Setenv(api.BaseURLEnvVar, "https://example.test")
	t.Setenv(api.AuthBaseURLEnvVar, "")
	SetProviderForTest(t, resolveProvider("v2"))

	c := NewClient(&http.Client{})
	if c.inner.ClientID != wantClientIDV2 {
		t.Errorf("ClientID = %q, want %s", c.inner.ClientID, wantClientIDV2)
	}
	if c.inner.DeviceCodePath != "/api/auth/oauth/device/code" {
		t.Errorf("DeviceCodePath = %q", c.inner.DeviceCodePath)
	}
	if c.inner.TokenPath != "/api/auth/token" {
		t.Errorf("TokenPath = %q", c.inner.TokenPath)
	}
	if c.inner.BaseURL != "https://example.test" {
		t.Errorf("BaseURL = %q", c.inner.BaseURL)
	}
}

func TestNewClient_DefaultsToV1WhenPinned(t *testing.T) {
	t.Setenv(api.BaseURLEnvVar, "https://example.test")
	t.Setenv(api.AuthBaseURLEnvVar, "")
	SetProviderForTest(t, resolveProvider(""))

	c := NewClient(nil)
	if c.inner.ClientID != wantClientIDV1 {
		t.Errorf("ClientID = %q, want %s", c.inner.ClientID, wantClientIDV1)
	}
	if c.inner.DeviceCodePath != "/oauth/device/code" {
		t.Errorf("DeviceCodePath = %q", c.inner.DeviceCodePath)
	}
	if c.inner.TokenPath != "/oauth/token" {
		t.Errorf("TokenPath = %q", c.inner.TokenPath)
	}
}
