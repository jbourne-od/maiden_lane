package observability

import (
	"io"
	"net/http"
	"time"

	"github.com/felixge/httpsnoop"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/optimaldynamics/maiden-lane/internal/observability"

const (
	httpClientError      = "http.client_error"
	httpServerError      = "http.server_error"
	httpRequestCanceled  = "request_canceled"
	httpHandlerPanic     = "handler_panic"
	httpInvalidStatus    = "invalid_http_status"
	httpNormalizedOther  = "OTHER"
	httpDurationName     = "http.server.request.duration"
	httpRequestSizeName  = "http.server.request.body.size"
	httpResponseSizeName = "http.server.response.body.size"
)

// InstrumentHTTPRoute instruments a handler whose method and route pattern
// came from application registration. Callers must wrap only matched,
// non-health handlers: the registered pattern is trusted metadata, while a
// request path, parameter, or arbitrary method is attacker-controlled input.
func (r *Runtime) InstrumentHTTPRoute(method, pattern string, next http.Handler) http.Handler {
	trustedMethod := normalizeMethod(method)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		start := time.Now()

		// Baggage is removed both before and after extraction. The first removal
		// handles baggage already present in a Go context; the second keeps this
		// privacy boundary intact if the configured propagator grows later.
		ctx := baggage.ContextWithoutBaggage(request.Context())
		ctx = r.propagator.Extract(ctx, propagation.HeaderCarrier(request.Header))
		ctx = baggage.ContextWithoutBaggage(ctx)
		ctx, span := r.tracerProvider.Tracer(instrumentationName).Start(
			ctx,
			trustedMethod+" "+pattern,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(baseHTTPAttributes(trustedMethod, pattern, request.Proto)...),
		)

		body := &countingReadCloser{ReadCloser: request.Body}
		request = request.Clone(ctx)
		request.Body = body
		response := newResponseObservation(writer)
		var captured httpsnoop.Metrics
		completed := false

		// This one defer is the terminal ownership boundary. In Go, ordinary
		// statements after ServeHTTP do not run during panic unwinding, so the
		// wrapper-owned clock, measurement recording, span end, and safe re-panic
		// all belong here.
		defer func() {
			// A legacy panic(nil) can make recover return nil. Whether ServeHTTP
			// completed normally is therefore the only reliable panic signal; the
			// recovered value is deliberately discarded to keep it out of telemetry.
			_ = recover()
			panicked := !completed
			status := response.status
			if !response.committed {
				status = http.StatusOK
				if panicked {
					status = 0
				}
			}
			statusCode, errorType, includeStatus := classifyHTTPResult(panicked, request.Context().Err(), status)

			spanAttributes := make([]attribute.KeyValue, 0, 2)
			metricAttributes := []attribute.KeyValue{
				semconv.HTTPRequestMethodKey.String(trustedMethod),
				semconv.HTTPRouteKey.String(pattern),
			}
			if includeStatus {
				statusAttribute := semconv.HTTPResponseStatusCodeKey.Int(status)
				spanAttributes = append(spanAttributes, statusAttribute)
				metricAttributes = append(metricAttributes, statusAttribute)
			}
			if errorType == "" {
				span.SetStatus(statusCode, "")
			} else {
				spanAttributes = append(spanAttributes, semconv.ErrorTypeKey.String(errorType))
				span.SetStatus(statusCode, errorType)
			}
			span.SetAttributes(spanAttributes...)

			recordOptions := metric.WithAttributes(metricAttributes...)
			r.httpDuration.Record(ctx, time.Since(start).Seconds(), recordOptions)
			r.httpRequestSize.Record(ctx, body.observed, recordOptions)
			r.httpResponseSize.Record(ctx, captured.Written, recordOptions)
			span.End()

			if panicked {
				// net/http recognizes ErrAbortHandler and suppresses the original
				// panic value and stack. Never allow either into its error logger.
				panic(http.ErrAbortHandler)
			}
		}()

		captured.CaptureMetrics(response.writer, func(observed http.ResponseWriter) {
			next.ServeHTTP(observed, request)
			completed = true
		})
	})
}

// classifyHTTPResult centralizes the closed status vocabulary. includeStatus
// is false for values that are not valid protocol status dimensions.
func classifyHTTPResult(panicked bool, requestErr error, status int) (statusCode codes.Code, errorType string, includeStatus bool) {
	validStatus := status >= 100 && status <= 599
	switch {
	case panicked:
		return codes.Error, httpHandlerPanic, validStatus
	case requestErr != nil:
		return codes.Error, httpRequestCanceled, validStatus
	case !validStatus:
		return codes.Error, httpInvalidStatus, false
	case status <= 399:
		return codes.Ok, "", true
	case status <= 499:
		return codes.Error, httpClientError, true
	default:
		return codes.Error, httpServerError, true
	}
}

func normalizeMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodDelete, http.MethodConnect, http.MethodOptions,
		http.MethodTrace, http.MethodPatch:
		return method
	default:
		return httpNormalizedOther
	}
}

func baseHTTPAttributes(method, pattern, protocol string) []attribute.KeyValue {
	attributes := []attribute.KeyValue{
		semconv.HTTPRequestMethodKey.String(method),
		semconv.HTTPRouteKey.String(pattern),
	}
	if name, version, ok := boundedProtocol(protocol); ok {
		attributes = append(attributes,
			semconv.NetworkProtocolNameKey.String(name),
			semconv.NetworkProtocolVersionKey.String(version),
		)
	}
	return attributes
}

func boundedProtocol(protocol string) (name, version string, ok bool) {
	switch protocol {
	case "HTTP/1.0":
		return "http", "1.0", true
	case "HTTP/1.1":
		return "http", "1.1", true
	case "HTTP/2.0":
		return "http", "2", true
	case "HTTP/3.0":
		return "http", "3", true
	default:
		return "", "", false
	}
}

func (r *Runtime) registerHTTPInstruments() error {
	meter := r.meterProvider.Meter(instrumentationName)
	var err error
	r.httpDuration, err = meter.Float64Histogram(
		httpDurationName,
		metric.WithUnit("s"),
		metric.WithDescription("Duration of matched non-health HTTP server requests"),
	)
	if err != nil {
		return err
	}
	r.httpRequestSize, err = meter.Int64Histogram(
		httpRequestSizeName,
		metric.WithUnit("By"),
		metric.WithDescription("Request body bytes observed by the server"),
	)
	if err != nil {
		return err
	}
	r.httpResponseSize, err = meter.Int64Histogram(
		httpResponseSizeName,
		metric.WithUnit("By"),
		metric.WithDescription("Response body bytes written by the server"),
	)
	return err
}

func httpMetricViews() []sdkmetric.View {
	// The wrapper already supplies only these keys. Exact-name views repeat the
	// boundary inside the SDK so a future instrument call cannot accidentally
	// add request-derived dimensions. Views do not filter exemplar attributes;
	// newMetricProvider separately disables exemplars for that reason.
	filter := attribute.NewAllowKeysFilter(
		semconv.HTTPRequestMethodKey,
		semconv.HTTPRouteKey,
		semconv.HTTPResponseStatusCodeKey,
	)
	// An unset aggregation inherits the SDK's default boundaries, which start
	// [0, 5, 10, 25, ...]. Those suit milliseconds; these instruments record
	// seconds and bytes, so the defaults put every observation of interest in
	// one bucket and make percentiles confidently wrong. See
	// TestDurationHistogramsCanDistinguishSubSecondLatency.
	boundaries := map[string][]float64{
		httpDurationName:     httpDurationBoundaries,
		httpRequestSizeName:  httpBodySizeBoundaries,
		httpResponseSizeName: httpBodySizeBoundaries,
	}

	views := make([]sdkmetric.View, 0, 3)
	for _, name := range []string{httpDurationName, httpRequestSizeName, httpResponseSizeName} {
		views = append(views, sdkmetric.NewView(
			sdkmetric.Instrument{Name: name},
			sdkmetric.Stream{
				AttributeFilter: filter,
				Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
					Boundaries: boundaries[name],
				},
			},
		))
	}
	return views
}

// httpDurationBoundaries are the boundaries the HTTP semantic conventions
// recommend for http.server.request.duration, used verbatim.
//
// They are coarser at the low end than this server's in-memory responses
// warrant, and that is the right trade. This instrument's name and attributes
// are semantic-convention surface, so dashboards, alert libraries, and managed
// backends all expect this distribution. Hand-tuning it for a local process
// would make the series diverge from every tool built to read it, for the sake
// of resolution that stops mattering the moment a network and a database are in
// the path.
var httpDurationBoundaries = []float64{
	0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10,
}

// httpBodySizeBoundaries span the payloads this API actually carries. The
// conventions recommend no distribution for body size, and the default one tops
// out at 10000 bytes, which a plan declaration for the ratified fixture already
// exceeds at 7225 bytes.
var httpBodySizeBoundaries = []float64{
	64, 256, 1024, 4096, 16384, 65536, 262144, 1 << 20, 4 << 20,
}

type countingReadCloser struct {
	io.ReadCloser
	observed int64
}

func (body *countingReadCloser) Read(buffer []byte) (int, error) {
	if body.ReadCloser == nil {
		return 0, io.EOF
	}
	count, err := body.ReadCloser.Read(buffer)
	body.observed += int64(count)
	return count, err
}

func (body *countingReadCloser) Close() error {
	if body.ReadCloser == nil {
		return nil
	}
	return body.ReadCloser.Close()
}

type responseObservation struct {
	writer    http.ResponseWriter
	committed bool
	status    int
}

func newResponseObservation(writer http.ResponseWriter) *responseObservation {
	observation := &responseObservation{}
	// httpsnoop preserves the exact optional interface set (Flusher, Hijacker,
	// Pusher, ReaderFrom, and newer controller interfaces) of the writer it
	// wraps. The inner wrapper remembers whether the handler committed a real
	// terminal status; the outer CaptureMetrics wrapper counts response bytes.
	observation.writer = httpsnoop.Wrap(writer, httpsnoop.Hooks{
		WriteHeader: func(next httpsnoop.WriteHeaderFunc) httpsnoop.WriteHeaderFunc {
			return func(status int) {
				next(status)
				if status < 100 || status > 199 {
					observation.commit(status)
				}
			}
		},
		Write: func(next httpsnoop.WriteFunc) httpsnoop.WriteFunc {
			return func(body []byte) (int, error) {
				count, err := next(body)
				observation.commit(http.StatusOK)
				return count, err
			}
		},
		Flush: func(next httpsnoop.FlushFunc) httpsnoop.FlushFunc {
			return func() {
				next()
				observation.commit(http.StatusOK)
			}
		},
		FlushError: func(next httpsnoop.FlushErrorFunc) httpsnoop.FlushErrorFunc {
			return func() error {
				err := next()
				observation.commit(http.StatusOK)
				return err
			}
		},
		WriteString: func(next httpsnoop.WriteStringFunc) httpsnoop.WriteStringFunc {
			return func(body string) (int, error) {
				count, err := next(body)
				observation.commit(http.StatusOK)
				return count, err
			}
		},
		ReadFrom: func(next httpsnoop.ReadFromFunc) httpsnoop.ReadFromFunc {
			return func(reader io.Reader) (int64, error) {
				count, err := next(reader)
				observation.commit(http.StatusOK)
				return count, err
			}
		},
	})
	return observation
}

func (observation *responseObservation) commit(status int) {
	if observation.committed {
		return
	}
	observation.committed = true
	observation.status = status
}
