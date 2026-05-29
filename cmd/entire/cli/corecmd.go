package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/entireio/cli/internal/coreapi"
)

// newCoreClient builds an authenticated Core API (control-plane) client
// for the logged-in user. Wraps coreapi.New so the command files share one
// construction site and a single error context.
func newCoreClient() (*coreapi.Client, error) {
	client, err := coreapi.New()
	if err != nil {
		return nil, fmt.Errorf("connect to Entire control plane: %w", err)
	}
	return client, nil
}

// markRequired marks one or more flags required, panicking if a name
// doesn't exist — that can only happen from a typo at wiring time, never
// at runtime, so a panic surfaces the bug immediately rather than letting
// a "required" flag silently not be enforced.
func markRequired(cmd *cobra.Command, names ...string) {
	for _, name := range names {
		if err := cmd.MarkFlagRequired(name); err != nil {
			panic(fmt.Sprintf("mark flag %q required: %v", name, err))
		}
	}
}

// renderCoreError converts a Core API error into a user-facing SilentError
// carrying the server's problem-detail message, falling back to the raw
// error for transport/local failures. Commands wrap their client-call
// errors with this so users see "organization name already taken" rather
// than ogen's decode-wrapped string.
func renderCoreError(err error) error {
	if err == nil {
		return nil
	}
	if msg := coreapi.APIError(err); msg != "" {
		return NewSilentError(errors.New(msg))
	}
	return err
}

// printJSON writes v as indented JSON to w. Used by the control-plane
// commands so their output is scriptable; a future --format=table flag
// can branch here.
func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode output: %w", err)
	}
	return nil
}
