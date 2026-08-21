package semantic

import (
	"testing"
)

func TestTimestampParseAndFormat(t *testing.T) {
	tests := []struct {
		input     string
		want      string
		wantValid bool
	}{
		{"2026-08-21T10:00:00Z", "2026-08-21T10:00:00Z", true},
		{"2026-08-21T10:00:00+00:00", "2026-08-21T10:00:00Z", true},
		{"2026-08-21T10:00:00-00:00", "2026-08-21T10:00:00Z", true},
		{"2026-08-21T10:00:00.123Z", "2026-08-21T10:00:00.123Z", true},
		{"2026-08-21T10:00:00.123456789Z", "2026-08-21T10:00:00.123456789Z", true},
		{"2026-08-21T10:00:00.120000Z", "2026-08-21T10:00:00.12Z", true},
		{"1970-01-01T00:00:00Z", "1970-01-01T00:00:00Z", true},
		{"2000-02-29T23:59:59Z", "2000-02-29T23:59:59Z", true},
		{"2262-04-11T23:47:16.854775807Z", "2262-04-11T23:47:16.854775807Z", true},
		{"2500-01-01T00:00:00Z", "", false}, // Beyond int64 nanoseconds (year 2262)
		{"1500-01-01T00:00:00Z", "", false}, // Before int64 nanoseconds (year 1678)
		{"", "", false},
		{"2026-08-21", "", false},
		{"2026-08-21 10:00:00", "", false},
		{"2026-08-21T10:00:00+05:00", "", false}, // Non-UTC timezone refused in pure kernel
		{"2026-08-21T10:00:00-08:00", "", false},
		{"2026-02-29T10:00:00Z", "", false}, // 2026 is not a leap year
		{"2026-13-01T10:00:00Z", "", false},
		{"2026-08-32T10:00:00Z", "", false},
		{"2026-08-21T25:00:00Z", "", false},
		{"2026-08-21T10:60:00Z", "", false},
		{"2026-08-21T10:00:60Z", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			ts, err := parseTimestamp(tc.input)
			if tc.wantValid {
				if err != nil {
					t.Fatalf("parseTimestamp(%q) unexpected error: %v", tc.input, err)
				}
				if got := ts.String(); got != tc.want {
					t.Errorf("parseTimestamp(%q).String() = %q, want %q", tc.input, got, tc.want)
				}
			} else {
				if err == nil {
					t.Fatalf("parseTimestamp(%q) expected error, got nil", tc.input)
				}
			}
		})
	}
}

func TestTimestampComparisonAndMath(t *testing.T) {
	t1, err := parseTimestamp("2026-08-21T10:00:00Z")
	if err != nil {
		t.Fatalf("parseTimestamp t1: %v", err)
	}
	t2, err := parseTimestamp("2026-08-21T11:00:00Z")
	if err != nil {
		t.Fatalf("parseTimestamp t2: %v", err)
	}
	t3, err := parseTimestamp("2026-08-21T10:00:00.000Z")
	if err != nil {
		t.Fatalf("parseTimestamp t3: %v", err)
	}

	if !t1.Equal(t3) {
		t.Errorf("t1 should equal t3")
	}
	if !t1.Less(t2) {
		t.Errorf("t1 should be less than t2")
	}
	if t2.Less(t1) {
		t.Errorf("t2 should not be less than t1")
	}

	// Add 3600 seconds (1 hour)
	t1Plus1h, err := t1.AddDuration(3600)
	if err != nil {
		t.Fatalf("AddDuration: %v", err)
	}
	if !t1Plus1h.Equal(t2) {
		t.Errorf("t1 + 1h = %s, want %s", t1Plus1h.String(), t2.String())
	}

	// Diff (t2 - t1) = 3600 seconds
	diff, err := t2.Diff(t1)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if diff != 3600 {
		t.Errorf("t2 - t1 = %d, want 3600", diff)
	}

	// Diff (t1 - t2) = -3600 seconds
	diffRev, err := t1.Diff(t2)
	if err != nil {
		t.Fatalf("Diff reverse: %v", err)
	}
	if diffRev != -3600 {
		t.Errorf("t1 - t2 = %d, want -3600", diffRev)
	}

	// Large difference spanning across centuries (2250 AD vs 1690 AD) without overflow
	tFarFuture, err := parseTimestamp("2250-01-01T00:00:00.800Z")
	if err != nil {
		t.Fatalf("parseTimestamp 2250: %v", err)
	}
	tFarPast, err := parseTimestamp("1690-01-01T00:00:00.700Z")
	if err != nil {
		t.Fatalf("parseTimestamp 1690: %v", err)
	}
	largeDiff, err := tFarFuture.Diff(tFarPast)
	if err != nil {
		t.Fatalf("Diff large span: %v", err)
	}
	if largeDiff != 17671824000 {
		t.Fatalf("Diff large span = %d, want 17671824000", largeDiff)
	}

	// Reverse difference
	largeDiffRev, err := tFarPast.Diff(tFarFuture)
	if err != nil {
		t.Fatalf("Diff large span rev: %v", err)
	}
	if largeDiffRev != -17671824000 {
		t.Fatalf("Diff large span rev = %d, want -17671824000", largeDiffRev)
	}

	// Large difference with fractional crossing (0.2s - 0.7s)
	tFarFutureLowFrac, _ := parseTimestamp("2250-01-01T00:00:00.200Z")
	largeDiffCross, err := tFarFutureLowFrac.Diff(tFarPast)
	if err != nil {
		t.Fatalf("Diff large span cross: %v", err)
	}
	if largeDiffCross != 17671823999 {
		t.Fatalf("Diff large span cross = %d, want 17671823999", largeDiffCross)
	}

	largeDiffCrossRev, err := tFarPast.Diff(tFarFutureLowFrac)
	if err != nil {
		t.Fatalf("Diff large span cross rev: %v", err)
	}
	if largeDiffCrossRev != -17671823999 {
		t.Fatalf("Diff large span cross rev = %d, want -17671823999", largeDiffCrossRev)
	}
}

func TestTimestampDateExtract(t *testing.T) {
	ts, err := parseTimestamp("2026-08-21T14:30:45.123456789Z") // 2026-08-21 was a Friday (ISO weekday 5)
	if err != nil {
		t.Fatalf("parseTimestamp: %v", err)
	}

	units := map[string]int64{
		"year":        2026,
		"month":       8,
		"day":         21,
		"hour":        14,
		"minute":      30,
		"second":      45,
		"day_of_week": 5, // Friday = 5
	}

	for unit, want := range units {
		got, err := ts.DateExtract(unit)
		if err != nil {
			t.Fatalf("DateExtract(%q): %v", unit, err)
		}
		if got != want {
			t.Errorf("DateExtract(%q) = %d, want %d", unit, got, want)
		}
	}

	_, err = ts.DateExtract("invalid_unit")
	if err == nil {
		t.Fatal("DateExtract with invalid unit must error")
	}
}

func TestDateParseAndFormat(t *testing.T) {
	tests := []struct {
		input     string
		want      string
		wantValid bool
	}{
		{"2026-08-21", "2026-08-21", true},
		{"1970-01-01", "1970-01-01", true},
		{"2000-02-29", "2000-02-29", true},
		{"2024-02-29", "2024-02-29", true},
		{"", "", false},
		{"2026-08-21T10:00:00Z", "", false},
		{"2026/08/21", "", false},
		{"2026-02-29", "", false}, // Not a leap year
		{"2026-13-01", "", false},
		{"2026-08-32", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			d, err := parseDate(tc.input)
			if tc.wantValid {
				if err != nil {
					t.Fatalf("parseDate(%q) unexpected error: %v", tc.input, err)
				}
				if got := d.String(); got != tc.want {
					t.Errorf("parseDate(%q).String() = %q, want %q", tc.input, got, tc.want)
				}
			} else {
				if err == nil {
					t.Fatalf("parseDate(%q) expected error, got nil", tc.input)
				}
			}
		})
	}
}

func TestDateComparisonAndExtract(t *testing.T) {
	d1, _ := parseDate("2026-08-21")
	d2, _ := parseDate("2026-08-22")
	d3, _ := parseDate("2026-08-21")

	if !d1.Equal(d3) {
		t.Errorf("d1 should equal d3")
	}
	if !d1.Less(d2) {
		t.Errorf("d1 should be less than d2")
	}
	if d2.Less(d1) {
		t.Errorf("d2 should not be less than d1")
	}

	y, err := d1.DateExtract("year")
	if err != nil || y != 2026 {
		t.Errorf("DateExtract year = %d, %v", y, err)
	}
	m, err := d1.DateExtract("month")
	if err != nil || m != 8 {
		t.Errorf("DateExtract month = %d, %v", m, err)
	}
	day, err := d1.DateExtract("day")
	if err != nil || day != 21 {
		t.Errorf("DateExtract day = %d, %v", day, err)
	}
	dow, err := d1.DateExtract("day_of_week")
	if err != nil || dow != 5 {
		t.Errorf("DateExtract day_of_week = %d, %v", dow, err)
	}
}
