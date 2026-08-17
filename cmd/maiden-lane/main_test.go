package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/optimaldynamics/maiden-lane/internal/app"
	"github.com/optimaldynamics/maiden-lane/internal/observability"
	"github.com/optimaldynamics/maiden-lane/internal/worker"
)

func TestExecuteDrainsApplicationBeforeTelemetryShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	telemetryErr := errors.New("hostile telemetry cause")
	var events []string
	runtime := &fakeObservabilityRuntime{shutdown: func(ctx context.Context) error {
		events = append(events, "telemetry-shutdown")
		if ctx.Err() != nil {
			t.Fatal("telemetry shutdown reused canceled process context")
		}
		deadline, ok := ctx.Deadline()
		remaining := time.Until(deadline)
		if !ok || remaining <= 0 || remaining > telemetryShutdownTimeout {
			t.Fatalf("shutdown deadline remaining = %v, present %t", remaining, ok)
		}
		return telemetryErr
	}}
	deps := testProcessDeps(runtime, func(context.Context, string, *slog.Logger, http.Handler, *log.Logger) error {
		events = append(events, "server-drained")
		return nil
	})

	err := execute(ctx, []string{"serve"}, io.Discard, io.Discard, deps)
	if !errors.Is(err, telemetryErr) {
		t.Fatalf("execute error = %v, want telemetry cause", err)
	}
	if got := strings.Join(events, ","); got != "server-drained,telemetry-shutdown" {
		t.Fatalf("events = %q", got)
	}
	if got := runtime.shutdownCalls(); got != 1 {
		t.Fatalf("telemetry shutdown calls = %d, want 1", got)
	}
}

func TestExecuteDefaultDisabledRuntimeUsesNoExporter(t *testing.T) {
	served := false
	deps := processDeps{
		lookupEnv: func(string) (string, bool) { return "", false },
		readFile: func(string) ([]byte, error) {
			t.Fatal("disabled observability read a TLS file")
			return nil, nil
		},
		newRuntime: func(ctx context.Context, cfg observability.Config, output io.Writer) (observabilityRuntime, *slog.Logger, error) {
			if cfg.TracesExporter != observability.ExporterNone || cfg.MetricsExporter != observability.ExporterNone {
				t.Fatalf("default exporters = (%q, %q), want none", cfg.TracesExporter, cfg.MetricsExporter)
			}
			runtime, err := observability.New(ctx, cfg, output)
			if err != nil {
				return nil, nil, err
			}
			return runtime, runtime.Logger, nil
		},
		serve: func(context.Context, string, *slog.Logger, http.Handler, *log.Logger) error {
			served = true
			return nil
		},
	}

	if err := execute(context.Background(), []string{"serve"}, io.Discard, io.Discard, deps); err != nil {
		t.Fatalf("execute with disabled runtime: %v", err)
	}
	if !served {
		t.Fatal("fake server was not invoked")
	}
}

func TestExecutePreservesCausesWithoutLoggingTheirText(t *testing.T) {
	serveErr := errors.New("hostile server secret")
	shutdownErr := errors.New("hostile shutdown secret")
	var stdout, stderr bytes.Buffer
	runtime := &fakeObservabilityRuntime{shutdown: func(context.Context) error { return shutdownErr }}
	deps := testProcessDeps(runtime, func(context.Context, string, *slog.Logger, http.Handler, *log.Logger) error {
		return serveErr
	})

	err := execute(context.Background(), []string{"serve"}, &stdout, &stderr, deps)
	if !errors.Is(err, serveErr) || !errors.Is(err, shutdownErr) {
		t.Fatalf("execute error = %v, want both causes", err)
	}
	output := stdout.String() + stderr.String()
	for _, secret := range []string{serveErr.Error(), shutdownErr.Error()} {
		if strings.Contains(output, secret) {
			t.Fatalf("logs exposed %q: %s", secret, output)
		}
	}
	for _, code := range []string{"observability_shutdown_failed"} {
		if !strings.Contains(output, code) {
			t.Fatalf("logs = %q, want code %q", output, code)
		}
	}
}

func TestExecuteConfigurationFailureStopsConstructionAndRedactsValue(t *testing.T) {
	const hostile = "hostile-header-secret"
	var stdout bytes.Buffer
	constructed := false
	deps := processDeps{
		lookupEnv: func(key string) (string, bool) {
			if key == "OTEL_TRACES_EXPORTER" {
				return hostile, true
			}
			return "", false
		},
		readFile: os.ReadFile,
		newRuntime: func(context.Context, observability.Config, io.Writer) (observabilityRuntime, *slog.Logger, error) {
			constructed = true
			return nil, nil, nil
		},
	}
	err := execute(context.Background(), []string{"serve"}, &stdout, io.Discard, deps)
	if err == nil {
		t.Fatal("execute error = nil")
	}
	if constructed {
		t.Fatal("runtime constructed after invalid configuration")
	}
	if got := stdout.String(); !strings.Contains(got, "observability_configuration_invalid") || !strings.Contains(got, "OTEL_TRACES_EXPORTER") || strings.Contains(got, hostile) {
		t.Fatalf("configuration log = %q", got)
	}
}

func TestExecuteRuntimeFailurePreventsServerStartup(t *testing.T) {
	runtimeErr := errors.New("hostile collector endpoint secret")
	var stdout bytes.Buffer
	served := false
	deps := processDeps{
		lookupEnv: func(string) (string, bool) { return "", false },
		readFile:  os.ReadFile,
		newRuntime: func(context.Context, observability.Config, io.Writer) (observabilityRuntime, *slog.Logger, error) {
			return nil, nil, runtimeErr
		},
		serve: func(context.Context, string, *slog.Logger, http.Handler, *log.Logger) error {
			served = true
			return nil
		},
	}
	err := execute(context.Background(), []string{"serve"}, &stdout, io.Discard, deps)
	if !errors.Is(err, runtimeErr) {
		t.Fatalf("execute error = %v, want runtime cause", err)
	}
	if served {
		t.Fatal("server started after runtime construction failed")
	}
	if got := stdout.String(); !strings.Contains(got, "observability_initialization_failed") || strings.Contains(got, runtimeErr.Error()) {
		t.Fatalf("initialization log = %q", got)
	}
}

func TestProcessMainLogsOnlyStableFinalFailure(t *testing.T) {
	commandErr := errors.New("hostile command failure secret")
	var stdout bytes.Buffer
	runtime := &fakeObservabilityRuntime{shutdown: func(context.Context) error { return nil }}
	deps := testProcessDeps(runtime, func(context.Context, string, *slog.Logger, http.Handler, *log.Logger) error {
		return commandErr
	})
	if code := processMain([]string{"serve"}, &stdout, io.Discard, deps); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if got := stdout.String(); !strings.Contains(got, "command_failed") || strings.Contains(got, commandErr.Error()) {
		t.Fatalf("final log = %q", got)
	}
}

func TestSafeHTTPErrorWriterDiscardsInput(t *testing.T) {
	var output bytes.Buffer
	writer := safeHTTPErrorWriter{logger: observability.NewLogger(&output, slog.LevelInfo)}
	input := []byte("hostile panic path peer and stack secret")
	count, err := writer.Write(input)
	if err != nil || count != len(input) {
		t.Fatalf("Write = (%d, %v), want (%d, nil)", count, err, len(input))
	}
	if got := output.String(); !strings.Contains(got, "http_server_internal_error") || strings.Contains(got, "hostile") || strings.Contains(got, "secret") {
		t.Fatalf("safe HTTP log = %q", got)
	}
}

func TestRunRequiresCommand(t *testing.T) {
	t.Parallel()

	var stderr strings.Builder
	err := run(context.Background(), nil, &stderr, testLogger(), nil, nil)
	if err == nil {
		t.Fatal("run error = nil, want a command error")
	}
	if !strings.Contains(stderr.String(), "maiden-lane serve") {
		t.Fatalf("usage = %q, want serve command", stderr.String())
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{"unknown"}, io.Discard, testLogger(), nil, nil)
	if err == nil {
		t.Fatal("run error = nil, want unknown-command error")
	}
}

func TestRunPassesExplicitListenAddress(t *testing.T) {
	t.Parallel()

	var gotAddress string
	serve := func(_ context.Context, address string, _ *slog.Logger, _ bool) error {
		gotAddress = address
		return nil
	}

	err := run(
		context.Background(),
		[]string{"serve", "--listen-address=127.0.0.1:9090"},
		io.Discard,
		testLogger(),
		serve,
		nil,
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotAddress != "127.0.0.1:9090" {
		t.Fatalf("listen address = %q, want %q", gotAddress, "127.0.0.1:9090")
	}
}

func TestRunRejectsUnexpectedServeArguments(t *testing.T) {
	t.Parallel()

	err := run(
		context.Background(),
		[]string{"serve", "unexpected"},
		io.Discard,
		testLogger(),
		func(context.Context, string, *slog.Logger, bool) error { return nil },
		nil,
	)
	if err == nil {
		t.Fatal("run error = nil, want unexpected-argument error")
	}
}

func TestServeListenerStopsAfterCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- serveListener(ctx, listener, testLogger(), testHandler(), testHTTPErrorLogger())
	}()

	url := "http://" + listener.Addr().String() + "/healthz"
	waitForStatus(t, url, http.StatusNoContent)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("serve after cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after cancellation")
	}
}

func TestServeListenerJoinsServeAfterShutdownFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	shutdownErr := errors.New("close listener during shutdown")
	acceptErr := &coordinatedAcceptError{
		cancel:  cancel,
		release: make(chan struct{}),
	}
	listener := &shutdownAndServeErrorListener{
		shutdownErr: shutdownErr,
		acceptErr:   acceptErr,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- serveListener(ctx, listener, testLogger(), testHandler(), testHTTPErrorLogger())
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, shutdownErr) {
			t.Fatalf("serve after shutdown failure = %v, want shutdown cause", err)
		}
		if !errors.Is(err, acceptErr) {
			t.Fatalf("serve after shutdown failure = %v, want serving goroutine cause", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop after shutdown failure")
	}
}

func TestServeListenerDrainsActiveRequestAfterUnexpectedServeError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	handler := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		writer.WriteHeader(http.StatusNoContent)
	})
	errCh := make(chan error, 1)
	go func() {
		errCh <- serveListener(context.Background(), listener, testLogger(), handler, testHTTPErrorLogger())
	}()

	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, requestErr := (&http.Client{Timeout: 2 * time.Second}).Get("http://" + listener.Addr().String())
		if requestErr == nil {
			_ = response.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	select {
	case err := <-errCh:
		t.Fatalf("serve returned before active handler drained: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("serve error = nil after unexpected listener failure")
		}
	case <-time.After(time.Second):
		t.Fatal("serve did not return after handler drained")
	}
	<-requestDone
}

func TestServeListenerSanitizesNetHTTPPanicDiagnostics(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logs bytes.Buffer
	logger := observability.NewLogger(&logs, slog.LevelInfo)
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("hostile-panic-path-peer-stack-secret")
	})
	errCh := make(chan error, 1)
	go func() {
		errCh <- serveListener(ctx, listener, logger, handler, log.New(safeHTTPErrorWriter{logger: logger}, "", 0))
	}()

	_, _ = (&http.Client{Timeout: time.Second}).Get("http://" + listener.Addr().String() + "/hostile-path-secret")
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("serve after cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
	}
	output := logs.String()
	if !strings.Contains(output, "http_server_internal_error") {
		t.Fatalf("logs = %q, want stable HTTP error code", output)
	}
	for _, secret := range []string{"hostile-panic", "hostile-path", "stack-secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("logs exposed %q: %s", secret, output)
		}
	}
}

func TestServePreservesListenFailure(t *testing.T) {
	t.Parallel()

	err := serve(context.Background(), "invalid address", testLogger(), testHandler(), testHTTPErrorLogger())
	if err == nil {
		t.Fatal("serve error = nil, want listen failure")
	}

	var operationError *net.OpError
	if !errors.As(err, &operationError) {
		t.Fatalf("serve error %T does not preserve *net.OpError", err)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func testHTTPErrorLogger() *log.Logger { return log.New(io.Discard, "", 0) }

func waitForStatus(t *testing.T, url string, want int) {
	t.Helper()

	client := &http.Client{Timeout: 250 * time.Millisecond}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(url)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == want {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s did not return status %d before deadline", url, want)
}

type shutdownAndServeErrorListener struct {
	shutdownErr error
	acceptErr   *coordinatedAcceptError
}

func (listener *shutdownAndServeErrorListener) Accept() (net.Conn, error) {
	return nil, listener.acceptErr
}

func (listener *shutdownAndServeErrorListener) Close() error {
	close(listener.acceptErr.release)
	return listener.shutdownErr
}

func (listener *shutdownAndServeErrorListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}
}

type coordinatedAcceptError struct {
	cancel  context.CancelFunc
	release chan struct{}
}

func (err *coordinatedAcceptError) Error() string {
	return "serve listener failed"
}

func (err *coordinatedAcceptError) Temporary() bool {
	err.cancel()
	<-err.release
	return false
}

func (*coordinatedAcceptError) Timeout() bool {
	return false
}

type fakeObservabilityRuntime struct {
	mu       sync.Mutex
	shutdown func(context.Context) error
	calls    int
}

// SemanticObserver returns the discarding observer. Telemetry is
// non-authoritative, so a fake that records nothing cannot change any result
// these tests assert.
func (runtime *fakeObservabilityRuntime) SemanticObserver() app.Observer {
	return app.DiscardObserver()
}

// WorkerTracer returns nil, which the worker supports. Returning a fake that
// opened spans would test this file's fake rather than the process, and an
// untraced worker must reach exactly the same outcomes anyway.
func (runtime *fakeObservabilityRuntime) WorkerTracer() worker.ExecutionTracer {
	return nil
}

// InstrumentHTTPRoute passes the handler through unchanged. Telemetry is
// non-authoritative, so a fake that measures nothing cannot change any
// behavior these tests assert.
func (runtime *fakeObservabilityRuntime) InstrumentHTTPRoute(_, _ string, next http.Handler) http.Handler {
	return next
}

func (runtime *fakeObservabilityRuntime) Shutdown(ctx context.Context) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.calls++
	return runtime.shutdown(ctx)
}

func (runtime *fakeObservabilityRuntime) shutdownCalls() int {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.calls
}

func testProcessDeps(runtime observabilityRuntime, serveCommand serveCommand) processDeps {
	return processDeps{
		lookupEnv: func(string) (string, bool) { return "", false },
		readFile:  os.ReadFile,
		newRuntime: func(_ context.Context, _ observability.Config, output io.Writer) (observabilityRuntime, *slog.Logger, error) {
			return runtime, observability.NewLogger(output, slog.LevelInfo), nil
		},
		serve: serveCommand,
	}
}

// Production break caught: falling back to in-memory storage when a database was
// configured but unreachable is the worst available failure. Nothing would look
// wrong until the first restart, by which point the artifacts are already gone.
// Refusing to start is loud, immediate, and recoverable.
func TestConfiguredButUnreachableDatabaseBlocksStartup(t *testing.T) {
	lookup := envLookup(map[string]string{
		databaseURLVariable: "postgres://nobody:secret@127.0.0.1:1/absent?sslmode=disable&connect_timeout=1",
	})

	store, closeStore, err := openPlanStore(t.Context(), lookup)
	if err == nil {
		if closeStore != nil {
			closeStore()
		}
		t.Fatal("an unreachable database yielded a usable store")
	}
	if store != nil {
		t.Fatal("a failed open returned a store to serve from")
	}
	// The connection string carries a credential, so it must not be echoed.
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("the error echoed the credential: %v", err)
	}
}

// Production break caught: a local run must need no database, or every developer
// and every test would require one.
func TestAbsentConfigurationSelectsInMemoryStorage(t *testing.T) {
	for name, lookup := range map[string]observability.LookupEnv{
		"unset": envLookup(map[string]string{}),
		"empty": envLookup(map[string]string{databaseURLVariable: ""}),
	} {
		t.Run(name, func(t *testing.T) {
			store, closeStore, err := openPlanStore(t.Context(), lookup)
			if err != nil {
				t.Fatalf("openPlanStore: %v", err)
			}
			if store == nil {
				t.Fatal("no store was selected")
			}
			if closeStore == nil {
				t.Fatal("no cleanup was returned")
			}
			closeStore()
		})
	}
}

// envLookup builds a deterministic environment for composition tests, so no test
// reads or mutates the real process environment.
func envLookup(env map[string]string) observability.LookupEnv {
	return func(key string) (string, bool) {
		value, present := env[key]
		return value, present
	}
}

// Production break caught: the work command must exist and be reachable, or the
// split deployment shape the HLD describes has no entry point.
func TestRunDispatchesTheWorkCommand(t *testing.T) {
	t.Parallel()

	called := false
	err := run(
		context.Background(),
		[]string{"work"},
		io.Discard,
		testLogger(),
		nil,
		func(context.Context, *slog.Logger) error {
			called = true
			return nil
		},
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !called {
		t.Fatal("the work command was not dispatched")
	}
}

// Production break caught: an in-process worker is the default because with
// in-memory storage a separate process cannot see the queue, so an enqueued
// execution would never run. The flag that disables it must actually reach the
// serve command rather than being silently ignored.
func TestServeReportsWhetherAWorkerRuns(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		args       []string
		withWorker bool
	}{
		{[]string{"serve"}, true},
		{[]string{"serve", "--no-worker"}, false},
	} {
		got := !test.withWorker
		err := run(
			context.Background(),
			test.args,
			io.Discard,
			testLogger(),
			func(_ context.Context, _ string, _ *slog.Logger, withWorker bool) error {
				got = withWorker
				return nil
			},
			nil,
		)
		if err != nil {
			t.Fatalf("run(%v): %v", test.args, err)
		}
		if got != test.withWorker {
			t.Errorf("run(%v) withWorker = %t, want %t", test.args, got, test.withWorker)
		}
	}
}
