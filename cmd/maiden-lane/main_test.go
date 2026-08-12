package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRunRequiresCommand(t *testing.T) {
	t.Parallel()

	var stderr strings.Builder
	err := run(context.Background(), nil, &stderr, testLogger(), nil)
	if err == nil {
		t.Fatal("run error = nil, want a command error")
	}
	if !strings.Contains(stderr.String(), "maiden-lane serve") {
		t.Fatalf("usage = %q, want serve command", stderr.String())
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	t.Parallel()

	err := run(context.Background(), []string{"unknown"}, io.Discard, testLogger(), nil)
	if err == nil {
		t.Fatal("run error = nil, want unknown-command error")
	}
}

func TestRunPassesExplicitListenAddress(t *testing.T) {
	t.Parallel()

	var gotAddress string
	serve := func(_ context.Context, address string, _ *slog.Logger) error {
		gotAddress = address
		return nil
	}

	err := run(
		context.Background(),
		[]string{"serve", "--listen-address=127.0.0.1:9090"},
		io.Discard,
		testLogger(),
		serve,
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
		func(context.Context, string, *slog.Logger) error { return nil },
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
		errCh <- serveListener(ctx, listener, testLogger())
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

func TestServePreservesListenFailure(t *testing.T) {
	t.Parallel()

	err := serve(context.Background(), "invalid address", testLogger())
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
