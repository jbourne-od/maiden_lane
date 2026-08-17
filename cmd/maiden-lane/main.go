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

	"github.com/optimaldynamics/maiden-lane/internal/adapters/memory"
	"github.com/optimaldynamics/maiden-lane/internal/adapters/postgres"
	"github.com/optimaldynamics/maiden-lane/internal/app"
	"github.com/optimaldynamics/maiden-lane/internal/httpapi"
	"github.com/optimaldynamics/maiden-lane/internal/observability"
	"github.com/optimaldynamics/maiden-lane/internal/ports"
	"github.com/optimaldynamics/maiden-lane/internal/worker"
)

const (
	defaultListenAddress     = ":8080"
	readHeaderTimeout        = 5 * time.Second
	idleTimeout              = 60 * time.Second
	shutdownTimeout          = 10 * time.Second
	telemetryShutdownTimeout = 10 * time.Second
)

var version = "devel"

// observabilityRuntime is the consumer-owned narrow view of the telemetry
// runtime this process needs: an observer for the semantic use case, and
// lifecycle shutdown.
type observabilityRuntime interface {
	SemanticObserver() app.Observer
	InstrumentHTTPRoute(method, pattern string, next http.Handler) http.Handler
	Shutdown(context.Context) error
}

type serveCommand func(context.Context, string, *slog.Logger, http.Handler, *log.Logger) error

// planStore is what this process serves plans and executions from. Both ports
// are satisfied by one adapter, so the queue and the plans it references cannot
// end up in different stores.
type planStore interface {
	ports.PlanStore
	ports.ExecutionStore
}

// productionSpineRunner adapts the application use case to the worker's
// consumer-owned interface.
type productionSpineRunner struct{}

func (productionSpineRunner) Run(ctx context.Context, request app.Request, observer app.Observer) (app.SpineResult, error) {
	return app.Run(ctx, request, observer)
}

// openPlanStore selects the store from configuration.
//
// databaseURLVariable is read rather than defaulted, so a local run needs no
// database. What it must never do is fall back: a configured URL that cannot be
// reached blocks startup, because quietly serving from memory when durability
// was asked for is the worst available outcome. Nothing would look wrong until
// the first restart, by which point the artifacts are already gone.
const databaseURLVariable = "MAIDEN_LANE_DATABASE_URL"

func openPlanStore(ctx context.Context, lookupEnv observability.LookupEnv) (planStore, func(), error) {
	url, present := lookupEnv(databaseURLVariable)
	if !present || url == "" {
		return memory.NewStore(), func() {}, nil
	}
	store, err := postgres.Open(ctx, url)
	if err != nil {
		return nil, nil, err
	}
	return store, store.Close, nil
}

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

	// Composition happens here, near the entry point, rather than through any
	// package-level state.
	planStore, closeStore, storeErr := openPlanStore(ctx, deps.lookupEnv)
	if storeErr != nil {
		// The connection string may carry a credential, so the cause is logged
		// as a bounded code rather than rendered.
		logger.Error("plan storage unavailable", "code", "plan_storage_unavailable")
		return storeErr
	}
	defer closeStore()
	if _, durable := deps.lookupEnv(databaseURLVariable); durable {
		logger.Info("plan storage is durable", "code", "plan_storage_durable")
	} else {
		// Said plainly at startup, because an operator who expected durability
		// should learn it here and not after a restart.
		logger.Warn("plan storage is in memory and will not survive a restart",
			"code", "plan_storage_ephemeral")
	}

	apiDependencies := httpapi.Dependencies{
		Plans:        planStore,
		Executions:   planStore,
		Observer:     runtime.SemanticObserver(),
		Instrumenter: runtime,
	}

	// The worker runs in this process unless disabled.
	//
	// It must, by default, because of what the queue is. With in-memory storage
	// a separate worker process cannot see the queue at all, so an enqueued
	// execution would never run and every read would report pending forever —
	// the default configuration silently useless. The API still answers 202
	// immediately either way: the worker is beside the server, never in the
	// response path, so its availability cannot affect a response.
	executionWorker := worker.New(worker.Options{
		Plans:      planStore,
		Executions: planStore,
		Runner:     productionSpineRunner{},
		Observer:   runtime.SemanticObserver(),
		Logger:     logger,
	})
	commandErr := run(ctx, args, stderr, logger,
		func(ctx context.Context, address string, logger *slog.Logger, withWorker bool) error {
			stopWorker := startWorker(ctx, executionWorker, logger, withWorker)
			defer stopWorker()
			return deps.serve(ctx, address, logger, httpapi.NewRouter(apiDependencies), errorLogger)
		},
		func(ctx context.Context, logger *slog.Logger) error {
			logger.Info("worker started", "code", "worker_started")
			return executionWorker.Run(ctx)
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
type runServeCommand func(context.Context, string, *slog.Logger, bool) error

// runWorkCommand runs a standalone worker. It is a separate command rather than a
// separate binary because the API and the worker are two modes of one image.
type runWorkCommand func(context.Context, *slog.Logger) error

func run(ctx context.Context, args []string, stderr io.Writer, logger *slog.Logger, serveCommand runServeCommand, workCommand runWorkCommand) error {
	if len(args) == 0 {
		writeUsage(stderr)
		return errors.New("command is required")
	}

	switch args[0] {
	case "serve":
		flags := flag.NewFlagSet("serve", flag.ContinueOnError)
		flags.SetOutput(stderr)
		listenAddress := flags.String("listen-address", defaultListenAddress, "TCP address on which the HTTP server listens")
		noWorker := flags.Bool("no-worker", false,
			"serve without an in-process worker; requires a separate `work` process and durable storage")
		if err := flags.Parse(args[1:]); err != nil {
			return fmt.Errorf("parse serve flags: %w", err)
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected serve arguments: %q", flags.Args())
		}
		return serveCommand(ctx, *listenAddress, logger, !*noWorker)
	case "work":
		flags := flag.NewFlagSet("work", flag.ContinueOnError)
		flags.SetOutput(stderr)
		if err := flags.Parse(args[1:]); err != nil {
			return fmt.Errorf("parse work flags: %w", err)
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected work arguments: %q", flags.Args())
		}
		return workCommand(ctx, logger)
	default:
		writeUsage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// startWorker runs the worker beside the server and returns a function that stops
// it and waits for it to drain.
//
// The returned function cancels the worker rather than only waiting for it. That
// distinction matters: the server can return for reasons other than
// cancellation, such as failing to bind its address, and a stop that merely
// waited would then block on a worker whose context is still live. Draining
// before returning is what keeps a claimed execution from sitting unclaimable for
// a whole lease interval after the process has otherwise finished.
func startWorker(ctx context.Context, executionWorker *worker.Worker, logger *slog.Logger, enabled bool) func() {
	if !enabled {
		logger.Info("serving without an in-process worker", "code", "worker_disabled")
		return func() {}
	}
	logger.Info("in-process worker started", "code", "worker_started")

	workerCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := executionWorker.Run(workerCtx); err != nil {
			logger.Error("worker stopped unexpectedly", "code", "worker_stopped")
		}
	}()
	return func() {
		stop()
		<-done
	}
}

func writeUsage(output io.Writer) {
	_, _ = fmt.Fprintln(output, "usage: maiden-lane serve [--listen-address=:8080] [--no-worker]")
	_, _ = fmt.Fprintln(output, "       maiden-lane work")
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
