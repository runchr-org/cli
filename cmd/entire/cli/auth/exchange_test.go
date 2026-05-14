package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/entireio/auth-go/sts"
	"github.com/entireio/auth-go/tokenmanager"
	"github.com/entireio/auth-go/tokens"
	"github.com/entireio/auth-go/tokenstore"
)

// memStoreForExchange mirrors the tokenmanager test helper but local
// to this package — the cmd-side test only exercises wiring, so we
// don't import the manager's test fixtures.
type memStoreForExchange struct {
	data map[string]tokens.TokenSet
}

func (s *memStoreForExchange) SaveTokens(profile string, t tokens.TokenSet) error {
	s.data[profile] = t
	return nil
}

func (s *memStoreForExchange) LoadTokens(profile string) (tokens.TokenSet, error) {
	t, ok := s.data[profile]
	if !ok {
		return tokens.TokenSet{}, tokenstore.ErrNotFound
	}
	return t, nil
}

func (s *memStoreForExchange) DeleteTokens(profile string) error {
	delete(s.data, profile)
	return nil
}

// TestTokenForResource_DelegatesToManager verifies the cmd-side shim
// forwards calls to whatever Manager SetManagerForTest installs. The
// underlying behaviour (cache, exchange, audience checks) is covered
// by the tokenmanager package tests.
func TestTokenForResource_DelegatesToManager(t *testing.T) {
	// Not parallel: SetManagerForTest mutates package-level state.
	store := &memStoreForExchange{data: map[string]tokens.TokenSet{
		"https://auth.example.com": {AccessToken: "core"},
	}}
	mgr, err := tokenmanager.New(tokenmanager.Config{
		Issuer:   "https://auth.example.com",
		ClientID: "test-cli",
		STSPath:  "/sts/token",
		Store:    store,
		Exchange: func(_ context.Context, _ sts.ExchangeRequest) (*tokens.TokenSet, error) {
			return &tokens.TokenSet{AccessToken: "exchanged"}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cleanup := SetManagerForTest(t, mgr)
	t.Cleanup(cleanup)

	got, err := TokenForResource(context.Background(), "https://api.example.com")
	if err != nil {
		t.Fatalf("TokenForResource: %v", err)
	}
	if got != "exchanged" {
		t.Fatalf("got %q, want exchanged", got)
	}
}

// TestTokenForResource_ReExportsErrNotLoggedIn ensures the cmd-side
// alias matches the underlying sentinel so callers can errors.Is
// against either.
func TestTokenForResource_ReExportsErrNotLoggedIn(t *testing.T) {
	t.Parallel()
	if !errors.Is(ErrNotLoggedIn, tokenmanager.ErrNotLoggedIn) {
		t.Fatal("auth.ErrNotLoggedIn must alias tokenmanager.ErrNotLoggedIn")
	}
}
