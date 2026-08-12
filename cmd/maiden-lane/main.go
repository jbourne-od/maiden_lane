// Command maiden-lane is the process entry point for Maiden Lane.
//
// This package owns process composition, operational command-line parsing, and
// lifecycle signals. Transformation semantics belong in inward domain packages
// and must never be introduced here.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/optimaldynamics/maiden-lane/internal/httpapi"
)

const (
	defaultListenAddress = ":8080"
	readHeaderTimeout    = 5 * time.Second
	idleTimeout          = 60 * time.Second
	shutdownTimeout      = 10 * time.Second
)

type serveCommand func(context.Context, string, *slog.Logger) error

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stderr, logger, serve); err != nil {
		logger.Error("command failed", "error", err)
		os.Exit(1)
	}
}

func run(
	ctx context.Context,
	args []string,
	stderr io.Writer,
	logger *slog.Logger,
	serveCommand serveCommand,
) error {
	if len(args) == 0 {
		writeUsage(stderr)
		return errors.New("command is required")
	}

	switch args[0] {
	case "serve":
		flags := flag.NewFlagSet("serve", flag.ContinueOnError)
		flags.SetOutput(stderr)
		listenAddress := flags.String(
			"listen-address",
			defaultListenAddress,
			"TCP address on which the HTTP server listens",
		)
		if err := flags.Parse(args[1:]); err != nil {
			return fmt.Errorf("parse serve flags: %w", err)
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected serve arguments: %q", flags.Args())
		}
		return serveCommand(ctx, *listenAddress, logger)
	default:
		writeUsage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func writeUsage(output io.Writer) {
	_, _ = fmt.Fprintln(output, "usage: maiden-lane serve [--listen-address=:8080]")
}

func serve(ctx context.Context, address string, logger *slog.Logger) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", address, err)
	}
	return serveListener(ctx, listener, logger)
}

func serveListener(ctx context.Context, listener net.Listener, logger *slog.Logger) error {
	server := &http.Server{
		Handler:           httpapi.NewRouter(),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()

	logger.Info("HTTP server started", "address", listener.Addr().String())

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		// Shutdown uses a fresh context because the process context is already
		// canceled. Reusing ctx would turn graceful shutdown into an immediate
		// abort, which is a subtle difference for readers coming from frameworks
		// that create a separate shutdown deadline automatically.
	}

	logger.Info("HTTP server stopping")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	shutdownErr := server.Shutdown(shutdownCtx)
	var closeErr error
	if shutdownErr != nil {
		closeErr = server.Close()
	}
	terminalErr := <-serveErr

	var lifecycleErrs []error
	if shutdownErr != nil {
		lifecycleErrs = append(lifecycleErrs, fmt.Errorf("shut down HTTP server: %w", shutdownErr))
	}
	if closeErr != nil {
		lifecycleErrs = append(lifecycleErrs, fmt.Errorf("force close HTTP server: %w", closeErr))
	}
	if terminalErr != nil && !errors.Is(terminalErr, http.ErrServerClosed) {
		lifecycleErrs = append(lifecycleErrs, fmt.Errorf("serve HTTP while shutting down: %w", terminalErr))
	}
	if err := errors.Join(lifecycleErrs...); err != nil {
		return err
	}

	logger.Info("HTTP server stopped")
	return nil
}
