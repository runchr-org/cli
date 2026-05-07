package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/entireio/cli/auth/deviceflow"
	"github.com/entireio/cli/auth/tokens"
	"github.com/entireio/cli/cmd/entire/cli/api"
)

// nowFunc is the package's clock. Override in tests.
var nowFunc = time.Now

// DeviceAuthStart preserves the historical type name; the shape now
// matches deviceflow.DeviceCode field-for-field.
type DeviceAuthStart = deviceflow.DeviceCode

// DeviceAuthPoll is the historical token-poll response shape. The shim
// flattens deviceflow's typed errors back into the Error field so
// existing login.go logic that switches on result.Error keeps working.
//
// ErrorDescription carries the optional `error_description` from the
// server's RFC 8628 §3.5 error response, when present. Used to give
// callers a more actionable message than the bare error code.
type DeviceAuthPoll struct {
	AccessToken      string
	TokenType        string
	ExpiresIn        int
	Scope            string
	Error            string
	ErrorDescription string
}

// Client wraps a deviceflow.Client preconfigured for whichever provider
// version is selected via ENTIRE_AUTH_PROVIDER_VERSION (defaulting to
// v1).
type Client struct {
	inner *deviceflow.Client
}

// NewClient constructs a Client targeting the active provider version.
// httpClient is used directly when non-nil; otherwise http.DefaultClient.
func NewClient(httpClient *http.Client) *Client {
	p := currentProvider()
	return &Client{inner: &deviceflow.Client{
		HTTP:           httpClient,
		BaseURL:        api.BaseURL(),
		ClientID:       p.clientID,
		Scope:          "cli",
		UserAgent:      p.clientID,
		DeviceCodePath: p.deviceCodePath,
		TokenPath:      p.tokenPath,
	}}
}

// BaseURL returns the issuer base URL this client talks to.
func (c *Client) BaseURL() string { return c.inner.BaseURL }

// StartDeviceAuth requests a fresh device code.
func (c *Client) StartDeviceAuth(ctx context.Context) (*DeviceAuthStart, error) {
	return c.inner.StartDeviceAuth(ctx)
}

// PollDeviceAuth polls the token endpoint. On any RFC 8628 §3.5 error,
// the wire-side error code is returned in DeviceAuthPoll.Error so the
// existing polling loop in login.go can branch on it. Non-RFC errors
// (network, decode) are returned as a real error.
func (c *Client) PollDeviceAuth(ctx context.Context, deviceCode string) (*DeviceAuthPoll, error) {
	t, err := c.inner.PollDeviceAuth(ctx, deviceCode)
	if err != nil {
		if code := oauthErrorCode(err); code != "" {
			return &DeviceAuthPoll{
				Error:            code,
				ErrorDescription: descriptionFromSentinel(err, code),
			}, nil
		}
		return nil, err
	}

	return &DeviceAuthPoll{
		AccessToken: t.AccessToken,
		TokenType:   t.TokenType,
		ExpiresIn:   secondsUntil(t),
		Scope:       t.Scope,
	}, nil
}

// oauthErrorCode returns the wire-side code for a recognised RFC 8628
// sentinel error, or "" if err isn't one.
func oauthErrorCode(err error) string {
	switch {
	case errors.Is(err, deviceflow.ErrAuthorizationPending):
		return "authorization_pending"
	case errors.Is(err, deviceflow.ErrSlowDown):
		return "slow_down"
	case errors.Is(err, deviceflow.ErrAccessDenied):
		return "access_denied"
	case errors.Is(err, deviceflow.ErrExpiredToken):
		return "expired_token"
	case errors.Is(err, deviceflow.ErrInvalidGrant):
		return "invalid_grant"
	}
	return ""
}

// descriptionFromSentinel pulls the description suffix out of a wrapped
// sentinel error. The deviceflow lib uses fmt.Errorf("%w: %s", sentinel,
// description) when the server included an error_description, so the
// formatted error reads "<code>: <description>". Stripping the
// "<code>: " prefix yields the description; absent prefix means the
// server didn't supply one.
func descriptionFromSentinel(err error, code string) string {
	msg := err.Error()
	prefix := code + ": "
	if rest, ok := strings.CutPrefix(msg, prefix); ok {
		return rest
	}
	return ""
}

// secondsUntil computes seconds-until-expiry for a TokenSet with an
// absolute ExpiresAt. Returns 0 when no expiry is set, mirroring the
// historical shape of DeviceAuthPoll.ExpiresIn.
func secondsUntil(t *tokens.TokenSet) int {
	if t.ExpiresAt.IsZero() {
		return 0
	}
	return int(t.ExpiresAt.Unix() - nowFunc().Unix())
}
