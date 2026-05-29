package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/entireio/cli/internal/coreapi"
)

// runCoreJSON runs fn against an authenticated control-plane client and
// prints its result as indented JSON. It owns the preamble every
// control-plane command shares: silence usage so input errors don't spam
// the usage block, build the client, and map an API error to a
// problem-detail SilentError. Commands supply only the call + the value to
// render.
func runCoreJSON(cmd *cobra.Command, fn func(ctx context.Context, c *coreapi.Client) (any, error)) error {
	return runCore(cmd, func(ctx context.Context, c *coreapi.Client) error {
		out, err := fn(ctx, c)
		if err != nil {
			return err
		}
		return printJSON(cmd.OutOrStdout(), out)
	})
}

// runCore is the variant for commands that don't render JSON (delete,
// revoke, remove): it runs the same preamble — silence usage, build
// client, map API errors — and leaves any success output to fn.
func runCore(cmd *cobra.Command, fn func(ctx context.Context, c *coreapi.Client) error) error {
	cmd.SilenceUsage = true
	client, err := coreapi.New()
	if err != nil {
		return fmt.Errorf("connect to Entire control plane: %w", err)
	}
	if err := fn(cmd.Context(), client); err != nil {
		return renderCoreError(err)
	}
	return nil
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
