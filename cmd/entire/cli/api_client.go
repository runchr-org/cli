package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/entireio/cli/cmd/entire/cli/api"
	"github.com/entireio/cli/cmd/entire/cli/auth"
)

// NewAuthenticatedAPIClient creates an API client targeting api.BaseURL()
// (the data API origin) carrying a token valid for that audience.
//
// Resolution: looks up the core token from the keyring, then either uses
// it directly (single-host setup, or when the core token's `aud` already
// covers api.BaseURL()) or performs an RFC 8693 token exchange against
// the auth host to obtain a token scoped to the data API. Exchanged
// tokens are cached in-memory per (core-token, resource) pair.
//
// Pass insecureHTTP=true to allow plain HTTP base URLs for local
// development. Both api.BaseURL() and api.AuthBaseURL() are validated:
// the bearer travels to the data host on resource requests, and the
// core token travels to the auth host during the exchange step.
func NewAuthenticatedAPIClient(ctx context.Context, insecureHTTP bool) (*api.Client, error) {
	if !insecureHTTP {
		if err := api.RequireSecureURL(api.BaseURL()); err != nil {
			return nil, fmt.Errorf("base URL check: %w", err)
		}
		if err := api.RequireSecureURL(api.AuthBaseURL()); err != nil {
			return nil, fmt.Errorf("auth base URL check: %w", err)
		}
	}

	token, err := auth.TokenForResource(ctx, api.BaseURL())
	if err != nil {
		if errors.Is(err, auth.ErrNotLoggedIn) {
			return nil, errors.New("not logged in (run 'entire login' first)")
		}
		return nil, fmt.Errorf("resolve API token: %w", err)
	}

	return api.NewClient(token), nil
}
