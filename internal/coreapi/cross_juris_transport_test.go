package coreapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// crossJurisTestServer scripts responses keyed by the caller-controlled
// handler and records the Authorization header per request so tests can
// assert which token the transport presented on each hop. It auto-serves
// GET /.well-known/entire-federation with the hosts in `peers`.
type crossJurisTestServer struct {
	hits  atomic.Int32
	auths []string
	peers []string
	srv   *httptest.Server
}

func (s *crossJurisTestServer) record(r *http.Request) {
	s.hits.Add(1)
	s.auths = append(s.auths, r.Header.Get("Authorization"))
}

func newCrossJurisTestServer(t *testing.T, handler func(s *crossJurisTestServer, w http.ResponseWriter, r *http.Request)) *crossJurisTestServer {
	t.Helper()
	s := &crossJurisTestServer{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/entire-federation" {
			writeTestFederation(w, s.peers)
			return
		}
		handler(s, w, r)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func writeTestFederation(w http.ResponseWriter, peers []string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if len(peers) == 0 {
		w.Write([]byte(`{"peer_auth_hosts":[]}`)) //nolint:errcheck // test
		return
	}
	var b strings.Builder
	b.WriteString(`{"peer_auth_hosts":[`)
	for i, p := range peers {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"`)
		b.WriteString(p)
		b.WriteString(`"`)
	}
	b.WriteString(`]}`)
	w.Write([]byte(b.String())) //nolint:errcheck // test
}

func transportFor() *crossJurisRoundTripper {
	return newCrossJurisRoundTripper(http.DefaultTransport)
}

// TestRoundTripper_PassThrough: a 2xx is returned unchanged.
func TestRoundTripper_PassThrough(t *testing.T) {
	srv := newCrossJurisTestServer(t, func(s *crossJurisTestServer, w http.ResponseWriter, r *http.Request) {
		s.record(r)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`)) //nolint:errcheck // test
	})

	client := &http.Client{Transport: transportFor()}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.srv.URL, nil) //nolint:errcheck // test
	req.Header.Set("Authorization", "Bearer user-jwt")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d", resp.StatusCode)
	}
	if srv.hits.Load() != 1 {
		t.Fatalf("hits=%d", srv.hits.Load())
	}
}

// TestRoundTripper_421FollowsToHomeCore: 421 with home_core_url is
// followed; the home core gets the original bearer on the first hop.
func TestRoundTripper_421FollowsToHomeCore(t *testing.T) {
	homeCore := newCrossJurisTestServer(t, func(s *crossJurisTestServer, w http.ResponseWriter, r *http.Request) {
		s.record(r)
		w.WriteHeader(http.StatusOK)
	})
	wrongCore := newCrossJurisTestServer(t, func(s *crossJurisTestServer, w http.ResponseWriter, r *http.Request) {
		s.record(r)
		w.WriteHeader(http.StatusMisdirectedRequest)
		w.Write([]byte(`{"home_core_url":"` + homeCore.srv.URL + `"}`)) //nolint:errcheck // test
	})
	wrongCore.peers = []string{homeCore.srv.URL}

	client := &http.Client{Transport: transportFor()}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, wrongCore.srv.URL+"/api/v1/mirrors", strings.NewReader(`{"a":1}`)) //nolint:errcheck // test
	req.Header.Set("Authorization", "Bearer user-jwt")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d", resp.StatusCode)
	}
	if homeCore.auths[0] != "Bearer user-jwt" {
		t.Fatalf("home core first hop auth = %q", homeCore.auths[0])
	}
}

// TestRoundTripper_421ThenBareUnauthorizedProactiveExchange is the
// production path: after following a 421, the home core can't verify the
// foreign-region login JWT's signature and returns a BARE 401 (no hint).
// The transport must still recover by exchanging the original login JWT
// at the home core's same-origin /oauth/token and retrying.
func TestRoundTripper_421ThenBareUnauthorizedProactiveExchange(t *testing.T) {
	homeExchangeHits := atomic.Int32{}
	homeExchangeSubjects := []string{}
	homeAPIAuths := []string{}
	homeCore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			_ = r.ParseForm() //nolint:errcheck // test
			homeExchangeSubjects = append(homeExchangeSubjects, r.PostForm.Get("subject_token"))
			homeExchangeHits.Add(1)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"access_token":"home-exchanged-jwt"}`)) //nolint:errcheck // test
			return
		}
		homeAPIAuths = append(homeAPIAuths, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") == "Bearer home-exchanged-jwt" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"ok":true}`)) //nolint:errcheck // test
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid token"}`)) //nolint:errcheck // test
	}))
	t.Cleanup(homeCore.Close)

	wrongCore := newCrossJurisTestServer(t, func(s *crossJurisTestServer, w http.ResponseWriter, r *http.Request) {
		s.record(r)
		w.WriteHeader(http.StatusMisdirectedRequest)
		w.Write([]byte(`{"home_core_url":"` + homeCore.URL + `"}`)) //nolint:errcheck // test
	})
	wrongCore.peers = []string{homeCore.URL}

	client := &http.Client{Transport: transportFor()}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, wrongCore.srv.URL+"/api/v1/mirrors/collaborators", strings.NewReader(`{}`)) //nolint:errcheck // test
	req.Header.Set("Authorization", "Bearer original-eu-login-jwt")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	if homeExchangeHits.Load() != 1 {
		t.Fatalf("home exchange hits=%d", homeExchangeHits.Load())
	}
	if len(homeExchangeSubjects) != 1 || homeExchangeSubjects[0] != "original-eu-login-jwt" {
		t.Fatalf("exchange subject_token = %v, want [original-eu-login-jwt]", homeExchangeSubjects)
	}
	if len(homeAPIAuths) != 2 || homeAPIAuths[0] != "Bearer original-eu-login-jwt" || homeAPIAuths[1] != "Bearer home-exchanged-jwt" {
		t.Fatalf("home API auths = %v", homeAPIAuths)
	}
}

// TestRoundTripper_BareUnauthorizedNoRedirectPassesThrough: a bare 401
// on a non-redirected request is a genuine failure — no exchange.
func TestRoundTripper_BareUnauthorizedNoRedirectPassesThrough(t *testing.T) {
	exchangeHits := atomic.Int32{}
	srv := newCrossJurisTestServer(t, func(s *crossJurisTestServer, w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			exchangeHits.Add(1)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"access_token":"x"}`)) //nolint:errcheck // test
			return
		}
		s.record(r)
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid token"}`)) //nolint:errcheck // test
	})

	client := &http.Client{Transport: transportFor()}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.srv.URL+"/api/v1/me", nil) //nolint:errcheck // test
	req.Header.Set("Authorization", "Bearer user-jwt")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got %d", resp.StatusCode)
	}
	if srv.hits.Load() != 1 || exchangeHits.Load() != 0 {
		t.Fatalf("hits=%d exchanges=%d", srv.hits.Load(), exchangeHits.Load())
	}
}

// TestRoundTripper_401ExchangeAndRetry covers the structured-hint path.
func TestRoundTripper_401ExchangeAndRetry(t *testing.T) {
	exchangeHits := atomic.Int32{}
	apiHits := atomic.Int32{}
	apiAuths := []string{}
	var apiServer *httptest.Server
	apiServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			exchangeHits.Add(1)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"access_token":"exchanged-jwt","token_type":"Bearer","expires_in":300}`)) //nolint:errcheck // test
			return
		}
		n := apiHits.Add(1)
		apiAuths = append(apiAuths, r.Header.Get("Authorization"))
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"cross_juris_token_required","token_exchange_url":"` + apiServer.URL + `/oauth/token","audience":"` + apiServer.URL + `"}`)) //nolint:errcheck // test
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(apiServer.Close)

	client := &http.Client{Transport: transportFor()}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, apiServer.URL+"/api/v1/mirrors", strings.NewReader(`{"a":1}`)) //nolint:errcheck // test
	req.Header.Set("Authorization", "Bearer user-jwt")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d", resp.StatusCode)
	}
	if exchangeHits.Load() != 1 {
		t.Fatalf("exchanges=%d", exchangeHits.Load())
	}
	if len(apiAuths) != 2 || apiAuths[0] != "Bearer user-jwt" || apiAuths[1] != "Bearer exchanged-jwt" {
		t.Fatalf("api auths = %v", apiAuths)
	}
}

// TestRoundTripper_Rejects421OffFederation: a 421 to a host not in the
// responding core's federation manifest is not followed.
func TestRoundTripper_Rejects421OffFederation(t *testing.T) {
	homeHits := atomic.Int32{}
	homeCore := newCrossJurisTestServer(t, func(s *crossJurisTestServer, w http.ResponseWriter, r *http.Request) {
		homeHits.Add(1)
		s.record(r)
		w.WriteHeader(http.StatusOK)
	})
	wrongCore := newCrossJurisTestServer(t, func(s *crossJurisTestServer, w http.ResponseWriter, r *http.Request) {
		s.record(r)
		w.WriteHeader(http.StatusMisdirectedRequest)
		w.Write([]byte(`{"home_core_url":"` + homeCore.srv.URL + `"}`)) //nolint:errcheck // test
	})
	// wrongCore.peers stays empty.

	client := &http.Client{Transport: transportFor()}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, wrongCore.srv.URL+"/api/v1/mirrors", nil) //nolint:errcheck // test
	req.Header.Set("Authorization", "Bearer user-jwt")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("got %d, want 421 passthrough", resp.StatusCode)
	}
	if homeHits.Load() != 0 {
		t.Fatalf("REGRESSION: off-federation home core received the JWT (hits=%d)", homeHits.Load())
	}
}

// TestRoundTripper_RejectsOffOrigin401ExchangeURL: a hint pointing
// token_exchange_url off-origin must not leak the JWT.
func TestRoundTripper_RejectsOffOrigin401ExchangeURL(t *testing.T) {
	attackerHits := atomic.Int32{}
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attackerHits.Add(1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"access_token":"attacker-issued"}`)) //nolint:errcheck // test
	}))
	t.Cleanup(attacker.Close)

	api := newCrossJurisTestServer(t, func(s *crossJurisTestServer, w http.ResponseWriter, r *http.Request) {
		s.record(r)
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"cross_juris_token_required","token_exchange_url":"` + attacker.URL + `","audience":"https://api.test"}`)) //nolint:errcheck // test
	})

	client := &http.Client{Transport: transportFor()}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, api.srv.URL+"/api/v1/me", nil) //nolint:errcheck // test
	req.Header.Set("Authorization", "Bearer user-jwt")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got %d", resp.StatusCode)
	}
	if attackerHits.Load() != 0 {
		t.Fatalf("REGRESSION: attacker host received the JWT (hits=%d)", attackerHits.Load())
	}
}

// TestRoundTripper_BodyReplayedOnRetry: a retried request replays its body.
func TestRoundTripper_BodyReplayedOnRetry(t *testing.T) {
	const payload = `{"provider":"github","owner":"acme","repo":"widget"}`
	homeCore := newCrossJurisTestServer(t, func(s *crossJurisTestServer, w http.ResponseWriter, r *http.Request) {
		s.record(r)
		body, _ := io.ReadAll(r.Body) //nolint:errcheck // test
		if string(body) != payload {
			t.Errorf("home body = %q", string(body))
		}
		w.WriteHeader(http.StatusOK)
	})
	wrongCore := newCrossJurisTestServer(t, func(s *crossJurisTestServer, w http.ResponseWriter, r *http.Request) {
		s.record(r)
		w.WriteHeader(http.StatusMisdirectedRequest)
		w.Write([]byte(`{"home_core_url":"` + homeCore.srv.URL + `"}`)) //nolint:errcheck // test
	})
	wrongCore.peers = []string{homeCore.srv.URL}

	client := &http.Client{Transport: transportFor()}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, wrongCore.srv.URL+"/api/v1/mirrors", bytes.NewReader([]byte(payload))) //nolint:errcheck // test
	req.Header.Set("Authorization", "Bearer user-jwt")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d", resp.StatusCode)
	}
}

// TestValidateExchangeURL pins the scheme / same-origin rules.
func TestValidateExchangeURL(t *testing.T) {
	mustParse := func(raw string) *url.URL {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	if err := validateExchangeURL("http://example.test/oauth/token", mustParse("http://example.test/api")); err == nil {
		t.Error("http non-loopback must be refused")
	}
	if err := validateExchangeURL("https://attacker.test/oauth/token", mustParse("https://example.test/api")); err == nil {
		t.Error("off-origin https must be refused")
	}
	if err := validateExchangeURL("https://example.test/oauth/token", mustParse("https://example.test/api")); err != nil {
		t.Errorf("same-origin https must pass: %v", err)
	}
	if err := validateExchangeURL("http://localhost:1234/oauth/token", mustParse("http://localhost:1234/api")); err != nil {
		t.Errorf("loopback http must pass: %v", err)
	}
}

// TestCacheExpiresAfterTTL: expired entries are evicted on lookup.
func TestCacheExpiresAfterTTL(t *testing.T) {
	rt := transportFor()
	rt.storeToken("https://example.test", "tok")
	if _, ok := rt.lookupToken("https://example.test"); !ok {
		t.Fatal("fresh token must be a hit")
	}
	rt.tokens.Store("https://example.test", cachedExchangedToken{token: "tok", exp: time.Now().Add(-time.Second)})
	if _, ok := rt.lookupToken("https://example.test"); ok {
		t.Fatal("expired entry must be evicted")
	}
}
