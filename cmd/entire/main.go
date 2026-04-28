package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/entireio/cli/cmd/entire/cli"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/spf13/cobra"
)

func main() {
	// Run delegates the real work so deferred cleanup (logger close, ctx
	// cancel) runs before os.Exit. main itself only translates the result
	// into an exit code.
	os.Exit(run())
}

func run() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt signals
	sigChan := make(chan os.Signal, 1)
	signals := []os.Signal{os.Interrupt}
	if runtime.GOOS != "windows" {
		signals = append(signals, syscall.SIGTERM)
	}
	signal.Notify(sigChan, signals...)
	go func() {
		<-sigChan
		cancel()
	}()

	// Initialize the logger once for the whole process. The logger flows
	// through the cobra context; commands and hooks enrich it via
	// logging.WithSession / WithComponent / etc. The lazy writer means
	// no log file is created for invocations that emit no log lines
	// (e.g., entire --version outside a repo).
	settingsLevel := ""
	if s, err := settings.Load(ctx); err == nil {
		settingsLevel = s.LogLevel
	}
	level := logging.ResolveLevel(os.Getenv(logging.LogLevelEnvVar), settingsLevel)
	logger, closeLogger := logging.New(ctx, logging.Options{Level: level})
	defer func() {
		if err := closeLogger(); err != nil {
			fmt.Fprintf(os.Stderr, "[entire] log close: %v\n", err)
		}
	}()
	ctx = logging.WithLogger(ctx, logger)

	// Create and execute root command
	rootCmd := cli.NewRootCmd()
	err := rootCmd.ExecuteContext(ctx)
	if err == nil {
		return 0
	}

	var silent *cli.SilentError
	switch {
	case errors.As(err, &silent):
		// Command already printed the error
	case strings.Contains(err.Error(), "unknown command") || strings.Contains(err.Error(), "unknown flag"):
		showSuggestion(rootCmd, err)
	default:
		fmt.Fprintln(rootCmd.OutOrStderr(), err)
	}
	return 1
}

func showSuggestion(cmd *cobra.Command, err error) {
	// Print usage first (brew style)
	fmt.Fprint(cmd.OutOrStderr(), cmd.UsageString())
	fmt.Fprintf(cmd.OutOrStderr(), "\nError: Invalid usage: %v\n", err)
}
