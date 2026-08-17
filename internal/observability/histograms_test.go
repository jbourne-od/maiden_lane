package observability

import (
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// Production break caught by rendering telemetry for the first time. Both
// seconds-valued histograms were left on the SDK's default explicit bucket
// boundaries, which are [0, 5, 10, 25, ... 10000] and are shaped for
// milliseconds. Recording seconds against them put every real observation in
// one bucket: a measured run had a mean phase duration of 104 microseconds,
// all 17 observations fell under le=5, and histogram_quantile(0.95, ...)
// therefore reported 4.75 seconds.
//
// A useless percentile would be tolerable. A percentile that is wrong by four
// orders of magnitude while looking entirely plausible is not, because an
// operator has no signal that the number is meaningless.
//
// These assertions are about discrimination rather than an exact boundary list.
// Pinning the exact numbers would fail whenever someone legitimately retunes
// them, and would not have caught the actual defect any earlier.
func TestDurationHistogramsCanDistinguishSubSecondLatency(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		views   []sdkmetric.View
		finest  float64
		atLeast int
	}{
		{
			// This instrument measures in-process spine phases, whose observed
			// scale is tens to hundreds of microseconds.
			name:    semanticPhaseDurationName,
			views:   semanticMetricViews(),
			finest:  0.001,
			atLeast: 6,
		},
		{
			// A server request crosses a network and, in a deployment, a
			// database. Millisecond resolution is the right floor here.
			name:    httpDurationName,
			views:   httpMetricViews(),
			finest:  0.01,
			atLeast: 6,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			boundaries := explicitBoundaries(t, testCase.views, testCase.name)

			below := 0
			for _, boundary := range boundaries {
				if boundary < 1 {
					below++
				}
			}
			if below < testCase.atLeast {
				t.Fatalf("boundaries below one second = %d, want at least %d: %v",
					below, testCase.atLeast, boundaries)
			}

			smallest := 0.0
			for _, boundary := range boundaries {
				if boundary > 0 {
					smallest = boundary
					break
				}
			}
			if smallest == 0 || smallest > testCase.finest {
				t.Fatalf("finest positive boundary = %v, want <= %v: %v",
					smallest, testCase.finest, boundaries)
			}
		})
	}
}

// The body size histograms carry the same defect for the same reason: the
// default boundaries stop at 10000, and a plan declaration payload for the
// ratified fixture is already 7225 bytes. Every payload of interest lands in
// the last two buckets, so the distribution says nothing.
func TestBodySizeHistogramsSpanRealisticPayloads(t *testing.T) {
	for _, name := range []string{httpRequestSizeName, httpResponseSizeName} {
		t.Run(name, func(t *testing.T) {
			boundaries := explicitBoundaries(t, httpMetricViews(), name)
			largest := boundaries[len(boundaries)-1]
			if largest < 1<<20 {
				t.Fatalf("largest boundary = %v bytes, want at least 1 MiB: %v", largest, boundaries)
			}
		})
	}
}

// explicitBoundaries resolves the stream a view produces for one instrument and
// returns its explicit bucket boundaries. An aggregation of another kind is a
// failure rather than a skip: these instruments are histograms, and a view that
// silently stopped setting an aggregation is exactly the regression here.
func explicitBoundaries(t *testing.T, views []sdkmetric.View, name string) []float64 {
	t.Helper()
	for _, view := range views {
		stream, matched := view(sdkmetric.Instrument{
			Name: name,
			Kind: sdkmetric.InstrumentKindHistogram,
		})
		if !matched {
			continue
		}
		aggregation, ok := stream.Aggregation.(sdkmetric.AggregationExplicitBucketHistogram)
		if !ok {
			t.Fatalf("%s aggregation = %T, want AggregationExplicitBucketHistogram "+
				"(an unset aggregation inherits the millisecond-shaped SDK defaults)",
				name, stream.Aggregation)
		}
		if len(aggregation.Boundaries) == 0 {
			t.Fatalf("%s declared an explicit histogram with no boundaries", name)
		}
		return aggregation.Boundaries
	}
	t.Fatalf("no view matched %s", name)
	return nil
}
