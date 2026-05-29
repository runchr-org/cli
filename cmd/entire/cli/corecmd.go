package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/entireio/cli/internal/coreapi"
)

// addJSONFlag registers a persistent --json flag on a command group so its
// read subcommands (list / get) can emit the raw wire JSON instead of the
// default human table. Persistent so it's inherited by nested subcommands
// (e.g. `entire repo mirror list`).
func addJSONFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().Bool("json", false, "output raw JSON instead of a table")
}

// jsonRequested reports whether --json was set on cmd or an ancestor. A
// lookup error means the flag isn't defined on this command tree, which is
// treated as "not requested".
func jsonRequested(cmd *cobra.Command) bool {
	v, err := cmd.Flags().GetBool("json")
	return err == nil && v
}

// runCoreList fetches a slice via fn and renders it as an aligned table
// (default) or the raw wire JSON (--json). headers names the columns; row
// maps one item to its cells in the same order. The human view keeps the
// output actionable — only the columns a person acts on — while --json
// preserves the full model for scripting.
func runCoreList[T any](cmd *cobra.Command, headers []string, row func(T) []string, fn func(ctx context.Context, c *coreapi.Client) ([]T, error)) error {
	return runCore(cmd, func(ctx context.Context, c *coreapi.Client) error {
		items, err := fn(ctx, c)
		if err != nil {
			return err
		}
		if jsonRequested(cmd) {
			return printJSON(cmd.OutOrStdout(), items)
		}
		if len(items) == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "(none)")
			return nil
		}
		return printTable(cmd.OutOrStdout(), headers, items, row)
	})
}

// runCoreObject fetches a single value via fn and renders it as a vertical
// field/value list (default) or raw JSON (--json), reusing the same column
// definition as the matching list view.
func runCoreObject[T any](cmd *cobra.Command, headers []string, row func(T) []string, fn func(ctx context.Context, c *coreapi.Client) (*T, error)) error {
	return runCore(cmd, func(ctx context.Context, c *coreapi.Client) error {
		item, err := fn(ctx, c)
		if err != nil {
			return err
		}
		if jsonRequested(cmd) {
			return printJSON(cmd.OutOrStdout(), item)
		}
		return printFields(cmd.OutOrStdout(), headers, row(*item))
	})
}

// printTable writes headers plus one tab-aligned row per item. Callers
// handle the empty case (the table layer always has at least a header).
func printTable[T any](w io.Writer, headers []string, items []T, row func(T) []string) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, it := range items {
		fmt.Fprintln(tw, strings.Join(row(it), "\t"))
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("render table: %w", err)
	}
	return nil
}

// printFields writes a single record as aligned "FIELD  value" lines,
// pairing headers with values positionally.
func printFields(w io.Writer, headers, values []string) error {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	for i, h := range headers {
		var v string
		if i < len(values) {
			v = values[i]
		}
		fmt.Fprintf(tw, "%s\t%s\n", h, v)
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("render fields: %w", err)
	}
	return nil
}

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

// printJSON writes v as indented JSON to w — the --json view for list/get
// and the default for create commands that echo the new object.
func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode output: %w", err)
	}
	return nil
}
