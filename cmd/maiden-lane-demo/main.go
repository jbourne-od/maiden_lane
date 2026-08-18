// Command maiden-lane-demo serves a browser client for exploring one semantic run.
//
// It is a demo client, not part of the service. It is a separate binary for a
// reason worth stating: cmd/maiden-lane owns process composition for production and
// must stay free of anything that exists to be shown to people, and a UI bundled
// into the service would become a surface someone has to support.
//
// What it does is narrow. It serves a static page and reverse-proxies /v1 to a real
// Maiden Lane server, so the browser calls the documented API on the same origin
// without the service needing CORS for a demo's benefit. Every semantic claim on
// the page comes from a response the network tab shows; nothing is computed here.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

//go:embed ui
var uiFiles embed.FS

const (
	defaultListenAddress = "127.0.0.1:8090"
	defaultAPI           = "http://127.0.0.1:8080"
	defaultExamples      = "examples/teamhos"
	defaultGrafana       = "http://127.0.0.1:3000"
	readHeaderTimeout    = 5 * time.Second
	shutdownTimeout      = 5 * time.Second
)

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "maiden-lane-demo: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("maiden-lane-demo", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listenAddress := flags.String("listen-address", defaultListenAddress,
		"TCP address the demo UI listens on")
	apiBase := flags.String("api", defaultAPI,
		"base URL of the Maiden Lane server this UI drives")
	examples := flags.String("examples", defaultExamples,
		"directory holding plan.json and execution.json")
	grafana := flags.String("grafana", defaultGrafana,
		"base URL of Grafana, used only to build a trace link")
	tenant := flags.String("tenant", "acme", "tenant identifier the UI sends")
	if err := flags.Parse(args); err != nil {
		return err
	}

	handler, err := newHandler(*apiBase, *examples, *grafana, *tenant)
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	server := &http.Server{
		Addr:              *listenAddress,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errs := make(chan error, 1)
	go func() {
		logger.Info("demo UI listening", "address", *listenAddress, "api", *apiBase)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
			return
		}
		errs <- nil
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}

// newHandler builds everything the demo serves: the page, its settings, and a
// transparent proxy to a real Maiden Lane server.
//
// Construction is separated from process lifecycle so a test can exercise the whole
// surface -- including what the proxy forwards -- without binding a port or driving
// signals.
func newHandler(apiBase, examples, grafana, tenant string) (http.Handler, error) {
	target, err := url.Parse(apiBase)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("--api must be an absolute URL, got %q", apiBase)
	}

	// The starting payloads are read from disk rather than embedded, so the page
	// opens on exactly the committed examples that internal/httpapi/examples_test.go
	// pins to the ratified fixture's identities. An embedded copy would be a second
	// source able to drift from the one under test. Reading them here rather than
	// per request means a missing or malformed file stops the demo from starting,
	// instead of breaking the page once somebody is watching.
	settings, err := loadSettings(examples, grafana, tenant)
	if err != nil {
		return nil, err
	}

	assets, err := fs.Sub(uiFiles, "ui")
	if err != nil {
		return nil, fmt.Errorf("ui assets: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(assets)))
	mux.HandleFunc("/demo/settings", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(settings)
	})

	// A transparent reverse proxy. It rewrites the destination and nothing else: no
	// header injection, no body rewriting, no retries. The tenant header is set by
	// the page, so what the browser sends is what the service receives, and the
	// network tab is a truthful record of the API being exercised rather than of
	// this process's embellishments.
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(target)
			request.Out.Host = target.Host
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"title":  "Maiden Lane is not reachable",
				"detail": "The demo UI could not reach " + target.String() + ".",
			})
		},
	}
	mux.Handle("/v1/", proxy)
	mux.Handle("/healthz", proxy)
	return mux, nil
}

// settings is what the page needs to start: the committed payloads, where to send
// requests, and where a trace can be looked at.
type settings struct {
	Tenant     string          `json:"tenant"`
	Grafana    string          `json:"grafana"`
	Plan       json.RawMessage `json:"plan"`
	Execution  json.RawMessage `json:"execution"`
	SourcePath string          `json:"sourcePath"`
}

func loadSettings(examples, grafana, tenant string) (settings, error) {
	plan, err := readJSON(filepath.Join(examples, "plan.json"))
	if err != nil {
		return settings{}, err
	}
	execution, err := readJSON(filepath.Join(examples, "execution.json"))
	if err != nil {
		return settings{}, err
	}
	absolute, err := filepath.Abs(examples)
	if err != nil {
		absolute = examples
	}
	return settings{
		Tenant: tenant, Grafana: grafana,
		Plan: plan, Execution: execution, SourcePath: absolute,
	}, nil
}

// readJSON refuses a malformed file here rather than letting the page receive it.
// A demo that starts and then fails in the browser sends whoever is watching to
// look at the wrong thing.
func readJSON(path string) (json.RawMessage, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w (run this from the repository root, or pass --examples)", path, err)
	}
	if !json.Valid(content) {
		return nil, fmt.Errorf("%s is not valid JSON", path)
	}
	return json.RawMessage(content), nil
}
