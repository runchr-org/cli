package auth

import (
	"errors"
	"fmt"
	"testing"

	"github.com/entireio/auth-go/deviceflow"
)

// TestOAuthErrorParts_KnownSentinels covers the five RFC 8628 §3.5
// codes the polling loop in login.go switches on by name. Without a
// match here, the loop's switch never fires for these terminal cases.
func TestOAuthErrorParts_KnownSentinels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"authorization_pending", deviceflow.ErrAuthorizationPending, "authorization_pending"},
		{"slow_down", deviceflow.ErrSlowDown, "slow_down"},
		{"access_denied", deviceflow.ErrAccessDenied, "access_denied"},
		{"expired_token", deviceflow.ErrExpiredToken, "expired_token"},
		{"invalid_grant", deviceflow.ErrInvalidGrant, "invalid_grant"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, desc, ok := oauthErrorParts(tc.err)
			if !ok {
				t.Fatalf("oauthErrorParts returned ok=false for %v", tc.err)
			}
			if code != tc.want {
				t.Errorf("code = %q, want %q", code, tc.want)
			}
			if desc != "" {
				t.Errorf("desc = %q, want empty (no description supplied)", desc)
			}
		})
	}
}

// TestOAuthErrorParts_KnownSentinelWithDescription pins the description
// extraction for the wrapped form: fmt.Errorf("%w: %s", sentinel, desc).
func TestOAuthErrorParts_KnownSentinelWithDescription(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("%w: device approval window closed", deviceflow.ErrExpiredToken)
	code, desc, ok := oauthErrorParts(wrapped)
	if !ok {
		t.Fatalf("oauthErrorParts ok=false for wrapped sentinel")
	}
	if code != "expired_token" {
		t.Errorf("code = %q, want expired_token", code)
	}
	if desc != "device approval window closed" {
		t.Errorf("desc = %q, want %q", desc, "device approval window closed")
	}
}

// TestOAuthErrorParts_UnknownCodePassesThrough is the regression for
// the bug surfaced in PR review: unknown OAuth codes (e.g.
// invalid_request, invalid_client, server_error) coming back from
// deviceflow as fmt.Errorf("oauth error: %s", code) used to fall
// through to the transient-retry path in login.go's waitForApproval,
// burning ~25-150s on permanent server errors. They now land in
// DeviceAuthPoll.Error so the polling loop's default switch arm fails
// fast with "device authorization failed: <code>".
func TestOAuthErrorParts_UnknownCodePassesThrough(t *testing.T) {
	t.Parallel()

	cases := []string{"invalid_request", "invalid_client", "server_error", "unsupported_grant_type"}
	for _, want := range cases {
		t.Run(want, func(t *testing.T) {
			t.Parallel()
			err := fmt.Errorf("oauth error: %s", want)
			code, desc, ok := oauthErrorParts(err)
			if !ok {
				t.Fatalf("oauthErrorParts ok=false for unknown OAuth code %q", want)
			}
			if code != want {
				t.Errorf("code = %q, want %q", code, want)
			}
			if desc != "" {
				t.Errorf("desc = %q, want empty (no description supplied)", desc)
			}
		})
	}
}

// TestOAuthErrorParts_UnknownCodeWithDescription matches the wire
// shape deviceflow produces when the server returns both an unknown
// error code and a non-empty error_description.
func TestOAuthErrorParts_UnknownCodeWithDescription(t *testing.T) {
	t.Parallel()

	err := errors.New("oauth error: invalid_client: client authentication failed")
	code, desc, ok := oauthErrorParts(err)
	if !ok {
		t.Fatalf("oauthErrorParts ok=false")
	}
	if code != "invalid_client" {
		t.Errorf("code = %q, want invalid_client", code)
	}
	if desc != "client authentication failed" {
		t.Errorf("desc = %q, want %q", desc, "client authentication failed")
	}
}

// TestOAuthErrorParts_NonOAuthError confirms transport/decode errors
// (no "oauth error:" prefix and not an RFC 8628 sentinel) are reported
// as ok=false so the polling loop treats them as transient.
func TestOAuthErrorParts_NonOAuthError(t *testing.T) {
	t.Parallel()

	err := errors.New("connection reset by peer")
	if _, _, ok := oauthErrorParts(err); ok {
		t.Fatal("oauthErrorParts ok=true for non-OAuth error; would mask transient transport failure")
	}
}
