package api

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Token is a single API token row returned by the auth-tokens endpoint.
// Plaintext token values are never returned by the server — only metadata.
type Token struct {
	ID         string  `json:"id"`
	UserID     string  `json:"user_id"`
	Name       string  `json:"name"`
	Scope      string  `json:"scope"`
	ExpiresAt  string  `json:"expires_at"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// TokensResponse is the envelope returned by the list endpoint.
type TokensResponse struct {
	Tokens []Token `json:"tokens"`
}

// authTokensProviderVersionEnvVar must match the env var read by
// cmd/entire/cli/auth's currentProvider(). Duplicated here rather than
// imported because api/ is a leaf package and shouldn't take a
// dependency on auth/ for routing.
const authTokensProviderVersionEnvVar = "ENTIRE_AUTH_PROVIDER_VERSION" //nolint:gosec // env var name, not a credential

// authTokensBasePath returns the auth-tokens endpoint family base path
// for the active provider version. v1 (default) hits /api/v1/auth/tokens;
// v2 hits /api/auth/tokens (no version segment).
func authTokensBasePath() string {
	if strings.TrimSpace(os.Getenv(authTokensProviderVersionEnvVar)) == "v2" {
		return "/api/auth/tokens"
	}
	return "/api/v1/auth/tokens"
}

// ListTokens returns the authenticated user's non-expired API tokens.
func (c *Client) ListTokens(ctx context.Context) ([]Token, error) {
	resp, err := c.Get(ctx, authTokensBasePath())
	if err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	defer resp.Body.Close()

	if err := CheckResponse(resp); err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}

	var out TokensResponse
	if err := DecodeJSON(resp, &out); err != nil {
		return nil, fmt.Errorf("list tokens: %w", err)
	}
	return out.Tokens, nil
}

// RevokeCurrentToken revokes the bearer token used to authenticate this client.
func (c *Client) RevokeCurrentToken(ctx context.Context) error {
	resp, err := c.Delete(ctx, authTokensBasePath()+"/current")
	if err != nil {
		return fmt.Errorf("revoke current token: %w", err)
	}
	defer resp.Body.Close()

	if err := CheckResponse(resp); err != nil {
		return fmt.Errorf("revoke current token: %w", err)
	}
	return nil
}

// RevokeToken revokes the API token with the given id.
func (c *Client) RevokeToken(ctx context.Context, id string) error {
	resp, err := c.Delete(ctx, authTokensBasePath()+"/"+url.PathEscape(id))
	if err != nil {
		return fmt.Errorf("revoke token %s: %w", id, err)
	}
	defer resp.Body.Close()

	if err := CheckResponse(resp); err != nil {
		return fmt.Errorf("revoke token %s: %w", id, err)
	}
	return nil
}
