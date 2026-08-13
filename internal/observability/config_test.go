// Package observability owns Maiden Lane's explicitly configured operational
// telemetry boundary. It must not admit arbitrary OpenTelemetry environment
// policy or expose configured secrets through validation errors.
package observability

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"log/slog"
)

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig(emptyEnv, rejectRead, "devel")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.LogLevel != slog.LevelInfo || cfg.TracesExporter != ExporterNone ||
		cfg.MetricsExporter != ExporterNone || cfg.ServiceName != "maiden-lane" ||
		cfg.ServiceVersion != "" || !cfg.validated {
		t.Fatalf("defaults = %#v", cfg)
	}
}

func TestLoadConfigAcceptsClosedTopLevelValues(t *testing.T) {
	for _, test := range []struct {
		name  string
		env   map[string]string
		level slog.Level
		trace ExporterMode
		met   ExporterMode
	}{
		{"debug", map[string]string{"LOG_LEVEL": "debug"}, slog.LevelDebug, ExporterNone, ExporterNone},
		{"info", map[string]string{"LOG_LEVEL": "info"}, slog.LevelInfo, ExporterNone, ExporterNone},
		{"warn", map[string]string{"LOG_LEVEL": "warn"}, slog.LevelWarn, ExporterNone, ExporterNone},
		{"error", map[string]string{"LOG_LEVEL": "error"}, slog.LevelError, ExporterNone, ExporterNone},
		{"traces only", map[string]string{"OTEL_TRACES_EXPORTER": "otlp"}, slog.LevelInfo, ExporterOTLP, ExporterNone},
		{"metrics only", map[string]string{"OTEL_METRICS_EXPORTER": "otlp"}, slog.LevelInfo, ExporterNone, ExporterOTLP},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := LoadConfig(mapLookup(test.env), rejectRead, "release-2026.08.12")
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if cfg.LogLevel != test.level || cfg.TracesExporter != test.trace || cfg.MetricsExporter != test.met {
				t.Fatalf("config = %#v", cfg)
			}
			if cfg.ServiceVersion != "release-2026.08.12" {
				t.Fatalf("service version = %q", cfg.ServiceVersion)
			}
		})
	}
}

func TestLoadConfigAcceptsMaximumServiceName(t *testing.T) {
	serviceName := strings.Repeat("s", 128)
	cfg, err := LoadConfig(mapLookup(map[string]string{"OTEL_SERVICE_NAME": serviceName}), rejectRead, "v1.2.3")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ServiceName != serviceName || utf8.RuneCountInString(cfg.ServiceName) != 128 {
		t.Fatalf("service name = %q", cfg.ServiceName)
	}
}

func TestLoadConfigRejectsClosedValueViolations(t *testing.T) {
	tests := []struct {
		name  string
		env   map[string]string
		field string
	}{
		{"log level", map[string]string{"LOG_LEVEL": "verbose"}, "LOG_LEVEL"},
		{"empty log level", map[string]string{"LOG_LEVEL": ""}, "LOG_LEVEL"},
		{"trace mode", map[string]string{"OTEL_TRACES_EXPORTER": "console"}, "OTEL_TRACES_EXPORTER"},
		{"empty trace mode", map[string]string{"OTEL_TRACES_EXPORTER": ""}, "OTEL_TRACES_EXPORTER"},
		{"metric mode", map[string]string{"OTEL_METRICS_EXPORTER": "prometheus"}, "OTEL_METRICS_EXPORTER"},
		{"empty metric mode", map[string]string{"OTEL_METRICS_EXPORTER": ""}, "OTEL_METRICS_EXPORTER"},
		{"empty service", map[string]string{"OTEL_SERVICE_NAME": ""}, "OTEL_SERVICE_NAME"},
		{"overlong service", map[string]string{"OTEL_SERVICE_NAME": strings.Repeat("s", 129)}, "OTEL_SERVICE_NAME"},
		{"control service", map[string]string{"OTEL_SERVICE_NAME": "safe\nunsafe"}, "OTEL_SERVICE_NAME"},
		{"resource injection", map[string]string{"OTEL_RESOURCE_ATTRIBUTES": "tenant.secret=value"}, "OTEL_RESOURCE_ATTRIBUTES"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadConfig(mapLookup(test.env), rejectRead, "v1.2.3")
			assertSafeFieldError(t, err, test.field, values(test.env)...)
		})
	}
}

func TestLoadConfigRejectsUnsupportedExperimentalOTelPolicy(t *testing.T) {
	for _, field := range unsupportedOTelExperimentalFields {
		t.Run(field, func(t *testing.T) {
			const hostile = "hostile-experimental-policy"
			_, err := LoadConfig(mapLookup(map[string]string{field: hostile}), rejectRead, "devel")
			assertSafeFieldError(t, err, field, hostile)
		})
	}
}

func TestLoadConfigResolvesEnabledOTLPHTTP(t *testing.T) {
	env := map[string]string{
		"OTEL_TRACES_EXPORTER":                   "otlp",
		"OTEL_METRICS_EXPORTER":                  "otlp",
		"OTEL_EXPORTER_OTLP_ENDPOINT":            "https://collector.example/base/",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT":     "https://trace.example/custom",
		"OTEL_EXPORTER_OTLP_HEADERS":             "authorization=Bearer%20redacted,x-safe=value",
		"OTEL_EXPORTER_OTLP_METRICS_HEADERS":     "x-safe=metric",
		"OTEL_EXPORTER_OTLP_TIMEOUT":             "12000",
		"OTEL_EXPORTER_OTLP_TRACES_TIMEOUT":      "7000",
		"OTEL_EXPORTER_OTLP_COMPRESSION":         "gzip",
		"OTEL_EXPORTER_OTLP_METRICS_COMPRESSION": "none",
		"OTEL_EXPORTER_OTLP_PROTOCOL":            "http/protobuf",
	}
	cfg, err := LoadConfig(mapLookup(env), rejectRead, "v1.2.3")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.traces.endpoint.String(); got != "https://trace.example/custom" {
		t.Fatalf("trace endpoint = %q", got)
	}
	if got := cfg.metrics.endpoint.String(); got != "https://collector.example/base/v1/metrics" {
		t.Fatalf("metric endpoint = %q", got)
	}
	if cfg.traces.timeout != 7*time.Second || cfg.metrics.timeout != 12*time.Second {
		t.Fatalf("timeouts = %v, %v", cfg.traces.timeout, cfg.metrics.timeout)
	}
	if got := cfg.traces.headers["Authorization"]; got != "Bearer redacted" {
		t.Fatalf("trace authorization = %q", got)
	}
	if got := cfg.metrics.headers["X-Safe"]; got != "metric" {
		t.Fatalf("metric header = %q", got)
	}
	if cfg.traces.compression != "gzip" || cfg.metrics.compression != "none" {
		t.Fatalf("compression = %q, %q", cfg.traces.compression, cfg.metrics.compression)
	}
}

func TestLoadConfigPrefersSignalSpecificOTLPSettings(t *testing.T) {
	certificate, key := testCertificate(t)
	files := map[string][]byte{
		"/trace-ca.pem":     certificate,
		"/trace-client.pem": certificate,
		"/trace-client.key": key,
	}
	cfg, err := LoadConfig(mapLookup(map[string]string{
		"OTEL_TRACES_EXPORTER":                         "otlp",
		"OTEL_EXPORTER_OTLP_PROTOCOL":                  "grpc",
		"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL":           "http/protobuf",
		"OTEL_EXPORTER_OTLP_ENDPOINT":                  "http://collector.example",
		"OTEL_EXPORTER_OTLP_INSECURE":                  "false",
		"OTEL_EXPORTER_OTLP_TRACES_INSECURE":           "true",
		"OTEL_EXPORTER_OTLP_CERTIFICATE":               "/global-ca.pem",
		"OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE":        "/trace-ca.pem",
		"OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE":        "/global-client.pem",
		"OTEL_EXPORTER_OTLP_TRACES_CLIENT_CERTIFICATE": "/trace-client.pem",
		"OTEL_EXPORTER_OTLP_CLIENT_KEY":                "/global-client.key",
		"OTEL_EXPORTER_OTLP_TRACES_CLIENT_KEY":         "/trace-client.key",
	}), fileLookup(files), "v1.2.3")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.traces.tlsConfig == nil || cfg.traces.tlsConfig.RootCAs == nil || len(cfg.traces.tlsConfig.Certificates) != 1 {
		t.Fatalf("TLS config = %#v", cfg.traces.tlsConfig)
	}
}

func TestLoadConfigUsesOTLPHTTPDefaultsAndSecureSchemes(t *testing.T) {
	for _, test := range []struct {
		name        string
		env         map[string]string
		traceURL    string
		metricURL   string
		traceSecure bool
	}{
		{
			name:      "defaults",
			env:       map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_METRICS_EXPORTER": "otlp"},
			traceURL:  "https://localhost:4318/v1/traces",
			metricURL: "https://localhost:4318/v1/metrics",
		},
		{
			name: "http with insecure true",
			env: map[string]string{
				"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector.example/base",
				"OTEL_EXPORTER_OTLP_INSECURE": "true",
			},
			traceURL: "http://collector.example/base/v1/traces",
		},
		{
			name: "https with insecure false",
			env: map[string]string{
				"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "https://collector.example/custom",
				"OTEL_EXPORTER_OTLP_TRACES_INSECURE": "false",
			},
			traceURL:    "https://collector.example/custom",
			traceSecure: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := LoadConfig(mapLookup(test.env), rejectRead, "v1.2.3")
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if test.traceURL != "" && cfg.traces.endpoint.String() != test.traceURL {
				t.Fatalf("trace endpoint = %q", cfg.traces.endpoint)
			}
			if test.metricURL != "" && cfg.metrics.endpoint.String() != test.metricURL {
				t.Fatalf("metric endpoint = %q", cfg.metrics.endpoint)
			}
			if test.traceSecure && cfg.traces.tlsConfig == nil {
				t.Fatal("trace TLS config is nil")
			}
		})
	}
}

func TestLoadConfigRejectsMalformedOTLPHTTP(t *testing.T) {
	secret := "secret-token"
	tests := []struct {
		name  string
		env   map[string]string
		field string
		leaks []string
	}{
		{"grpc protocol", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_PROTOCOL": "grpc"}, "OTEL_EXPORTER_OTLP_PROTOCOL", []string{"grpc"}},
		{"unknown protocol", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_PROTOCOL": "unknown"}, "OTEL_EXPORTER_OTLP_PROTOCOL", []string{"unknown"}},
		{"relative endpoint", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_ENDPOINT": "/collector"}, "OTEL_EXPORTER_OTLP_ENDPOINT", []string{"/collector"}},
		{"hostless endpoint", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_ENDPOINT": "https://:4318"}, "OTEL_EXPORTER_OTLP_ENDPOINT", []string{"https://:4318"}},
		{"credential endpoint", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_ENDPOINT": "https://user:pass@collector.example"}, "OTEL_EXPORTER_OTLP_ENDPOINT", []string{"user:pass"}},
		{"query endpoint", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_ENDPOINT": "https://collector.example?" + secret}, "OTEL_EXPORTER_OTLP_ENDPOINT", []string{secret}},
		{"fragment endpoint", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_ENDPOINT": "https://collector.example/#" + secret}, "OTEL_EXPORTER_OTLP_ENDPOINT", []string{secret}},
		{"invalid insecure", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_INSECURE": "sometimes"}, "OTEL_EXPORTER_OTLP_INSECURE", nil},
		{"http secure conflict", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector.example", "OTEL_EXPORTER_OTLP_INSECURE": "false"}, "OTEL_EXPORTER_OTLP_INSECURE", nil},
		{"https insecure conflict", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_ENDPOINT": "https://collector.example", "OTEL_EXPORTER_OTLP_INSECURE": "true"}, "OTEL_EXPORTER_OTLP_INSECURE", nil},
		{"zero timeout", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_TIMEOUT": "0"}, "OTEL_EXPORTER_OTLP_TIMEOUT", nil},
		{"invalid timeout", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_TIMEOUT": "forever"}, "OTEL_EXPORTER_OTLP_TIMEOUT", []string{"forever"}},
		{"invalid compression", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_COMPRESSION": "brotli"}, "OTEL_EXPORTER_OTLP_COMPRESSION", []string{"brotli"}},
		{"malformed header", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_HEADERS": "not-a-header"}, "OTEL_EXPORTER_OTLP_HEADERS", []string{"not-a-header"}},
		{"duplicate header", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_HEADERS": "x-safe=one,X-SAFE=two"}, "OTEL_EXPORTER_OTLP_HEADERS", []string{"x-safe=one", "X-SAFE=two"}},
		{"control header", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_HEADERS": "authorization=" + secret + "%0Aunsafe"}, "OTEL_EXPORTER_OTLP_HEADERS", []string{secret}},
		{"null header", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_HEADERS": "x-safe=%00"}, "OTEL_EXPORTER_OTLP_HEADERS", []string{"x-safe=%00"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadConfig(mapLookup(test.env), rejectRead, "v1.2.3")
			assertSafeFieldError(t, err, test.field, test.leaks...)
		})
	}
}

func TestLoadConfigLoadsTLSMaterial(t *testing.T) {
	certificate, key := testCertificate(t)
	files := map[string][]byte{
		"/ca.pem":     certificate,
		"/client.pem": certificate,
		"/client.key": key,
	}
	cfg, err := LoadConfig(mapLookup(map[string]string{
		"OTEL_TRACES_EXPORTER":                  "otlp",
		"OTEL_EXPORTER_OTLP_CERTIFICATE":        "/ca.pem",
		"OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE": "/client.pem",
		"OTEL_EXPORTER_OTLP_CLIENT_KEY":         "/client.key",
	}), fileLookup(files), "v1.2.3")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.traces.tlsConfig == nil || cfg.traces.tlsConfig.RootCAs == nil || len(cfg.traces.tlsConfig.Certificates) != 1 {
		t.Fatalf("TLS config = %#v", cfg.traces.tlsConfig)
	}
}

func TestLoadConfigRejectsMalformedTLSMaterial(t *testing.T) {
	path := "/private/path.pem"
	tests := []struct {
		name  string
		env   map[string]string
		files map[string][]byte
		field string
	}{
		{"unreadable CA", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_CERTIFICATE": path}, nil, "OTEL_EXPORTER_OTLP_CERTIFICATE"},
		{"unreadable signal CA", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE": path}, nil, "OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE"},
		{"invalid CA", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_CERTIFICATE": path}, map[string][]byte{path: []byte("not pem")}, "OTEL_EXPORTER_OTLP_CERTIFICATE"},
		{"unpaired client certificate", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE": path}, nil, "OTEL_EXPORTER_OTLP_CLIENT_KEY"},
		{"unpaired client key", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_CLIENT_KEY": path}, nil, "OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE"},
		{"unreadable client pair", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE": path, "OTEL_EXPORTER_OTLP_CLIENT_KEY": path + ".key"}, nil, "OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE"},
		{"unreadable signal client pair", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_TRACES_CLIENT_CERTIFICATE": path, "OTEL_EXPORTER_OTLP_TRACES_CLIENT_KEY": path + ".key"}, nil, "OTEL_EXPORTER_OTLP_TRACES_CLIENT_CERTIFICATE"},
		{"invalid client pair", map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE": path, "OTEL_EXPORTER_OTLP_CLIENT_KEY": path + ".key"}, map[string][]byte{path: []byte("not pem"), path + ".key": []byte("not key")}, "OTEL_EXPORTER_OTLP_CLIENT_CERTIFICATE"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadConfig(mapLookup(test.env), fileLookup(test.files), "v1.2.3")
			assertSafeFieldError(t, err, test.field, append(values(test.env), path)...)
		})
	}
}

func assertSafeFieldError(t *testing.T, err error, field string, forbidden ...string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), field) {
		t.Fatalf("error = %v, want field %q", err, field)
	}
	for _, value := range forbidden {
		if value != "" && strings.Contains(err.Error(), value) {
			t.Fatalf("error leaked configured value %q: %v", value, err)
		}
	}
}

func values(env map[string]string) []string {
	values := make([]string, 0, len(env))
	for _, value := range env {
		values = append(values, value)
	}
	return values
}

func mapLookup(env map[string]string) LookupEnv {
	return func(name string) (string, bool) {
		value, ok := env[name]
		return value, ok
	}
}

func emptyEnv(string) (string, bool) { return "", false }

func rejectRead(string) ([]byte, error) { return nil, errors.New("unexpected file read") }

func fileLookup(files map[string][]byte) ReadFile {
	return func(name string) ([]byte, error) {
		value, ok := files[name]
		if !ok {
			return nil, errors.New("not found")
		}
		return append([]byte(nil), value...), nil
	}
}

func testCertificate(t *testing.T) ([]byte, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Unix(0, 0),
		NotAfter:     time.Unix(1, 0),
	}, &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Unix(0, 0),
		NotAfter:     time.Unix(1, 0),
	}, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	keyDER := x509.MarshalPKCS1PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyDER})
}
