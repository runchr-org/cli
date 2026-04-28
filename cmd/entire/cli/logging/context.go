package logging

import (
	"context"
	"log/slog"

	"github.com/entireio/cli/cmd/entire/cli/agent/types"
)

// Context keys used purely for parent-session promotion bookkeeping. The
// attrs themselves are baked into the ctx-carried *slog.Logger by each With*
// helper, so the only consumer of these stored values is WithSession's lookup
// for parent_session_id.
type contextKey int

const (
	sessionIDKey contextKey = iota
	parentSessionIDKey
)

// WithSession adds a session ID to the context. The ctx-carried logger gains a
// session_id attr. If the context already has a different session, that one is
// promoted to parent_session_id on the new logger.
func WithSession(ctx context.Context, sessionID string) context.Context {
	l := LoggerFromContext(ctx)
	existing, _ := ctx.Value(sessionIDKey).(string) //nolint:errcheck // type assertion result; bool ignored intentionally
	if existing != "" && existing != sessionID {
		ctx = context.WithValue(ctx, parentSessionIDKey, existing)
		l = l.With(slog.String("parent_session_id", existing))
	}
	ctx = context.WithValue(ctx, sessionIDKey, sessionID)
	return WithLogger(ctx, l.With(slog.String("session_id", sessionID)))
}

// WithParentSession explicitly sets the parent session ID on the ctx-logger.
// Use this when you need to set the parent explicitly rather than having it
// inferred from an existing session.
func WithParentSession(ctx context.Context, parentSessionID string) context.Context {
	ctx = context.WithValue(ctx, parentSessionIDKey, parentSessionID)
	l := LoggerFromContext(ctx).With(slog.String("parent_session_id", parentSessionID))
	return WithLogger(ctx, l)
}

// WithToolCall adds a tool_call_id attr to the ctx-logger.
func WithToolCall(ctx context.Context, toolCallID string) context.Context {
	l := LoggerFromContext(ctx).With(slog.String("tool_call_id", toolCallID))
	return WithLogger(ctx, l)
}

// WithComponent adds a component attr to the ctx-logger.
// Component names help identify the subsystem generating logs (e.g., "hooks", "strategy", "session").
func WithComponent(ctx context.Context, component string) context.Context {
	l := LoggerFromContext(ctx).With(slog.String("component", component))
	return WithLogger(ctx, l)
}

// WithAgent adds an agent attr to the ctx-logger.
// Agent names identify the AI agent generating activity (e.g., "claude-code", "cursor", "aider").
func WithAgent(ctx context.Context, agentName types.AgentName) context.Context {
	l := LoggerFromContext(ctx).With(slog.String("agent", string(agentName)))
	return WithLogger(ctx, l)
}

// SessionIDFromContext returns the session ID stored by WithSession, or empty
// string if none. The slog logger in ctx is the canonical source of session_id
// in log output; this getter exists for callers that need the raw ID for
// non-logging decisions (e.g., test assertions, hook routing).
func SessionIDFromContext(ctx context.Context) string {
	if v := ctx.Value(sessionIDKey); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// ParentSessionIDFromContext returns the parent session ID stored by
// WithParentSession or by WithSession's auto-promotion, or empty string if
// none. See SessionIDFromContext for usage notes.
func ParentSessionIDFromContext(ctx context.Context) string {
	if v := ctx.Value(parentSessionIDKey); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
