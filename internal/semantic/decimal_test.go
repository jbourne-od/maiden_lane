package semantic

import (
	"testing"
)

func TestDecimalParseAndFormat(t *testing.T) {
	tests := []struct {
		input     string
		want      string
		wantValid bool
	}{
		{"0", "0", true},
		{"0.0", "0", true},
		{"00.00", "0", true},
		{"-0", "0", true},
		{"-0.0", "0", true},
		{"123", "123", true},
		{"+123", "123", true},
		{"-123", "-123", true},
		{"0123", "123", true},
		{"123.4500", "123.45", true},
		{"-0.50", "-0.5", true},
		{"0.0001", "0.0001", true},
		{"1000.000", "1000", true},
		{"-1000.000", "-1000", true},
		{"", "", false},
		{".", "", false},
		{"abc", "", false},
		{"12.34.56", "", false},
		{"12e3", "", false},
		{"--12", "", false},
		{"+-12", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			d, err := parseDecimal(tc.input)
			if tc.wantValid {
				if err != nil {
					t.Fatalf("parseDecimal(%q) unexpected error: %v", tc.input, err)
				}
				if got := d.String(); got != tc.want {
					t.Errorf("parseDecimal(%q).String() = %q, want %q", tc.input, got, tc.want)
				}
			} else {
				if err == nil {
					t.Fatalf("parseDecimal(%q) expected error, got nil", tc.input)
				}
			}
		})
	}
}

func TestDecimalComparison(t *testing.T) {
	tests := []struct {
		a, b      string
		wantEqual bool
		wantLess  bool
	}{
		{"0", "0", true, false},
		{"0", "0.00", true, false},
		{"-0", "0", true, false},
		{"1", "1", true, false},
		{"1.5", "1.50", true, false},
		{"-1.5", "-1.50", true, false},
		{"0.05", ".05", true, false},
		{"00.050", ".05", true, false},
		{"00123.450", "123.45", true, false},
		{"0.0001", ".0001", true, false},
		{"-0.05", "-.05", true, false},
		{"1", "2", false, true},
		{"2", "1", false, false},
		{"-2", "-1", false, true},
		{"-1", "-2", false, false},
		{"-1", "0", false, true},
		{"0", "-1", false, false},
		{"1.001", "1.002", false, true},
		{"1.002", "1.001", false, false},
		{"-1.002", "-1.001", false, true},
		{"99.99", "100.0", false, true},
	}

	for _, tc := range tests {
		t.Run(tc.a+"_vs_"+tc.b, func(t *testing.T) {
			da, err := parseDecimal(tc.a)
			if err != nil {
				t.Fatalf("parseDecimal(%q): %v", tc.a, err)
			}
			db, err := parseDecimal(tc.b)
			if err != nil {
				t.Fatalf("parseDecimal(%q): %v", tc.b, err)
			}

			if got := da.Equal(db); got != tc.wantEqual {
				t.Errorf("(%s).Equal(%s) = %t, want %t", tc.a, tc.b, got, tc.wantEqual)
			}
			if got := da.Less(db); got != tc.wantLess {
				t.Errorf("(%s).Less(%s) = %t, want %t", tc.a, tc.b, got, tc.wantLess)
			}
		})
	}
}

func TestDecimalArithmetic(t *testing.T) {
	tests := []struct {
		a, b string
		add  string
		sub  string
		mul  string
		div  string
	}{
		{"10", "2", "12", "8", "20", "5"},
		{"1.5", "2.5", "4", "-1", "3.75", "0.6"},
		{"-1.5", "2.5", "1", "-4", "-3.75", "-0.6"},
		{"-1.5", "-2.5", "-4", "1", "3.75", "0.6"},
		{"0.1", "0.2", "0.3", "-0.1", "0.02", "0.5"},
		{"100.50", "0.50", "101", "100", "50.25", "201"},
		{"0", "5", "5", "-5", "0", "0"},
		{"7", "3", "10", "4", "21", "2.333333333333333333"},
		{"0.0000000000000000000000001", "2", "2.0000000000000000000000001", "-1.9999999999999999999999999", "0.0000000000000000000000002", "0.00000000000000000000000005"},
	}

	for _, tc := range tests {
		t.Run(tc.a+"_and_"+tc.b, func(t *testing.T) {
			da, _ := parseDecimal(tc.a)
			db, _ := parseDecimal(tc.b)

			addRes, err := da.Add(db)
			if err != nil {
				t.Fatalf("Add: %v", err)
			}
			if got := addRes.String(); got != tc.add {
				t.Errorf("%s + %s = %s, want %s", tc.a, tc.b, got, tc.add)
			}

			subRes, err := da.Sub(db)
			if err != nil {
				t.Fatalf("Sub: %v", err)
			}
			if got := subRes.String(); got != tc.sub {
				t.Errorf("%s - %s = %s, want %s", tc.a, tc.b, got, tc.sub)
			}

			mulRes, err := da.Mul(db)
			if err != nil {
				t.Fatalf("Mul: %v", err)
			}
			if got := mulRes.String(); got != tc.mul {
				t.Errorf("%s * %s = %s, want %s", tc.a, tc.b, got, tc.mul)
			}

			divRes, err := da.Div(db)
			if err != nil {
				t.Fatalf("Div: %v", err)
			}
			if got := divRes.String(); got != tc.div {
				t.Errorf("%s / %s = %s, want %s", tc.a, tc.b, got, tc.div)
			}
		})
	}
}

func TestDecimalDivideByZero(t *testing.T) {
	da, _ := parseDecimal("10")
	dzero, _ := parseDecimal("0")
	_, err := da.Div(dzero)
	if err == nil {
		t.Fatal("expected error on divide by zero, got nil")
	}
}

func TestDecimalAbsAndClamp(t *testing.T) {
	d1, _ := parseDecimal("-12.34")
	d2, _ := parseDecimal("12.34")
	if d1.Abs().String() != "12.34" {
		t.Fatalf("Abs(-12.34) = %s, want 12.34", d1.Abs().String())
	}
	if d2.Abs().String() != "12.34" {
		t.Fatalf("Abs(12.34) = %s, want 12.34", d2.Abs().String())
	}

	dVal, _ := parseDecimal("15")
	dMin, _ := parseDecimal("10")
	dMax, _ := parseDecimal("20")

	c1, err := dVal.Clamp(dMin, dMax)
	if err != nil || c1.String() != "15" {
		t.Fatalf("Clamp(15, 10, 20) = %s, %v", c1.String(), err)
	}

	dLow, _ := parseDecimal("5")
	c2, err := dLow.Clamp(dMin, dMax)
	if err != nil || c2.String() != "10" {
		t.Fatalf("Clamp(5, 10, 20) = %s, %v", c2.String(), err)
	}

	dHigh, _ := parseDecimal("25")
	c3, err := dHigh.Clamp(dMin, dMax)
	if err != nil || c3.String() != "20" {
		t.Fatalf("Clamp(25, 10, 20) = %s, %v", c3.String(), err)
	}

	_, err = dVal.Clamp(dMax, dMin)
	if err == nil {
		t.Fatal("Clamp with min > max must error")
	}
}
