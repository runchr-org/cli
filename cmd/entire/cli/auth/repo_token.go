package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/entireio/cli/internal/entireclient/clusterdiscovery"
	"github.com/entireio/cli/internal/entireclient/contexts"
	"github.com/entireio/cli/internal/entireclient/discovery"
	"github.com/entireio/cli/internal/entireclient/httputil"
	"github.com/entireio/cli/internal/entireclient/repocreds"
)

// ErrRepoTargetUnknown reports that the cluster's STS refused the exchange
// with RFC 8693 `invalid_target`: it has no servable mirror at the
// requested audience. The placement row may well exist but be suspended —
// the data plane's auth gate deliberately hides suspended mirrors behind
// invalid_target rather than disclosing their state (an enumeration guard;
// see entiredb's validateMirrorRepoExchange). Callers that already know the
// mirror exists (e.g. the create flow's clone probe) use this to render an
// actionable message instead of the raw OAuth error.
var ErrRepoTargetUnknown = errors.New("cluster has no servable mirror at this audience")

// repoExchangeTimeout bounds the HTTP calls behind one mint: the
// /.well-known cluster discovery (disk-cached after the first call) and
// the /oauth/token exchange.
const repoExchangeTimeout = 30 * time.Second

// resolveContextForCluster is the discovery seam, swapped in tests so they
// don't reach the network. Mirrors clusterdiscovery.ResolveContextForCluster.
var resolveContextForCluster resolveContextFunc = clusterdiscovery.ResolveContextForCluster

// setResolveContextForClusterForTest overrides the cluster-discovery seam
// and returns a cleanup func. Test-only.
func setResolveContextForClusterForTest(fn resolveContextFunc) func() {
	prev := resolveContextForCluster
	resolveContextForCluster = fn
	return func() { resolveContextForCluster = prev }
}

// repoExchangeTransportForTest, when non-nil, is the HTTP transport used by
// RepoScopedToken's exchange (and login refresh), so the wire form can be
// asserted without a live core. Production leaves it nil.
var repoExchangeTransportForTest http.RoundTripper

// SetRepoExchangeTransportForTest installs rt as the transport used by
// RepoScopedToken and returns a cleanup function. Test-only.
func SetRepoExchangeTransportForTest(rt http.RoundTripper) func() {
	prev := repoExchangeTransportForTest
	repoExchangeTransportForTest = rt
	return func() { repoExchangeTransportForTest = prev }
}

// RepoScopedToken exchanges a login JWT for a short-lived, repo-scoped
// access token usable against a data-plane cluster's git endpoints
// (clone / fetch / info-refs). The data plane's git gate rejects the raw
// login bearer: it only accepts a token whose RFC 8693 audience is
// https://<clusterHost><repoSlug> and whose scope is "repo:<action>".
//
// The login context is resolved the way git-remote-entire resolves it: the
// cluster's /.well-known/entire-cluster.json names the core(s) it trusts,
// and the matching local context (active if eligible, else the sole
// eligible one, else an explicit-choice error) supplies the subject token —
// exchanged at that context's core, never at the active context's. The
// exchange itself goes through repocreds, the same code path (and wire
// form) git-remote-entire uses. An expired login JWT is transparently
// re-minted from the stored refresh token.
//
//   - clusterHost is the data-plane cluster host (e.g. aws-us-east-2.entire.io).
//   - repoSlug is the surface-prefixed repo path (e.g. /gh/octocat/hello or
//     /et/<project>/<repo>), joined verbatim to https://<clusterHost> to
//     form the audience.
//   - action is "pull" for reads or "push" for writes.
//
// Each call performs a fresh exchange and does not cache — callers that
// poll (e.g. the mirror clone wait) re-invoke on token expiry.
func RepoScopedToken(ctx context.Context, clusterHost, repoSlug, action string) (string, error) {
	if clusterHost == "" {
		return "", errors.New("repo-scoped token exchange requires a target cluster host")
	}

	// Bridge any pre-contexts.json login so the resolver can match it.
	// Best-effort: a migration failure must not block resolution.
	_, _ = MigrateLegacyLoginContext() //nolint:errcheck // best-effort bridge; resolution proceeds regardless

	httpClient := &http.Client{Timeout: repoExchangeTimeout, Transport: repoExchangeTransportForTest}
	clusterCtx, err := resolveContextForCluster(ctx, contexts.DefaultConfigDir(), discovery.DefaultCacheDir(), clusterHost, httpClient, nil)
	if err != nil {
		return "", err
	}

	allowInsecure := insecureHTTPEnabled() || isLoopbackHTTP(clusterCtx.CoreURL)
	loginProvider, err := NewRefreshingLoginProvider(clusterCtx, repoExchangeTransportForTest, allowInsecure)
	if err != nil {
		return "", err
	}

	token, err := repocreds.New(clusterCtx.CoreURL, "https://"+clusterHost, loginProvider, httpClient).
		Token(ctx, repoSlug, action)
	if err != nil {
		// invalid_target means the cluster has no servable mirror at this
		// audience (commonly a suspended placement). Surface the sentinel for
		// callers that branch on it, preserving the verbatim OAuth body
		// (second %w) for those that don't.
		var oe *httputil.OAuthError
		if errors.As(err, &oe) && oe.Code == "invalid_target" {
			return "", fmt.Errorf("repo-scoped token exchange: %w: %w", ErrRepoTargetUnknown, err)
		}
		return "", fmt.Errorf("repo-scoped token exchange: %w", err)
	}
	return token, nil
}
