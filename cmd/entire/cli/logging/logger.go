// Package logging provides structured logging for the Entire CLI using slog.
//
// Convention: pass the request-scoped ctx to every Debug/Info/Warn/Error/
// LogDuration call. Never pass context.Background() or context.TODO() — those
// bypass the ctx-carried logger and route to slog.Default (stderr text), which
// silently drops session_id/component/agent enrichment. If a function needs
// to log but lacks ctx, thread one through — that's almost always the right
// fix and surfaces missing plumbing rather than hiding it.
//
// The logger lives in context.Context and is initialised once in main.go via
// New + WithLogger. Commands and hooks enrich the ctx-logger with attrs via
// WithSession / WithComponent / WithToolCall / WithAgent / WithParentSession.
//
// Usage in main.go:
//
//	level := logging.ResolveLevel(os.Getenv(logging.LogLevelEnvVar), settings.LogLevel)
//	logger, closer := logging.New(ctx, logging.Options{Level: level})
//	defer closer()
//	ctx = logging.WithLogger(ctx, logger)
//	rootCmd.ExecuteContext(ctx)
//
// Usage in commands and hooks:
//
//	ctx = logging.WithSession(ctx, sessionID)
//	logging.Info(ctx, "hook invoked",
//	    slog.String("hook", hookName),
//	    slog.String("branch", branch),
//	)
package logging

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/paths"
)

// LogLevelEnvVar is the environment variable that controls log level.
const LogLevelEnvVar = "ENTIRE_LOG_LEVEL"

// LogsDir is the directory where log files are stored (relative to repo root).
const LogsDir = ".entire/logs"

// Closer flushes and closes any IO held by a logger. Idempotent: safe to call
// multiple times. A no-op closer is returned when there is no IO to clean up.
type Closer func() error

// Options configures New.
type Options struct {
	// Output, if non-nil, is used directly. Tests pass *bytes.Buffer here.
	// If nil, New returns a logger backed by a lazy writer that opens
	// .entire/logs/entire.log on first write, falling back to stderr on
	// failure (with a one-shot stderr warning).
	Output io.Writer

	// Level is the minimum slog level. Use ResolveLevel to compute from
	// env + settings precedence.
	Level slog.Level
}

// loggerCtxKey is the private context key for *slog.Logger.
// Type-distinct from contextKey to avoid collisions.
type loggerCtxKey struct{}

// New constructs a *slog.Logger and a matching Closer.
//
// When opts.Output is nil, the logger writes to .entire/logs/entire.log via
// a lazy writer that opens the file on first write and falls back to stderr
// on failure.
//
// The returned Closer flushes and closes the underlying file (if any). It is
// idempotent — main.go's defer is the canonical caller.
func New(ctx context.Context, opts Options) (*slog.Logger, Closer) {
	if opts.Output != nil {
		return createLogger(opts.Output, opts.Level), noopCloser
	}
	lw := newLazyWriter(ctx)
	return createLogger(lw, opts.Level), lw.Close
}

// WithLogger stores l in ctx so LoggerFromContext can retrieve it later.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	if l == nil {
		return ctx
	}
	return context.WithValue(ctx, loggerCtxKey{}, l)
}

// LoggerFromContext returns the logger stored by WithLogger, or slog.Default()
// when none is present. Never returns nil.
func LoggerFromContext(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return slog.Default()
	}
	if l, ok := ctx.Value(loggerCtxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}

// ResolveLevel computes the effective slog.Level using env > settings > Info
// precedence. Empty inputs are skipped; invalid inputs fall back to Info.
func ResolveLevel(envValue, settingsValue string) slog.Level {
	for _, v := range []string{envValue, settingsValue} {
		if v == "" {
			continue
		}
		if !isValidLogLevel(v) {
			fmt.Fprintf(os.Stderr, "[entire] Warning: invalid log level %q, defaulting to INFO\n", v)
			return slog.LevelInfo
		}
		return parseLogLevel(v)
	}
	return slog.LevelInfo
}

func noopCloser() error { return nil }

// createLogger creates a JSON logger writing to the given writer at the specified level.
func createLogger(w io.Writer, level slog.Level) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: level,
	}
	handler := slog.NewJSONHandler(w, opts)
	return slog.New(handler)
}

// parseLogLevel parses a log level string to slog.Level.
// Returns slog.LevelInfo for empty or invalid values.
func parseLogLevel(s string) slog.Level {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG":
		return slog.LevelDebug
	case "INFO":
		return slog.LevelInfo
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// isValidLogLevel checks if the given string is a valid log level.
func isValidLogLevel(s string) bool {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG", "INFO", "WARN", "WARNING", "ERROR", "":
		return true
	default:
		return false
	}
}

// Debug logs at DEBUG level using the ctx-carried logger.
func Debug(ctx context.Context, msg string, attrs ...any) {
	log(ctx, slog.LevelDebug, msg, attrs...)
}

// Info logs at INFO level using the ctx-carried logger.
func Info(ctx context.Context, msg string, attrs ...any) {
	log(ctx, slog.LevelInfo, msg, attrs...)
}

// Warn logs at WARN level using the ctx-carried logger.
func Warn(ctx context.Context, msg string, attrs ...any) {
	log(ctx, slog.LevelWarn, msg, attrs...)
}

// Error logs at ERROR level using the ctx-carried logger.
func Error(ctx context.Context, msg string, attrs ...any) {
	log(ctx, slog.LevelError, msg, attrs...)
}

// LogDuration logs a message with duration_ms calculated from the start time.
// Designed for use with defer:
//
//	defer logging.LogDuration(ctx, slog.LevelInfo, "operation completed", time.Now())
func LogDuration(ctx context.Context, level slog.Level, msg string, start time.Time, attrs ...any) {
	durationMs := time.Since(start).Milliseconds()
	allAttrs := make([]any, 0, len(attrs)+1)
	allAttrs = append(allAttrs, slog.Int64("duration_ms", durationMs))
	allAttrs = append(allAttrs, attrs...)
	log(ctx, level, msg, allAttrs...)
}

// log routes a record to the ctx-carried logger, materialising the typed-key
// enrichment attrs (session_id, component, …) from ctx as slog.Attrs.
//
// The logger value in ctx is immutable for the lifetime of the request and the
// file closer fires only after main.go's defer (i.e., after ExecuteContext
// returns), so no synchronisation is needed here.
//
// Attrs are appended fresh per call rather than baked into the logger via
// slog.With to avoid duplicate JSON keys when WithSession nests (slog.With
// accumulates without deduplicating).
func log(ctx context.Context, level slog.Level, msg string, attrs ...any) {
	ctxAttrs := attrsFromContext(ctx)
	if len(ctxAttrs) == 0 {
		LoggerFromContext(ctx).Log(ctx, level, msg, attrs...)
		return
	}
	allAttrs := make([]any, 0, len(ctxAttrs)+len(attrs))
	for _, a := range ctxAttrs {
		allAttrs = append(allAttrs, a)
	}
	allAttrs = append(allAttrs, attrs...)
	LoggerFromContext(ctx).Log(ctx, level, msg, allAttrs...)
}

// lazyWriter opens .entire/logs/entire.log on first write, falling back to
// stderr if the open fails. Subsequent writes go to the chosen target.
//
// Designed so that invocations that emit no log lines (e.g., entire --version)
// never create the file. Closer is sync.Once-guarded; safe to call multiple
// times.
type lazyWriter struct {
	ctx       context.Context
	openOnce  sync.Once
	closeOnce sync.Once

	target  io.Writer
	flush   func() error
	closeFn func() error
}

func newLazyWriter(ctx context.Context) *lazyWriter {
	if ctx == nil {
		ctx = context.Background()
	}
	return &lazyWriter{ctx: ctx}
}

// Write implements io.Writer. The first call resolves the underlying target
// (file or stderr); subsequent calls reuse it.
func (w *lazyWriter) Write(p []byte) (int, error) {
	w.openOnce.Do(w.resolveTarget)
	return w.target.Write(p) //nolint:wrapcheck // io.Writer pass-through; wrapping would obscure the underlying error
}

func (w *lazyWriter) resolveTarget() {
	// Use a non-cancellable view of the request ctx so SIGINT doesn't prevent
	// us from opening the log file during shutdown — the user usually wants
	// the shutdown trace to land on disk.
	repoRoot, err := paths.WorktreeRoot(context.WithoutCancel(w.ctx))
	if err != nil {
		w.useStderr(fmt.Errorf("repo root not found: %w", err))
		return
	}
	logsPath := filepath.Join(repoRoot, LogsDir)
	if err := os.MkdirAll(logsPath, 0o750); err != nil {
		w.useStderr(fmt.Errorf("mkdir %s: %w", logsPath, err))
		return
	}
	logFilePath := filepath.Join(logsPath, "entire.log")
	f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // fixed filename, not user-controlled
	if err != nil {
		w.useStderr(fmt.Errorf("open %s: %w", logFilePath, err))
		return
	}
	buf := bufio.NewWriterSize(f, 8192)
	w.target = buf
	w.flush = buf.Flush
	w.closeFn = f.Close
}

func (w *lazyWriter) useStderr(reason error) {
	fmt.Fprintf(os.Stderr, "[entire] log file unavailable: %v; logs going to stderr\n", reason)
	w.target = os.Stderr
	w.flush = func() error { return nil }
	w.closeFn = func() error { return nil }
}

// Close flushes and closes the underlying file. Idempotent.
func (w *lazyWriter) Close() error {
	var err error
	w.closeOnce.Do(func() {
		// If openOnce never fired (no writes happened), there's nothing to do.
		if w.flush == nil {
			return
		}
		if ferr := w.flush(); ferr != nil {
			err = ferr
		}
		if cerr := w.closeFn(); cerr != nil && err == nil {
			err = cerr
		}
	})
	return err
}
