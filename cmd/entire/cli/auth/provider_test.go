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

func TestCurrentProvider_DefaultsToV1(t *testing.T) {
	t.Setenv(ProviderVersionEnvVar, "")

	p := currentProvider()
	if p.clientID != wantClientIDV1 || p.deviceCodePath != "/oauth/device/code" || p.tokenPath != "/oauth/token" {
		t.Fatalf("default provider = %+v, want v1 config", p)
	}
}

func TestCurrentProvider_V1Explicit(t *testing.T) {
	t.Setenv(ProviderVersionEnvVar, "v1")

	p := currentProvider()
	if p.clientID != wantClientIDV1 {
		t.Fatalf("v1 clientID = %q", p.clientID)
	}
}

func TestCurrentProvider_V2(t *testing.T) {
	t.Setenv(ProviderVersionEnvVar, "v2")

	p := currentProvider()
	if p.clientID != wantClientIDV2 {
		t.Fatalf("v2 clientID = %q, want %s", p.clientID, wantClientIDV2)
	}
	if p.deviceCodePath != "/api/auth/oauth/device/code" {
		t.Fatalf("v2 deviceCodePath = %q", p.deviceCodePath)
	}
	if p.tokenPath != "/api/auth/token" {
		t.Fatalf("v2 tokenPath = %q", p.tokenPath)
	}
}

func TestCurrentProvider_UnknownDefaultsToV1(t *testing.T) {
	t.Setenv(ProviderVersionEnvVar, "v999")

	p := currentProvider()
	if p.clientID != wantClientIDV1 {
		t.Fatalf("unknown version should default to v1; got clientID = %q", p.clientID)
	}
}

func TestCurrentProvider_TrimsWhitespace(t *testing.T) {
	t.Setenv(ProviderVersionEnvVar, "  v2  ")

	p := currentProvider()
	if p.clientID != wantClientIDV2 {
		t.Fatalf("whitespace-padded v2 clientID = %q, want %s", p.clientID, wantClientIDV2)
	}
}

func TestNewClient_HonoursProviderVersion(t *testing.T) {
	t.Setenv(api.BaseURLEnvVar, "https://example.test")
	t.Setenv(ProviderVersionEnvVar, "v2")

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

func TestNewClient_DefaultsToV1(t *testing.T) {
	t.Setenv(api.BaseURLEnvVar, "https://example.test")
	t.Setenv(ProviderVersionEnvVar, "")

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
