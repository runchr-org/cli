package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/entireio/cli/internal/entireclient/clusterdiscovery"
	"github.com/entireio/cli/internal/entireclient/contexts"
	"github.com/entireio/cli/internal/entireclient/tokenstore"
)

// seedRepoTokenContext wires the two seams RepoScopedToken sits on: a
// file-backed token store holding a still-valid login JWT for a context on
// coreURL, and a discovery stub resolving any cluster to that context. The
// exchange transport is left to each test. Returns the seeded login JWT.
func seedRepoTokenContext(t *testing.T, coreURL string) string {
	t.Helper()
	// Sandbox the legacy-login migration's contexts.json writes.
	t.Setenv("ENTIRE_CONFIG_DIR", t.TempDir())
	t.Cleanup(tokenstore.UseFileBackendForTesting(filepath.Join(t.TempDir(), "tokens.json")))

	svc := tokenstore.CoreKeyringService(coreURL)
	jwt := makeJWT(t, fmt.Sprintf(`{"iss":%q,"handle":"alice","exp":%d}`, coreURL, time.Now().Add(2*time.Hour).Unix()))
	if err := tokenstore.Set(svc, "alice", tokenstore.EncodeTokenWithExpiration(jwt, 7200)); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	c := &contexts.Context{Name: "alice@core", CoreURL: coreURL, Handle: "alice", KeychainService: svc}
	t.Cleanup(setResolveContextForClusterForTest(
		func(context.Context, string, string, string, *http.Client, clusterdiscovery.DebugFunc) (*contexts.Context, error) {
			return c, nil
		}))
	return jwt
}

// statusTransport returns a canned non-200 response with the given body so
// the OAuth error-decoding path can be exercised offline.
type statusTransport struct {
	status int
	body   string
}

func (s statusTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: s.status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(s.body)),
		Request:    req,
	}, nil
}

// TestRepoScopedToken_InvalidTarget asserts that a 400 invalid_target STS
// response — what the data plane returns for a suspended (or otherwise
// non-servable) mirror — surfaces as ErrRepoTargetUnknown while still
// preserving the verbatim OAuth description for callers that don't branch
// on the sentinel.
func TestRepoScopedToken_InvalidTarget(t *testing.T) {
	seedRepoTokenContext(t, "https://us.auth.entire.io")
	t.Cleanup(SetRepoExchangeTransportForTest(statusTransport{
		status: http.StatusBadRequest,
		body:   `{"error":"invalid_target","error_description":"no mirror at this URL"}`,
	}))

	_, err := RepoScopedToken(context.Background(),
		"aws-us-east-2.entire.io", "/gh/octocat/hello", "pull")
	if err == nil {
		t.Fatal("RepoScopedToken: expected error, got nil")
	}
	if !errors.Is(err, ErrRepoTargetUnknown) {
		t.Errorf("error %v does not wrap ErrRepoTargetUnknown", err)
	}
	// Verbatim STS detail must remain in the chain.
	if !strings.Contains(err.Error(), "no mirror at this URL") {
		t.Errorf("error %q dropped the STS description", err)
	}
}

// captureTransport records the last request's parsed form body, URL, and
// Authorization header, and returns a canned RFC 8693 token-exchange
// success response.
type captureTransport struct {
	form url.Values
	url  string
	auth string
}

func (c *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, err
	}
	c.form = form
	c.url = req.URL.String()
	c.auth = req.Header.Get("Authorization")
	resp := `{"access_token":"repo-scoped.jwt","token_type":"Bearer",` +
		`"issued_token_type":"urn:ietf:params:oauth:token-type:access_token","expires_in":300}`
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(resp)),
		Request:    req,
	}, nil
}

// TestRepoScopedToken_WireForm asserts the exchange targets the
// cluster-resolved context's core (not any ambient default) and that the
// form matches what the data plane's git gate accepts — identical to what
// git-remote-entire sends via repocreds.
func TestRepoScopedToken_WireForm(t *testing.T) {
	loginJWT := seedRepoTokenContext(t, "https://eu.auth.entire.io")
	capture := &captureTransport{}
	t.Cleanup(SetRepoExchangeTransportForTest(capture))

	tok, err := RepoScopedToken(context.Background(),
		"aws-eu-west-1.entire.io", "/gh/octocat/hello", "pull")
	if err != nil {
		t.Fatalf("RepoScopedToken: %v", err)
	}
	if tok != "repo-scoped.jwt" {
		t.Errorf("token = %q, want %q", tok, "repo-scoped.jwt")
	}

	// Endpoint: the resolved context's core, not the active context's.
	if capture.url != "https://eu.auth.entire.io/oauth/token" {
		t.Errorf("exchange URL = %q, want resolved core's /oauth/token", capture.url)
	}

	// Wire form must match what the data plane's git gate accepts.
	want := map[string]string{
		"grant_type":           "urn:ietf:params:oauth:grant-type:token-exchange",
		"subject_token":        loginJWT,
		"subject_token_type":   "urn:ietf:params:oauth:token-type:access_token",
		"requested_token_type": "urn:ietf:params:oauth:token-type:access_token",
		"audience":             "https://aws-eu-west-1.entire.io/gh/octocat/hello",
		"scope":                "repo:pull",
	}
	for k, v := range want {
		if got := capture.form.Get(k); got != v {
			t.Errorf("form[%q] = %q, want %q", k, got, v)
		}
	}

	// resource must NOT be sent — the gate keys on audience alone, and a
	// divergent resource param risks the server validating the wrong value.
	if capture.form.Has("resource") {
		t.Errorf("form unexpectedly includes resource=%q", capture.form.Get("resource"))
	}

	// client_id travels as Basic auth (PostOAuthToken lifts it from the
	// form; zitadel's token endpoint only reads it there).
	if capture.form.Has("client_id") {
		t.Errorf("form unexpectedly includes client_id=%q", capture.form.Get("client_id"))
	}
	if !strings.HasPrefix(capture.auth, "Basic ") {
		t.Errorf("Authorization = %q, want Basic client credentials", capture.auth)
	}
}

// TestRepoScopedToken_DiscoveryErrorSurfaces asserts a context-resolution
// failure (no eligible login, ambiguous contexts, unreachable cluster) is
// returned verbatim — never papered over with a wrong-core exchange.
func TestRepoScopedToken_DiscoveryErrorSurfaces(t *testing.T) {
	t.Setenv("ENTIRE_CONFIG_DIR", t.TempDir())
	t.Cleanup(tokenstore.UseFileBackendForTesting(filepath.Join(t.TempDir(), "tokens.json")))
	t.Cleanup(setResolveContextForClusterForTest(
		func(context.Context, string, string, string, *http.Client, clusterdiscovery.DebugFunc) (*contexts.Context, error) {
			return nil, errors.New("not logged in to a login server trusted by cluster x; run `entire login`")
		}))
	t.Cleanup(SetRepoExchangeTransportForTest(failRoundTripper(t)))

	_, err := RepoScopedToken(context.Background(), "x.entire.io", "/gh/o/r", "pull")
	if err == nil || !strings.Contains(err.Error(), "not logged in to a login server trusted by cluster x") {
		t.Fatalf("err = %v, want the discovery error verbatim", err)
	}
}
