// Package observability owns Maiden Lane's operational telemetry boundary.
//
// This package keeps environment and filesystem access at process composition.
// Its validated Config value is deliberately separate from Maiden Lane's
// semantic inputs, identities, and execution behavior.
package observability

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	defaultServiceName  = "maiden-lane"
	defaultOTLPEndpoint = "https://localhost:4318"
	defaultOTLPTimeout  = 10 * time.Second
)

// LookupEnv has the same signature as os.LookupEnv. Dependency injection keeps
// ambient process configuration at this operational boundary and testable.
type LookupEnv func(string) (string, bool)

// ReadFile has the same signature as os.ReadFile. It is used only for selected
// OTLP TLS material, never from Maiden Lane's semantic packages.
type ReadFile func(string) ([]byte, error)

// ExporterMode is the closed set of supported telemetry exporter modes.
type ExporterMode string

const (
	// ExporterNone disables the corresponding signal.
	ExporterNone ExporterMode = "none"
	// ExporterOTLP enables the supported OTLP-over-HTTP/protobuf exporter.
	ExporterOTLP ExporterMode = "otlp"
)

// Config is validated operational observability configuration. Its unexported
// fields prevent callers from constructing a configuration that authorizes
// exporter initialization without passing this package's closed validation.
type Config struct {
	LogLevel        slog.Level
	TracesExporter  ExporterMode
	MetricsExporter ExporterMode
	ServiceName     string
	ServiceVersion  string

	validated bool
	traces    otlpHTTPConfig
	metrics   otlpHTTPConfig
}

type otlpHTTPConfig struct {
	endpoint    *url.URL
	headers     map[string]string
	timeout     time.Duration
	compression string
	tlsConfig   *tls.Config
}

// LoadConfig parses the closed operational observability contract. It never
// returns configured values in validation errors because endpoints, header
// values, and certificate paths may contain sensitive information.
func LoadConfig(lookup LookupEnv, readFile ReadFile, serviceVersion string) (Config, error) {
	logLevel, err := loadLogLevel(lookup)
	if err != nil {
		return Config{}, err
	}
	tracesExporter, err := loadExporter(lookup, "OTEL_TRACES_EXPORTER")
	if err != nil {
		return Config{}, err
	}
	metricsExporter, err := loadExporter(lookup, "OTEL_METRICS_EXPORTER")
	if err != nil {
		return Config{}, err
	}
	serviceName, err := loadServiceName(lookup)
	if err != nil {
		return Config{}, err
	}
	if raw, present := lookup("OTEL_RESOURCE_ATTRIBUTES"); present && raw != "" {
		return Config{}, invalidField("OTEL_RESOURCE_ATTRIBUTES", "an unset value")
	}

	cfg := Config{
		LogLevel:        logLevel,
		TracesExporter:  tracesExporter,
		MetricsExporter: metricsExporter,
		ServiceName:     serviceName,
	}
	if serviceVersion != "" && serviceVersion != "devel" {
		cfg.ServiceVersion = serviceVersion
	}
	if tracesExporter == ExporterOTLP {
		cfg.traces, err = resolveOTLPHTTP(lookup, readFile, "TRACES", "/v1/traces")
		if err != nil {
			return Config{}, err
		}
	}
	if metricsExporter == ExporterOTLP {
		cfg.metrics, err = resolveOTLPHTTP(lookup, readFile, "METRICS", "/v1/metrics")
		if err != nil {
			return Config{}, err
		}
	}

	cfg.validated = true
	return cfg, nil
}

func loadLogLevel(lookup LookupEnv) (slog.Level, error) {
	raw, present := lookup("LOG_LEVEL")
	if !present {
		return slog.LevelInfo, nil
	}
	levels := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	}
	level, ok := levels[raw]
	if !ok {
		return 0, invalidField("LOG_LEVEL", "debug, info, warn, or error")
	}
	return level, nil
}

func loadExporter(lookup LookupEnv, field string) (ExporterMode, error) {
	raw, present := lookup(field)
	if !present {
		return ExporterNone, nil
	}
	mode := ExporterMode(raw)
	if mode != ExporterNone && mode != ExporterOTLP {
		return "", invalidField(field, "none or otlp")
	}
	return mode, nil
}

func loadServiceName(lookup LookupEnv) (string, error) {
	serviceName, present := lookup("OTEL_SERVICE_NAME")
	if !present {
		return defaultServiceName, nil
	}
	if len(serviceName) == 0 || len(serviceName) > 128 || !utf8.ValidString(serviceName) {
		return "", invalidField("OTEL_SERVICE_NAME", "1-128 UTF-8 bytes without control characters")
	}
	for _, r := range serviceName {
		if unicode.IsControl(r) {
			return "", invalidField("OTEL_SERVICE_NAME", "1-128 UTF-8 bytes without control characters")
		}
	}
	return serviceName, nil
}

// resolveOTLPHTTP resolves exactly one signal's selected OTLP/HTTP settings.
// Signal-specific settings take precedence over global settings so both signal
// configurations are deterministic values rather than exporter-side policy.
func resolveOTLPHTTP(lookup LookupEnv, readFile ReadFile, signal, signalPath string) (otlpHTTPConfig, error) {
	endpointRaw, endpointField, endpointPresent := firstSignalEnv(lookup, signal, "ENDPOINT")
	if !endpointPresent {
		endpointRaw, endpointField, endpointPresent = defaultOTLPEndpoint, "OTEL_EXPORTER_OTLP_ENDPOINT", true
	}
	endpoint, err := parseEndpoint(endpointField, endpointRaw, signalPath, endpointField == "OTEL_EXPORTER_OTLP_ENDPOINT")
	if err != nil {
		return otlpHTTPConfig{}, err
	}

	if protocol, field, present := firstSignalEnv(lookup, signal, "PROTOCOL"); present && protocol != "http/protobuf" {
		return otlpHTTPConfig{}, invalidField(field, "http/protobuf")
	}

	headers := map[string]string{}
	if raw, field, present := firstSignalEnv(lookup, signal, "HEADERS"); present {
		headers, err = parseHeaders(field, raw)
		if err != nil {
			return otlpHTTPConfig{}, err
		}
	}

	timeout := defaultOTLPTimeout
	if raw, field, present := firstSignalEnv(lookup, signal, "TIMEOUT"); present {
		timeout, err = parseTimeout(field, raw)
		if err != nil {
			return otlpHTTPConfig{}, err
		}
	}

	compression := "none"
	if raw, field, present := firstSignalEnv(lookup, signal, "COMPRESSION"); present {
		if raw != "none" && raw != "gzip" {
			return otlpHTTPConfig{}, invalidField(field, "none or gzip")
		}
		compression = raw
	}

	if err := validateInsecure(lookup, signal, endpoint); err != nil {
		return otlpHTTPConfig{}, err
	}
	tlsConfig, err := buildTLSConfig(lookup, readFile, signal)
	if err != nil {
		return otlpHTTPConfig{}, err
	}
	return otlpHTTPConfig{
		endpoint:    endpoint,
		headers:     cloneHeaders(headers),
		timeout:     timeout,
		compression: compression,
		tlsConfig:   cloneTLSConfig(tlsConfig),
	}, nil
}

// firstSignalEnv returns a signal-specific setting when present, otherwise the
// corresponding global setting. A present empty string is intentionally left
// to the setting parser so it fails closed with that field's safe error.
func firstSignalEnv(lookup LookupEnv, signal, suffix string) (value, field string, present bool) {
	signalField := "OTEL_EXPORTER_OTLP_" + signal + "_" + suffix
	if value, present = lookup(signalField); present {
		return value, signalField, true
	}
	field = "OTEL_EXPORTER_OTLP_" + suffix
	value, present = lookup(field)
	return value, field, present
}

func parseEndpoint(field, raw, signalPath string, global bool) (*url.URL, error) {
	if raw == "" {
		return nil, invalidField(field, "an absolute http or https URL")
	}
	endpoint, err := url.Parse(raw)
	if err != nil || !endpoint.IsAbs() || endpoint.Host == "" || endpoint.User != nil ||
		(endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" {
		return nil, invalidField(field, "an absolute http or https URL without credentials, query, or fragment")
	}
	if global {
		endpoint.Path = strings.TrimRight(endpoint.Path, "/") + signalPath
		if endpoint.Path == "" {
			endpoint.Path = signalPath
		}
		endpoint.RawPath = ""
	}
	return endpoint, nil
}

func parseHeaders(field, raw string) (map[string]string, error) {
	if raw == "" {
		return nil, invalidField(field, "comma-separated header=value pairs")
	}
	headers := make(map[string]string)
	for _, entry := range strings.Split(raw, ",") {
		name, value, found := strings.Cut(entry, "=")
		if !found || !validHeaderName(name) {
			return nil, invalidField(field, "comma-separated header=value pairs")
		}
		decoded, err := url.PathUnescape(value)
		if err != nil || strings.ContainsAny(decoded, "\r\n") {
			return nil, invalidField(field, "percent-encoded header values without CR or LF")
		}
		canonicalName := http.CanonicalHeaderKey(name)
		if _, exists := headers[canonicalName]; exists {
			return nil, invalidField(field, "unique case-insensitive header names")
		}
		headers[canonicalName] = decoded
	}
	return headers, nil
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c))) {
			return false
		}
	}
	return true
}

func parseTimeout(field, raw string) (time.Duration, error) {
	milliseconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || milliseconds <= 0 || milliseconds > int64((time.Duration(1<<63-1))/time.Millisecond) {
		return 0, invalidField(field, "a positive integer milliseconds value")
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func validateInsecure(lookup LookupEnv, signal string, endpoint *url.URL) error {
	raw, field, present := firstSignalEnv(lookup, signal, "INSECURE")
	if !present {
		return nil
	}
	insecure, err := strconv.ParseBool(raw)
	if err != nil || insecure != (endpoint.Scheme == "http") {
		return invalidField(field, "true for http endpoints or false for https endpoints")
	}
	return nil
}

// buildTLSConfig reads selected TLS material through the injected file boundary.
// Failures deliberately classify only the field; certificate paths and parsing
// details may be sensitive deployment information.
func buildTLSConfig(lookup LookupEnv, readFile ReadFile, signal string) (*tls.Config, error) {
	config := &tls.Config{}
	if path, field, present := firstSignalEnv(lookup, signal, "CERTIFICATE"); present {
		if path == "" {
			return nil, invalidField(field, "a readable PEM certificate")
		}
		pemBytes, err := readFile(path)
		if err != nil {
			return nil, invalidField(field, "a readable PEM certificate")
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBytes) {
			return nil, invalidField(field, "a valid PEM certificate")
		}
		config.RootCAs = pool
	}

	certificatePath, certificateField, certificatePresent := firstSignalEnv(lookup, signal, "CLIENT_CERTIFICATE")
	keyPath, keyField, keyPresent := firstSignalEnv(lookup, signal, "CLIENT_KEY")
	if certificatePresent != keyPresent {
		if certificatePresent {
			return nil, invalidField(keyField, "a paired client certificate and key")
		}
		return nil, invalidField(certificateField, "a paired client certificate and key")
	}
	if !certificatePresent {
		return config, nil
	}
	if certificatePath == "" {
		return nil, invalidField(certificateField, "a readable client certificate")
	}
	if keyPath == "" {
		return nil, invalidField(keyField, "a readable client key")
	}
	certificatePEM, err := readFile(certificatePath)
	if err != nil {
		return nil, invalidField(certificateField, "a readable client certificate")
	}
	keyPEM, err := readFile(keyPath)
	if err != nil {
		return nil, invalidField(keyField, "a readable client key")
	}
	certificate, err := tls.X509KeyPair(certificatePEM, keyPEM)
	if err != nil {
		return nil, invalidField(certificateField, "a valid paired client certificate and key")
	}
	config.Certificates = []tls.Certificate{certificate}
	return config, nil
}

func cloneHeaders(headers map[string]string) map[string]string {
	copy := make(map[string]string, len(headers))
	for name, value := range headers {
		copy[name] = value
	}
	return copy
}

func cloneTLSConfig(config *tls.Config) *tls.Config {
	if config == nil {
		return nil
	}
	return config.Clone()
}

func invalidField(name, allowed string) error {
	return fmt.Errorf("invalid %s: expected %s", name, allowed)
}
