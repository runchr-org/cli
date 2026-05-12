package auth

import (
	"errors"
	"fmt"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/api"
	"github.com/zalando/go-keyring"
)

const keyringService = "entire-cli"

// Store manages CLI authentication tokens across the configured auth sources.
// Reads resolve in priority order: the ENTIRE_AUTH_TOKEN environment variable
// (scoped to the production API origin), a user-configured file store at
// ENTIRE_SECRETS_PATH (the headless-environments path from issue #1036), or
// the pluggable tokenBackend (OS keyring in production; a separate
// file-backed backend gated behind the `authfilestore` build tag, used by
// integration tests to avoid the OS keychain).
type Store struct {
	service string
	backend tokenBackend
}

// tokenBackend abstracts token persistence. Implementations must treat
// "missing key" as a non-error: get returns ("", nil) and delete is a
// no-op so callers don't have to plumb backend-specific sentinels.
type tokenBackend interface {
	save(service, key, value string) error
	get(service, key string) (string, error)
	delete(service, key string) error
}

// chooseBackend returns the backend used by NewStore and
// NewStoreWithService. The default returns the keyring backend; the
// `authfilestore` build adds an init() that may swap in a file-backed
// backend when the test env var is set.
var chooseBackend = func() tokenBackend { return keyringBackend{} }

// NewStore returns a Store backed by the system keyring (or, in
// `authfilestore` builds, optionally a file-backed test store). When
// ENTIRE_SECRETS_PATH is set, reads and writes route to that file
// instead, ahead of the backend.
func NewStore() *Store {
	return &Store{service: keyringService, backend: chooseBackend()}
}

// NewStoreWithService returns a Store with a custom keyring service name (for testing).
// Honors the same backend selection as NewStore so tests that opt into the
// file-backed test store via env var see consistent behavior across both
// constructors.
func NewStoreWithService(service string) *Store {
	return &Store{service: service, backend: chooseBackend()}
}

// SaveToken persists an access token for the given base URL.
func (s *Store) SaveToken(baseURL, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("refusing to save empty token")
	}

	if path, ok, err := configuredSecretsPath(); err != nil {
		return err
	} else if ok {
		if err := saveFileToken(path, baseURL, token); err != nil {
			return fmt.Errorf("save token to file: %w", err)
		}
		return nil
	}

	return s.backend.save(s.service, baseURL, token)
}

// GetToken retrieves a stored token for the given base URL.
// Returns an empty string (and no error) if no token is stored.
func (s *Store) GetToken(baseURL string) (string, error) {
	info, err := s.GetTokenInfo(baseURL)
	if err != nil {
		return "", err
	}

	return info.Value, nil
}

// GetTokenInfo retrieves a stored token and reports where it came from.
// Returns an empty TokenInfo (and no error) if no token is stored.
func (s *Store) GetTokenInfo(baseURL string) (TokenInfo, error) {
	// ENTIRE_AUTH_TOKEN is scoped to the production API origin so a stray
	// ENTIRE_API_BASE_URL override can't leak a prod bearer to a staging
	// or local endpoint. Custom origins must use the per-origin file or
	// keyring stores, which are already keyed by baseURL.
	if baseURL == api.DefaultBaseURL {
		if token := envAuthToken(); token != "" {
			return TokenInfo{Value: token, Source: TokenSourceEnv}, nil
		}
	}

	if path, ok, err := configuredSecretsPath(); err != nil {
		return TokenInfo{}, err
	} else if ok {
		token, err := getFileToken(path, baseURL)
		if err != nil {
			return TokenInfo{}, fmt.Errorf("get token from file: %w", err)
		}
		if token == "" {
			return TokenInfo{}, nil
		}
		return TokenInfo{Value: token, Source: TokenSourceFile, Path: path}, nil
	}

	token, err := s.backend.get(s.service, baseURL)
	if err != nil {
		return TokenInfo{}, err
	}
	if token == "" {
		return TokenInfo{}, nil
	}
	return TokenInfo{Value: token, Source: TokenSourceKeyring}, nil
}

// DeleteToken removes a stored token for the given base URL.
// Returns no error if the token does not exist.
func (s *Store) DeleteToken(baseURL string) error {
	if path, ok, err := configuredSecretsPath(); err != nil {
		return err
	} else if ok {
		if err := deleteFileToken(path, baseURL); err != nil {
			return fmt.Errorf("delete token from file: %w", err)
		}
		return nil
	}

	return s.backend.delete(s.service, baseURL)
}

// LookupCurrentToken retrieves the token for the current base URL.
func LookupCurrentToken() (string, error) {
	store := NewStore()
	return store.GetToken(api.BaseURL())
}

type keyringBackend struct{}

func (keyringBackend) save(service, key, value string) error {
	if err := keyring.Set(service, key, value); err != nil {
		return fmt.Errorf("save token to keyring: %w", err)
	}
	return nil
}

func (keyringBackend) get(service, key string) (string, error) {
	token, err := keyring.Get(service, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get token from keyring: %w", err)
	}
	return token, nil
}

func (keyringBackend) delete(service, key string) error {
	err := keyring.Delete(service, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete token from keyring: %w", err)
	}
	return nil
}
