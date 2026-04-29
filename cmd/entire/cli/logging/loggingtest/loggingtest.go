// Package loggingtest provides helpers for tests that need a deterministic,
// in-memory logger.
//
// Use New(t) to obtain a context pre-loaded with a debug-level logger writing
// to a *bytes.Buffer; assert on the captured records via Records(t, buf).
package loggingtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/logging"
)

// New returns a ctx pre-loaded with a debug-level logger writing to buf.
// Equivalent to NewWithLevel(t, slog.LevelDebug).
func New(t testing.TB) (context.Context, *bytes.Buffer) {
	t.Helper()
	return NewWithLevel(t, slog.LevelDebug)
}

// NewWithLevel returns a ctx pre-loaded with a logger at the requested level
// writing to a fresh *bytes.Buffer. The closer is registered via t.Cleanup.
func NewWithLevel(t testing.TB, level slog.Level) (context.Context, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	logger, closer := logging.New(context.Background(), logging.Options{
		Output: buf,
		Level:  level,
	})
	t.Cleanup(func() {
		if err := closer(); err != nil {
			t.Errorf("loggingtest: closer returned error: %v", err)
		}
	})
	return logging.WithLogger(context.Background(), logger), buf
}

// Record is a parsed JSON log line for assertions in tests.
type Record struct {
	// Time is the slog "time" field, kept as a string so tests can compare
	// against the raw value when needed.
	Time string

	// Level is the slog "level" field (e.g., "INFO", "DEBUG").
	Level string

	// Msg is the log message.
	Msg string

	// Attrs holds every other top-level JSON field attached to the record.
	Attrs map[string]any
}

// Records parses each JSONL line in buf into a Record. Lines that fail to
// parse fail the test.
func Records(t testing.TB, buf *bytes.Buffer) []Record {
	t.Helper()
	if buf.Len() == 0 {
		return nil
	}
	var records []Record
	dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
	// Loop until io.EOF rather than json.Decoder.More: More() is defined for
	// elements inside an array/object, and JSONL is a stream of top-level
	// values. EOF is the well-defined terminator for that shape.
	for {
		var raw map[string]any
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("loggingtest: parse log line: %v", err)
		}
		rec := Record{Attrs: map[string]any{}}
		for k, v := range raw {
			switch k {
			case "time":
				if s, ok := v.(string); ok {
					rec.Time = s
				}
			case "level":
				if s, ok := v.(string); ok {
					rec.Level = s
				}
			case "msg":
				if s, ok := v.(string); ok {
					rec.Msg = s
				}
			default:
				rec.Attrs[k] = v
			}
		}
		records = append(records, rec)
	}
	return records
}
