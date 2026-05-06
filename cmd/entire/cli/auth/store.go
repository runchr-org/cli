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
	return s.inner.SaveTokens(baseURL, tokens.TokenSet{AccessToken: token})
}

// GetToken retrieves a stored token for the given base URL. Returns
// an empty string (and no error) if no token is stored.
//
// Falls back to a bare-string read to surface tokens written before
// the shim landed.
func (s *Store) GetToken(baseURL string) (string, error) {
	t, err := s.inner.LoadTokens(baseURL)
	if err == nil {
		return t.AccessToken, nil
	}
	if errors.Is(err, tokenstore.ErrNotFound) {
		return "", nil
	}

	// Legacy fallback: pre-shim entries stored the raw access token
	// rather than a JSON-encoded TokenSet.
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
	return s.inner.DeleteTokens(baseURL)
}

// LookupCurrentToken retrieves the token for the current base URL.
func LookupCurrentToken() (string, error) {
	return NewStore().GetToken(api.BaseURL())
}
