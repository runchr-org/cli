package auth

import (
	"net/http"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/api"
)

func TestCurrentProvider_DefaultsToV1(t *testing.T) {
	t.Setenv(ProviderVersionEnvVar, "")

	p := currentProvider()
	if p.clientID != "entire-cli" || p.deviceCodePath != "/oauth/device/code" || p.tokenPath != "/oauth/token" {
		t.Fatalf("default provider = %+v, want v1 config", p)
	}
}

func TestCurrentProvider_V1Explicit(t *testing.T) {
	t.Setenv(ProviderVersionEnvVar, "v1")

	p := currentProvider()
	if p.clientID != "entire-cli" {
		t.Fatalf("v1 clientID = %q", p.clientID)
	}
}

func TestCurrentProvider_V2(t *testing.T) {
	t.Setenv(ProviderVersionEnvVar, "v2")

	p := currentProvider()
	if p.clientID != "cli" {
		t.Fatalf("v2 clientID = %q, want cli", p.clientID)
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
	if p.clientID != "entire-cli" {
		t.Fatalf("unknown version should default to v1; got clientID = %q", p.clientID)
	}
}

func TestCurrentProvider_TrimsWhitespace(t *testing.T) {
	t.Setenv(ProviderVersionEnvVar, "  v2  ")

	p := currentProvider()
	if p.clientID != "cli" {
		t.Fatalf("whitespace-padded v2 clientID = %q, want cli", p.clientID)
	}
}

func TestNewClient_HonoursProviderVersion(t *testing.T) {
	t.Setenv(api.BaseURLEnvVar, "https://example.test")
	t.Setenv(ProviderVersionEnvVar, "v2")

	c := NewClient(&http.Client{})
	if c.inner.ClientID != "cli" {
		t.Errorf("ClientID = %q, want cli", c.inner.ClientID)
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
	if c.inner.ClientID != "entire-cli" {
		t.Errorf("ClientID = %q, want entire-cli", c.inner.ClientID)
	}
	if c.inner.DeviceCodePath != "/oauth/device/code" {
		t.Errorf("DeviceCodePath = %q", c.inner.DeviceCodePath)
	}
	if c.inner.TokenPath != "/oauth/token" {
		t.Errorf("TokenPath = %q", c.inner.TokenPath)
	}
}
