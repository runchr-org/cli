package logging

import (
	"context"
	"testing"
)

// testComponent and testAgent are defined in logger_test.go

// These tests verify the typed-key bookkeeping that powers parent_session_id
// promotion. The attr-baking behaviour itself is covered end-to-end by
// TestEnrichmentChain_ComposesAllAttrs in logger_test.go.

func TestWithSession_StoresInContext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sessionID := "2025-01-15-test-session"

	ctx = WithSession(ctx, sessionID)

	if got := SessionIDFromContext(ctx); got != sessionID {
		t.Errorf("SessionIDFromContext() = %q, want %q", got, sessionID)
	}
}

func TestWithSession_PromotesParentInContext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	parentSessionID := "2025-01-15-parent-session"
	childSessionID := "2025-01-15-child-session"

	ctx = WithSession(ctx, parentSessionID)
	ctx = WithSession(ctx, childSessionID)

	if got := SessionIDFromContext(ctx); got != childSessionID {
		t.Errorf("SessionIDFromContext() = %q, want %q", got, childSessionID)
	}
	if got := ParentSessionIDFromContext(ctx); got != parentSessionID {
		t.Errorf("ParentSessionIDFromContext() = %q, want %q", got, parentSessionID)
	}
}

func TestWithParentSession_StoresInContext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	parentSessionID := "2025-01-15-explicit-parent"

	ctx = WithParentSession(ctx, parentSessionID)

	if got := ParentSessionIDFromContext(ctx); got != parentSessionID {
		t.Errorf("ParentSessionIDFromContext() = %q, want %q", got, parentSessionID)
	}
}

func TestContextValues_EmptyByDefault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	if got := SessionIDFromContext(ctx); got != "" {
		t.Errorf("SessionIDFromContext() on empty = %q, want empty", got)
	}
	if got := ParentSessionIDFromContext(ctx); got != "" {
		t.Errorf("ParentSessionIDFromContext() on empty = %q, want empty", got)
	}
}
