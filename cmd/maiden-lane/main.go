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
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/optimaldynamics/maiden-lane/internal/httpapi"
	"github.com/optimaldynamics/maiden-lane/internal/observability"
)

const (
	defaultListenAddress     = ":8080"
	readHeaderTimeout        = 5 * time.Second
	idleTimeout              = 60 * time.Second
	shutdownTimeout          = 10 * time.Second
	telemetryShutdownTimeout = 10 * time.Second
)

var version = "devel"

type observabilityRuntime interface {
	Shutdown(context.Context) error
}

type serveCommand func(context.Context, string, *slog.Logger, http.Handler, *log.Logger) error

type processDeps struct {
	lookupEnv  observability.LookupEnv
	readFile   observability.ReadFile
	newRuntime func(context.Context, observability.Config, io.Writer) (observabilityRuntime, *slog.Logger, error)
	serve      serveCommand
}

func main() {
	os.Exit(processMain(os.Args[1:], os.Stdout, os.Stderr, productionDeps()))
}

// processMain returns an exit code instead of exiting directly. Its deferred
// signal cleanup therefore runs before main calls os.Exit, whose abrupt process
// termination does not execute Go defers.
func processMain(args []string, stdout, stderr io.Writer, deps processDeps) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := execute(ctx, args, stdout, stderr, deps); err != nil {
		observability.NewLogger(stdout, slog.LevelInfo).Error("command failed", "code", "command_failed")
		return 1
	}
	return 0
}

func productionDeps() processDeps {
	return processDeps{
		lookupEnv: os.LookupEnv,
		readFile:  os.ReadFile,
		newRuntime: func(ctx context.Context, cfg observability.Config, output io.Writer) (observabilityRuntime, *slog.Logger, error) {
			runtime, err := observability.New(ctx, cfg, output)
			if err != nil {
				return nil, nil, err
			}
			return runtime, runtime.Logger, nil
		},
		serve: serve,
	}
}

func execute(ctx context.Context, args []string, stdout, stderr io.Writer, deps processDeps) error {
	bootstrap := observability.NewLogger(stdout, slog.LevelInfo)
	cfg, err := observability.LoadConfig(deps.lookupEnv, deps.readFile, version)
	if err != nil {
		// LoadConfig errors contain a bounded field name and fixed expectation,
		// never the rejected endpoint, header, path, or other configured value.
		bootstrap.Error("observability configuration invalid", "code", "observability_configuration_invalid", "error", err)
		return err
	}

	runtime, logger, err := deps.newRuntime(ctx, cfg, stdout)
	if err != nil {
		bootstrap.Error("observability initialization failed", "code", "observability_initialization_failed")
		return err
	}
	if runtime == nil || logger == nil {
		return errors.New("observability runtime construction returned an incomplete result")
	}
	logger.Info(
		"observability initialized",
		"traces_exporter", string(cfg.TracesExporter),
		"metrics_exporter", string(cfg.MetricsExporter),
	)

	errorLogger := log.New(safeHTTPErrorWriter{logger: logger}, "", 0)
	commandErr := run(ctx, args, stderr, logger, func(ctx context.Context, address string, logger *slog.Logger) error {
		return deps.serve(ctx, address, logger, httpapi.NewRouter(), errorLogger)
	})

	// The process context may already be canceled. A fresh bounded context lets
	// providers flush after all application work has drained without allowing
	// shutdown to hang indefinitely.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), telemetryShutdownTimeout)
	shutdownErr := runtime.Shutdown(shutdownCtx)
	cancel()
	if shutdownErr != nil {
		logger.Error("observability shutdown failed", "code", "observability_shutdown_failed")
	}
	return errors.Join(commandErr, shutdownErr)
}

// safeHTTPErrorWriter is the only sink used by net/http's internal ErrorLog.
// Standard-library diagnostics may include paths, peer addresses, and panic
// stacks, so the bytes are acknowledged but deliberately discarded.
type safeHTTPErrorWriter struct {
	logger *slog.Logger
}

func (writer safeHTTPErrorWriter) Write(data []byte) (int, error) {
	writer.logger.Error("HTTP server internal failure", "code", "http_server_internal_error")
	return len(data), nil
}

// run retains the small command parser as a separately testable concern.
type runServeCommand func(context.Context, string, *slog.Logger) error

func run(ctx context.Context, args []string, stderr io.Writer, logger *slog.Logger, serveCommand runServeCommand) error {
	if len(args) == 0 {
		writeUsage(stderr)
		return errors.New("command is required")
	}

	switch args[0] {
	case "serve":
		flags := flag.NewFlagSet("serve", flag.ContinueOnError)
		flags.SetOutput(stderr)
		listenAddress := flags.String("listen-address", defaultListenAddress, "TCP address on which the HTTP server listens")
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

func serve(ctx context.Context, address string, logger *slog.Logger, handler http.Handler, errorLogger *log.Logger) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", address, err)
	}
	return serveListener(ctx, listener, logger, handler, errorLogger)
}

func serveListener(ctx context.Context, listener net.Listener, logger *slog.Logger, handler http.Handler, errorLogger *log.Logger) error {
	server := &http.Server{
		Handler:           handler,
		ErrorLog:          errorLogger,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	logger.Info("HTTP server started", "address", listener.Addr().String())

	var terminalErr error
	select {
	case terminalErr = <-serveErr:
	case <-ctx.Done():
	}

	// Shutdown is required even after an unexpected accept-loop failure: an
	// already accepted handler may still be running. Draining here guarantees
	// process composition cannot flush telemetry ahead of application work.
	logger.Info("HTTP server stopping")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	shutdownErr := server.Shutdown(shutdownCtx)
	cancel()
	var closeErr error
	if shutdownErr != nil {
		closeErr = server.Close()
	}
	if terminalErr == nil {
		terminalErr = <-serveErr
	}

	var lifecycleErrs []error
	if terminalErr != nil && !errors.Is(terminalErr, http.ErrServerClosed) {
		lifecycleErrs = append(lifecycleErrs, fmt.Errorf("serve HTTP: %w", terminalErr))
	}
	if shutdownErr != nil {
		lifecycleErrs = append(lifecycleErrs, fmt.Errorf("shut down HTTP server: %w", shutdownErr))
	}
	if closeErr != nil {
		lifecycleErrs = append(lifecycleErrs, fmt.Errorf("force close HTTP server: %w", closeErr))
	}
	if err := errors.Join(lifecycleErrs...); err != nil {
		return err
	}
	logger.Info("HTTP server stopped")
	return nil
}
