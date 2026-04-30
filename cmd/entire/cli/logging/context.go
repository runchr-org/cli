package logging

import (
	"context"
	"log/slog"

	"github.com/entireio/cli/cmd/entire/cli/agent/types"
)

// Context keys for the per-request attrs that log() materialises into slog
// records. We track values here rather than via slog.With on the ctx-logger
// because slog.With appends without deduplicating: chained With("session_id",
// "a") + With("session_id", "b") emits two session_id JSON keys, which
// violates RFC 7159's SHOULD-be-unique recommendation and breaks log
// consumers that pick the first value. Building the attr list per call from
// these typed values guarantees one value per key.
type contextKey int

const (
	sessionIDKey contextKey = iota
	parentSessionIDKey
	toolCallIDKey
	componentKey
	agentKey
)

// WithSession adds a session ID to the context. log() emits session_id on
// every record. If the context already has a different session, that one is
// promoted to parent_session_id (a common pattern for subagents).
func WithSession(ctx context.Context, sessionID string) context.Context {
	if existing, ok := ctx.Value(sessionIDKey).(string); ok && existing != "" && existing != sessionID {
		ctx = context.WithValue(ctx, parentSessionIDKey, existing)
	}
	return context.WithValue(ctx, sessionIDKey, sessionID)
}

// WithParentSession explicitly sets the parent session ID on the context.
// Use this when you need to set the parent explicitly rather than having it
// inferred from an existing session.
func WithParentSession(ctx context.Context, parentSessionID string) context.Context {
	return context.WithValue(ctx, parentSessionIDKey, parentSessionID)
}

// WithToolCall adds a tool_call_id attr to log records emitted from ctx.
func WithToolCall(ctx context.Context, toolCallID string) context.Context {
	return context.WithValue(ctx, toolCallIDKey, toolCallID)
}

// WithComponent adds a component attr to log records emitted from ctx.
// Component names help identify the subsystem generating logs (e.g., "hooks", "strategy", "session").
func WithComponent(ctx context.Context, component string) context.Context {
	return context.WithValue(ctx, componentKey, component)
}

// WithAgent adds an agent attr to log records emitted from ctx.
// Agent names identify the AI agent generating activity (e.g., "claude-code", "cursor", "aider").
func WithAgent(ctx context.Context, agentName types.AgentName) context.Context {
	return context.WithValue(ctx, agentKey, string(agentName))
}

// SessionIDFromContext returns the session ID stored by WithSession, or empty
// string if none. Useful for callers that need the raw ID for non-logging
// decisions (e.g., test assertions, hook routing).
func SessionIDFromContext(ctx context.Context) string {
	return stringFromContext(ctx, sessionIDKey)
}

// ParentSessionIDFromContext returns the parent session ID stored by
// WithParentSession or by WithSession's auto-promotion, or empty string if
// none. See SessionIDFromContext for usage notes.
func ParentSessionIDFromContext(ctx context.Context) string {
	return stringFromContext(ctx, parentSessionIDKey)
}

// stringFromContext returns the string value stored under key, or "".
func stringFromContext(ctx context.Context, key contextKey) string {
	if s, ok := ctx.Value(key).(string); ok {
		return s
	}
	return ""
}

// attrsFromContext collects the typed-key attrs into a fresh slog.Attr slice
// in a stable order. Called from log() per-record to materialise enrichment
// without relying on slog.With (which accumulates and would emit duplicate
// keys across chained With* calls).
func attrsFromContext(ctx context.Context) []slog.Attr {
	if ctx == nil {
		return nil
	}
	var attrs []slog.Attr
	attrs = appendStringAttr(ctx, attrs, sessionIDKey, "session_id")
	attrs = appendStringAttr(ctx, attrs, parentSessionIDKey, "parent_session_id")
	attrs = appendStringAttr(ctx, attrs, toolCallIDKey, "tool_call_id")
	attrs = appendStringAttr(ctx, attrs, componentKey, "component")
	attrs = appendStringAttr(ctx, attrs, agentKey, "agent")
	return attrs
}

func appendStringAttr(ctx context.Context, attrs []slog.Attr, key contextKey, name string) []slog.Attr {
	if s := stringFromContext(ctx, key); s != "" {
		return append(attrs, slog.String(name, s))
	}
	return attrs
}
