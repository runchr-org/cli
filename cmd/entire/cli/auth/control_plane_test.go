package auth

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/api"
	"github.com/entireio/cli/internal/entireclient/contexts"
	"github.com/entireio/cli/internal/entireclient/tokenstore"
)

// These tests drive process-global state (ENTIRE_AUTH_BASE_URL,
// ENTIRE_CONFIG_DIR, the token-store backend) so they cannot run in parallel.

// writeActiveContext writes a single-context contexts.json under configDir and
// marks it current.
func writeActiveContext(t *testing.T, configDir, name, coreURL, handle, svc string) {
	t.Helper()
	c := &contexts.Context{Name: name, CoreURL: coreURL, Handle: handle, KeychainService: svc}
	if err := contexts.Save(configDir, &contexts.File{CurrentContext: name, Contexts: []*contexts.Context{c}}); err != nil {
		t.Fatalf("write contexts.json: %v", err)
	}
}

// An explicit ENTIRE_AUTH_BASE_URL pins the core to that origin and skips the
// active-context lookup entirely — the override is unconditional.
func TestResolveControlPlaneTarget_EnvOverrideWins(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("ENTIRE_CONFIG_DIR", configDir)
	t.Setenv(api.AuthBaseURLEnvVar, "https://override.example")

	// An active context on a *different* core must be ignored under override.
	writeActiveContext(t, configDir, "ctx", "https://other-core.example", "alice", "svc")

	target, err := ResolveControlPlaneTarget()
	if err != nil {
		t.Fatalf("ResolveControlPlaneTarget: %v", err)
	}
	if want := api.AuthBaseURL(); target.CoreURL != want {
		t.Fatalf("CoreURL = %q, want the override origin %q (not the context core)", target.CoreURL, want)
	}
}

// With no override and an active context, the target is that context's core and
// the bearer comes from the context's keyring slot (the refreshing provider).
func TestResolveControlPlaneTarget_ActiveContextWins(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("ENTIRE_CONFIG_DIR", configDir)
	t.Setenv(api.AuthBaseURLEnvVar, "") // ensure no override leaks in from the env

	restore := tokenstore.UseFileBackendForTesting(filepath.Join(t.TempDir(), "tokens.json"))
	t.Cleanup(restore)

	const coreURL = "https://ctx-core.example"
	svc := tokenstore.CoreKeyringService(coreURL)
	jwt := makeJWT(t, fmt.Sprintf(`{"iss":%q,"handle":"alice","exp":%d}`, coreURL, time.Now().Add(2*time.Hour).Unix()))
	if err := tokenstore.Set(svc, "alice", tokenstore.EncodeTokenWithExpiration(jwt, 7200)); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	writeActiveContext(t, configDir, "alice@core", coreURL, "alice", svc)

	target, err := ResolveControlPlaneTarget()
	if err != nil {
		t.Fatalf("ResolveControlPlaneTarget: %v", err)
	}
	if target.CoreURL != coreURL {
		t.Fatalf("CoreURL = %q, want the active context's core %q", target.CoreURL, coreURL)
	}
	// The fresh token is returned with no network call, proving the source is
	// wired to the context's keyring slot.
	got, err := target.TokenSource(context.Background())
	if err != nil {
		t.Fatalf("TokenSource: %v", err)
	}
	if got != jwt {
		t.Fatalf("TokenSource returned %q, want the context's stored JWT", got)
	}
}

// With no override and no active context, the target falls back to the
// configured default auth origin (pre-contexts behaviour).
func TestResolveControlPlaneTarget_NoContextFallsBackToDefault(t *testing.T) {
	configDir := t.TempDir() // empty: no contexts.json
	t.Setenv("ENTIRE_CONFIG_DIR", configDir)
	t.Setenv(api.AuthBaseURLEnvVar, "")

	target, err := ResolveControlPlaneTarget()
	if err != nil {
		t.Fatalf("ResolveControlPlaneTarget: %v", err)
	}
	if want := api.AuthBaseURL(); target.CoreURL != want {
		t.Fatalf("CoreURL = %q, want the default auth origin %q", target.CoreURL, want)
	}
}
