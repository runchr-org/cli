package auth

import (
	"errors"
	"fmt"
	"strings"

	"github.com/entireio/cli/auth/tokens"
	"github.com/entireio/cli/auth/tokenstore"
	"github.com/entireio/cli/cmd/entire/cli/api"
	"github.com/zalando/go-keyring"
)

const keyringService = "entire-cli"

// Store manages CLI authentication tokens in the OS keyring.
//
// Wraps tokenstore.Keyring with a backward-compatibility read path:
// pre-shim entries stored bare access-token strings, while the shared
// Keyring writes JSON-encoded TokenSets. GetToken transparently
// handles both shapes; SaveToken always writes the new shape.
type Store struct {
	inner *tokenstore.Keyring
}

// NewStore returns a Store backed by the system keyring.
func NewStore() *Store {
	return &Store{inner: tokenstore.NewKeyring(keyringService)}
}

// NewStoreWithService returns a Store with a custom keyring service
// name (for testing).
func NewStoreWithService(service string) *Store {
	return &Store{inner: tokenstore.NewKeyring(service)}
}

// SaveToken persists an access token for the given base URL.
func (s *Store) SaveToken(baseURL, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("refusing to save empty token")
	}
	return s.inner.SaveTokens(baseURL, tokens.TokenSet{AccessToken: token}) //nolint:wrapcheck // shim returns the lib error verbatim
}

// GetToken retrieves a stored token for the given base URL. Returns
// an empty string (and no error) if no token is stored.
//
// Falls back to a bare-string read when the stored entry is malformed
// JSON, to handle pre-shim entries that stored the raw access token
// rather than a JSON-encoded TokenSet. Real keyring errors (transport,
// permission denied) propagate; only ErrNotFound and ErrMalformed
// trigger the fallback.
func (s *Store) GetToken(baseURL string) (string, error) {
	t, err := s.inner.LoadTokens(baseURL)
	if err == nil {
		return t.AccessToken, nil
	}
	if errors.Is(err, tokenstore.ErrNotFound) {
		return "", nil
	}
	if !errors.Is(err, tokenstore.ErrMalformed) {
		return "", fmt.Errorf("load token from keyring: %w", err)
	}

	raw, kerr := keyring.Get(s.inner.Service, baseURL)
	if errors.Is(kerr, keyring.ErrNotFound) {
		return "", nil
	}
	if kerr != nil {
		return "", fmt.Errorf("get token from keyring: %w", kerr)
	}
	return raw, nil
}

// DeleteToken removes a stored token for the given base URL.
func (s *Store) DeleteToken(baseURL string) error {
	return s.inner.DeleteTokens(baseURL) //nolint:wrapcheck // shim returns the lib error verbatim
}

// SaveTokens implements tokenstore.Store. Used by the tokenmanager.
func (s *Store) SaveTokens(profile string, t tokens.TokenSet) error {
	return s.inner.SaveTokens(profile, t) //nolint:wrapcheck // shim returns the lib error verbatim
}

// LoadTokens implements tokenstore.Store, preserving the legacy bare-string
// fallback path so users with pre-shim keyring entries don't appear logged
// out after upgrading.
//
// Falls back to a bare-string read when the stored entry is malformed
// JSON (pre-shim entries stored the raw access token verbatim). Real
// keyring errors (transport, permission denied) propagate; only
// ErrMalformed triggers the fallback. ErrNotFound surfaces verbatim
// so the manager's "not logged in" branch still works.
func (s *Store) LoadTokens(profile string) (tokens.TokenSet, error) {
	t, err := s.inner.LoadTokens(profile)
	if err == nil {
		return t, nil
	}
	if !errors.Is(err, tokenstore.ErrMalformed) {
		return tokens.TokenSet{}, err //nolint:wrapcheck // surface ErrNotFound and real keyring errors verbatim
	}

	raw, kerr := keyring.Get(s.inner.Service, profile)
	if errors.Is(kerr, keyring.ErrNotFound) {
		return tokens.TokenSet{}, tokenstore.ErrNotFound
	}
	if kerr != nil {
		return tokens.TokenSet{}, fmt.Errorf("get token from keyring: %w", kerr)
	}
	return tokens.TokenSet{AccessToken: raw}, nil
}

// DeleteTokens implements tokenstore.Store.
func (s *Store) DeleteTokens(profile string) error {
	return s.inner.DeleteTokens(profile) //nolint:wrapcheck // shim returns the lib error verbatim
}

// LookupCurrentToken retrieves the token for the current auth base URL.
// Tokens are keyed by the auth issuer (api.AuthBaseURL()) since that's the
// host that minted them; in single-host deployments AuthBaseURL falls back
// to BaseURL so behaviour is unchanged.
func LookupCurrentToken() (string, error) {
	return NewStore().GetToken(api.AuthBaseURL())
}
