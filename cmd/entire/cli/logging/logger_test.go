package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Test constants to avoid goconst warnings
const (
	testSessionID = "2025-01-15-test-session"
	testComponent = "hooks"
	testAgent     = "claude-code"
)

// initGitRepo initialises a git repo in dir so tests that exercise the lazy
// writer's repo-root resolution can succeed.
func initGitRepo(t testing.TB, dir string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", "init")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
}

// withTestLogger returns a ctx pre-loaded with a debug-level logger writing to
// the returned buffer. Cleanup is registered on t.
//
// Mirrors loggingtest.New but lives in this package so tests inside `logging`
// avoid an import cycle with `logging/loggingtest`.
func withTestLogger(t testing.TB) (context.Context, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	logger, closer := New(context.Background(), Options{
		Output: buf,
		Level:  slog.LevelDebug,
	})
	t.Cleanup(func() {
		if err := closer(); err != nil {
			t.Errorf("closer error: %v", err)
		}
	})
	return WithLogger(context.Background(), logger), buf
}

// firstRecord parses the first JSONL line from buf into a map; fails the test
// if the buffer is empty or unparseable.
func firstRecord(t testing.TB, buf *bytes.Buffer) map[string]any {
	t.Helper()
	if buf.Len() == 0 {
		t.Fatal("expected at least one log record, buffer is empty")
	}
	rec := map[string]any{}
	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	if err := dec.Decode(&rec); err != nil {
		t.Fatalf("parse log record: %v", err)
	}
	return rec
}

func TestParseLogLevel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"DEBUG", slog.LevelDebug},
		{"debug", slog.LevelDebug},
		{"INFO", slog.LevelInfo},
		{"info", slog.LevelInfo},
		{"WARN", slog.LevelWarn},
		{"WARNING", slog.LevelWarn},
		{"ERROR", slog.LevelError},
		{"", slog.LevelInfo},
		{"bogus", slog.LevelInfo},
	}
	for _, tt := range tests {
		got := parseLogLevel(tt.input)
		if got != tt.want {
			t.Errorf("parseLogLevel(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestResolveLevel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		env, settings string
		want          slog.Level
	}{
		{"defaults to info", "", "", slog.LevelInfo},
		{"env wins over settings", "DEBUG", "WARN", slog.LevelDebug},
		{"settings used when env empty", "", "WARN", slog.LevelWarn},
		{"invalid env falls back", "bogus", "", slog.LevelInfo},
		{"invalid settings falls back", "", "bogus", slog.LevelInfo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ResolveLevel(tt.env, tt.settings)
			if got != tt.want {
				t.Errorf("ResolveLevel(%q, %q) = %v, want %v", tt.env, tt.settings, got, tt.want)
			}
		})
	}
}

func TestNew_OutputBufferOverridesLazyFile(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger, closer := New(context.Background(), Options{Output: &buf, Level: slog.LevelDebug})
	t.Cleanup(func() {
		if err := closer(); err != nil {
			t.Errorf("closer: %v", err)
		}
	})

	logger.Info("hello", slog.String("foo", "bar"))

	if !strings.Contains(buf.String(), `"msg":"hello"`) {
		t.Errorf("expected hello in output, got: %s", buf.String())
	}
}

func TestNew_LazyOpensFile(t *testing.T) {
	// Cannot use t.Parallel: t.Chdir mutates process-global state.
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)
	t.Chdir(tmpDir)

	logger, closer := New(context.Background(), Options{Level: slog.LevelDebug})
	t.Cleanup(func() {
		if err := closer(); err != nil {
			t.Errorf("closer: %v", err)
		}
	})

	logger.Info("first record")
	if err := closer(); err != nil {
		t.Fatalf("closer: %v", err)
	}

	logFile := filepath.Join(tmpDir, LogsDir, "entire.log")
	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(content), `"msg":"first record"`) {
		t.Errorf("expected log file to contain message, got: %s", content)
	}
}

func TestNew_NoFileWhenNoWrites(t *testing.T) {
	// Cannot use t.Parallel: t.Chdir mutates process-global state.
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)
	t.Chdir(tmpDir)

	_, closer := New(context.Background(), Options{Level: slog.LevelDebug})
	if err := closer(); err != nil {
		t.Fatalf("closer: %v", err)
	}

	logFile := filepath.Join(tmpDir, LogsDir, "entire.log")
	if _, err := os.Stat(logFile); !os.IsNotExist(err) {
		t.Errorf("expected log file to not exist when nothing was logged")
	}
}

func TestNew_FallsBackToStderrOnIOFailure(t *testing.T) {
	// Cannot use t.Parallel: t.Chdir mutates process-global state.
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)
	t.Chdir(tmpDir)

	entireDir := filepath.Join(tmpDir, ".entire")
	if err := os.MkdirAll(entireDir, 0o755); err != nil {
		t.Fatalf("mkdir entire dir: %v", err)
	}
	if err := os.Chmod(entireDir, 0o500); err != nil {
		t.Fatalf("chmod entire dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(entireDir, 0o755); err != nil {
			t.Logf("cleanup chmod: %v", err)
		}
	})

	logger, closer := New(context.Background(), Options{Level: slog.LevelDebug})
	t.Cleanup(func() {
		if err := closer(); err != nil {
			t.Errorf("closer: %v", err)
		}
	})

	// First write triggers the open attempt; should not panic. The lazy writer
	// emits a warning to stderr and routes the record there.
	logger.Info("expect stderr fallback")
}

func TestCloser_Idempotent(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	_, closer := New(context.Background(), Options{Output: &buf, Level: slog.LevelDebug})
	if err := closer(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := closer(); err != nil {
		t.Errorf("second close should be a no-op, got: %v", err)
	}
}

func TestLoggerFromContext_FallbackToDefault(t *testing.T) {
	t.Parallel()
	if LoggerFromContext(context.Background()) == nil {
		t.Fatal("expected non-nil fallback for empty ctx")
	}
	if LoggerFromContext(nil) == nil { //nolint:staticcheck // testing nil-safety
		t.Fatal("expected non-nil fallback for nil ctx")
	}
}

func TestWithLogger_RoundTrip(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger, _ := New(context.Background(), Options{Output: &buf, Level: slog.LevelDebug})
	ctx := WithLogger(context.Background(), logger)

	got := LoggerFromContext(ctx)
	if got != logger {
		t.Errorf("LoggerFromContext returned different logger than WithLogger stored")
	}
}

func TestWithSession_BakesAttr(t *testing.T) {
	t.Parallel()
	ctx, buf := withTestLogger(t)
	ctx = WithSession(ctx, testSessionID)
	Info(ctx, "session check")

	rec := firstRecord(t, buf)
	if rec["session_id"] != testSessionID {
		t.Errorf("session_id = %v, want %q", rec["session_id"], testSessionID)
	}
}

func TestWithSession_PromotesParentFromExisting(t *testing.T) {
	t.Parallel()
	parent, child := "parent-session-id", "child-session-id"
	ctx, buf := withTestLogger(t)
	ctx = WithSession(ctx, parent)
	ctx = WithSession(ctx, child)
	Info(ctx, "child")

	rec := firstRecord(t, buf)
	if rec["session_id"] != child {
		t.Errorf("session_id = %v, want %q", rec["session_id"], child)
	}
	if rec["parent_session_id"] != parent {
		t.Errorf("parent_session_id = %v, want %q", rec["parent_session_id"], parent)
	}
}

func TestWithParentSession_AttachesParent(t *testing.T) {
	t.Parallel()
	ctx, buf := withTestLogger(t)
	ctx = WithParentSession(ctx, "explicit-parent")
	Info(ctx, "explicit")

	rec := firstRecord(t, buf)
	if rec["parent_session_id"] != "explicit-parent" {
		t.Errorf("parent_session_id = %v, want explicit-parent", rec["parent_session_id"])
	}
}

func TestEnrichmentChain_ComposesAllAttrs(t *testing.T) {
	t.Parallel()
	ctx, buf := withTestLogger(t)
	ctx = WithSession(ctx, "session-1")
	ctx = WithComponent(ctx, testComponent)
	ctx = WithToolCall(ctx, "tool-1")
	ctx = WithAgent(ctx, testAgent)
	Info(ctx, "all-attrs")

	rec := firstRecord(t, buf)
	checks := map[string]string{
		"session_id":   "session-1",
		"component":    testComponent,
		"tool_call_id": "tool-1",
		"agent":        testAgent,
	}
	for key, want := range checks {
		if got := rec[key]; got != want {
			t.Errorf("attr %q = %v, want %v", key, got, want)
		}
	}
}

func TestLevels_FilterRecords(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger, closer := New(context.Background(), Options{Output: &buf, Level: slog.LevelWarn})
	t.Cleanup(func() {
		if err := closer(); err != nil {
			t.Errorf("closer: %v", err)
		}
	})
	ctx := WithLogger(context.Background(), logger)

	Debug(ctx, "skip-debug")
	Info(ctx, "skip-info")
	Warn(ctx, "include-warn")
	Error(ctx, "include-error")

	output := buf.String()
	if strings.Contains(output, "skip-debug") || strings.Contains(output, "skip-info") {
		t.Errorf("expected debug/info to be filtered at warn level, got: %s", output)
	}
	if !strings.Contains(output, "include-warn") || !strings.Contains(output, "include-error") {
		t.Errorf("expected warn+error to be present, got: %s", output)
	}
}

func TestLogDuration_AttachesDurationMS(t *testing.T) {
	t.Parallel()
	ctx, buf := withTestLogger(t)

	// Use a defer-style call to mirror real usage.
	func() {
		defer LogDuration(ctx, slog.LevelInfo, "op", nowFn())
	}()

	rec := firstRecord(t, buf)
	if _, ok := rec["duration_ms"]; !ok {
		t.Errorf("expected duration_ms in record, got: %s", buf.String())
	}
}

// nowFn is a tiny indirection so we can keep the helper free of time imports
// at top-level (other tests don't need time directly).
func nowFn() (now time.Time) { return time.Now() }

func TestLogging_ConcurrentLogToSharedLogger(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger, closer := New(context.Background(), Options{
		// Wrap buf in a synchronised writer so concurrent writes don't trip the
		// race detector — slog handlers serialise per-write but we still need a
		// thread-safe sink.
		Output: &lockedWriter{w: &buf},
		Level:  slog.LevelDebug,
	})
	t.Cleanup(func() {
		if err := closer(); err != nil {
			t.Errorf("closer: %v", err)
		}
	})
	ctx := WithLogger(context.Background(), logger)

	const (
		goroutines = 16
		iterations = 200
	)

	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := range goroutines {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			for j := range iterations {
				Info(ctx, "concurrent log",
					slog.Int("worker", worker),
					slog.Int("iteration", j),
				)
			}
		}(i)
	}

	close(start)
	wg.Wait()

	// Every goroutine emitted iterations records. Count newlines as a sanity
	// check (one per JSON line).
	got := strings.Count(buf.String(), "\n")
	if want := goroutines * iterations; got != want {
		t.Errorf("expected %d records, got %d", want, got)
	}
}

// lockedWriter serialises writes to an underlying writer.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
