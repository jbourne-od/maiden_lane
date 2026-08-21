package semantic

import "testing"

// Production break caught: accepting malformed UTF-8 would make exact string
// bytes non-canonical across semantic artifacts.
func TestNewStringValueRejectsInvalidUTF8(t *testing.T) {
	_, err := NewStringValue(string([]byte{0xff}))
	if err == nil {
		t.Fatal("NewStringValue accepted invalid UTF-8")
	}
}

// Production break caught: normalizing valid UTF-8 would collapse distinct
// semantic inputs that the v1 format promises to preserve byte-for-byte.
func TestNewStringValuePreservesExactUTF8Bytes(t *testing.T) {
	composed := "caf\u00e9"
	decomposed := "cafe\u0301"

	a, err := NewStringValue(composed)
	if err != nil {
		t.Fatalf("NewStringValue(composed): %v", err)
	}
	b, err := NewStringValue(decomposed)
	if err != nil {
		t.Fatalf("NewStringValue(decomposed): %v", err)
	}

	gotA, ok := a.String()
	if !ok || gotA != composed {
		t.Fatalf("composed String() = %q, %v; want %q, true", gotA, ok, composed)
	}
	gotB, ok := b.String()
	if !ok || gotB != decomposed {
		t.Fatalf("decomposed String() = %q, %v; want %q, true", gotB, ok, decomposed)
	}
	if a.Equal(b) {
		t.Fatal("canonically distinct UTF-8 spellings compare equal")
	}
}

// Production break caught: rejecting an empty but present scalar would blur
// representation validation with later use-specific non-empty invariants.
func TestNewAtomValueAllowsEmptyPresentValue(t *testing.T) {
	value, err := NewAtomValue("")
	if err != nil {
		t.Fatalf("NewAtomValue: %v", err)
	}
	got, ok := value.String()
	if !ok || got != "" {
		t.Fatalf("String() = %q, %v; want empty, true", got, ok)
	}
}

// Production break caught: accepting malformed UTF-8 atoms would let invalid
// bytes enter equality checks and canonical identity.
func TestNewAtomValueRejectsInvalidUTF8(t *testing.T) {
	_, err := NewAtomValue(string([]byte{0xc3, 0x28}))
	if err == nil {
		t.Fatal("NewAtomValue accepted invalid UTF-8")
	}
}

// Production break caught: conflating the three closed scalar variants would
// allow schema validation to accept a value of the wrong semantic type.
func TestValueAccessorsPreserveClosedKinds(t *testing.T) {
	text, err := NewStringValue("10")
	if err != nil {
		t.Fatalf("NewStringValue: %v", err)
	}
	atom, err := NewAtomValue("10")
	if err != nil {
		t.Fatalf("NewAtomValue: %v", err)
	}
	integer := NewInt64Value(10)

	if text.Kind() != ValueString || atom.Kind() != ValueAtom || integer.Kind() != ValueInt64 {
		t.Fatalf("kinds = %v, %v, %v", text.Kind(), atom.Kind(), integer.Kind())
	}
	if _, ok := text.Int64(); ok {
		t.Fatal("string exposed an int64 value")
	}
	if got, ok := integer.Int64(); !ok || got != 10 {
		t.Fatalf("Int64() = %d, %v; want 10, true", got, ok)
	}
	if _, ok := integer.String(); ok {
		t.Fatal("int64 exposed a string value")
	}
}

// Production break caught: an unvalidated zero Value could masquerade as a
// fourth scalar kind and become canonically encodable.
func TestZeroValueIsNotValid(t *testing.T) {
	if (Value{}).Valid() {
		t.Fatal("zero Value reported valid")
	}
}

// The text codec is the plan store's dependency, so it is tested as one.
//
// Nothing exercised MarshalText or UnmarshalText directly: the only coverage was three
// literals inside a storage fixture -- the string "stored", the atom "T0" and the integer 1 --
// and a fixture whose integer is 1 cannot distinguish base ten from base sixteen, cannot
// distinguish int64 from int32, and cannot see a sign. Each of those is a mutant that
// compiles, runs, and survives, and each turns a stored plan unreadable after a successful
// write.
//
// The values below are chosen for the dimensions the codec can get wrong, not for realism.
func TestValueTextFormRoundTripsEveryDimension(t *testing.T) {
	cases := map[string]Value{
		"empty string": mustString(t, ""),
		"plain string": mustString(t, "certified"),
		// The separator is the first colon only, so a payload full of them must survive.
		"string of colons":        mustString(t, "a:b:c::"),
		"string spelling a value": mustString(t, "int64:5"),
		"string with a newline":   mustString(t, "two\nlines"),
		"unicode string":          mustString(t, "dépôt é中"),
		"empty atom":              mustAtom(t, ""),
		"atom":                    mustAtom(t, "T0"),
		// Byte-identical to a string, so only the kind separates them.
		"atom spelling a string": mustAtom(t, "certified"),
		"zero":                   NewInt64Value(0),
		"one":                    NewInt64Value(1),
		"negative":               NewInt64Value(-1),
		// Past int32, so a bitSize of 32 fails here and nowhere in any other fixture.
		"past int32":          NewInt64Value(3000000000),
		"past negative int32": NewInt64Value(-3000000000),
		// Base ten and base sixteen differ from ten upward.
		"two digits": NewInt64Value(16),
		"maximum":    NewInt64Value(1<<63 - 1),
		"minimum":    NewInt64Value(-1 << 63),

		// Timestamps
		"timestamp epoch":  mustTimestamp(t, "1970-01-01T00:00:00Z"),
		"timestamp recent": mustTimestamp(t, "2026-08-21T10:00:00.123Z"),

		// Durations
		"duration zero":     NewDurationValue(0),
		"duration 1h":       NewDurationValue(3600),
		"duration negative": NewDurationValue(-1800),

		// Decimals
		"decimal zero":     mustDecimal(t, "0"),
		"decimal positive": mustDecimal(t, "123.45"),
		"decimal negative": mustDecimal(t, "-0.005"),

		// Dates
		"date leap":   mustDate(t, "2024-02-29"),
		"date recent": mustDate(t, "2026-08-21"),
	}
	encoded := make(map[string]string, len(cases))
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			text, err := value.MarshalText()
			if err != nil {
				t.Fatalf("MarshalText: %v", err)
			}
			var returned Value
			if err := returned.UnmarshalText(text); err != nil {
				t.Fatalf("UnmarshalText(%q): %v", text, err)
			}
			if !returned.Equal(value) || returned.Kind() != value.Kind() {
				t.Fatalf("round trip produced %v (kind %d), want %v (kind %d)",
					returned, returned.Kind(), value, value.Kind())
			}
			encoded[name] = string(text)
		})
	}
	// And no two distinct values share a text form -- the property that makes the codec a
	// codec rather than a rendering. An atom and a string with the same bytes are the pair
	// that would collide if the kind stopped participating.
	seen := make(map[string]string, len(encoded))
	for name, text := range encoded {
		if previous, collision := seen[text]; collision {
			t.Fatalf("%q and %q both encode as %q", previous, name, text)
		}
		seen[text] = name
	}
}

func mustTimestamp(t *testing.T, s string) Value {
	t.Helper()
	v, err := NewTimestampValue(s)
	if err != nil {
		t.Fatalf("NewTimestampValue(%q): %v", s, err)
	}
	return v
}

func mustDecimal(t *testing.T, s string) Value {
	t.Helper()
	v, err := NewDecimalValue(s)
	if err != nil {
		t.Fatalf("NewDecimalValue(%q): %v", s, err)
	}
	return v
}

func mustDate(t *testing.T, s string) Value {
	t.Helper()
	v, err := NewDateValue(s)
	if err != nil {
		t.Fatalf("NewDateValue(%q): %v", s, err)
	}
	return v
}

// Refusals, each of which is a way a stored value could come back as something else.
//
// "int64:+5" is deliberately absent: strconv.ParseInt accepts a leading plus, so the decoder
// admits a form the encoder never writes. That is harmless rather than overlooked -- it maps
// to the same Value, MarshalText emits only the canonical spelling, and rebuild recompiles and
// compares identities, so a hand-edited row spelling five that way still reproduces the plan
// it claims to be. A strictness check here would refuse a text that means exactly what it
// says.
func TestValueTextFormRefusesWhatItCannotRebuild(t *testing.T) {
	for name, text := range map[string]string{
		"no separator":            "certified",
		"unknown kind":            "float:1.5",
		"empty kind":              ":5",
		"integer with spaces":     "int64: 5",
		"integer in hexadecimal":  "int64:0x10",
		"integer that is not one": "int64:many",
		"integer past the range":  "int64:9223372036854775808",
		"invalid utf8 string":     "string:\xff",
		"invalid utf8 atom":       "atom:\xff",
		"invalid timestamp":       "timestamp:not-a-timestamp",
		"invalid date":            "date:2026-02-29",
		"invalid decimal":         "decimal:abc",
	} {
		t.Run(name, func(t *testing.T) {
			var value Value
			if err := value.UnmarshalText([]byte(text)); err == nil {
				t.Fatalf("accepted %q as %v", text, value)
			}
			if value.Valid() {
				t.Fatal("a refused text produced a usable value")
			}
		})
	}
}

// A value the kernel would refuse must not be written, and the check is Valid rather than
// the kind. See MarshalText's comment: consulting the kind alone let an invalid string
// serialize, get its bad byte replaced by encoding/json, and read back as a DIFFERENT value
// with no error anywhere.
func TestValueTextFormRefusesAnInvalidValue(t *testing.T) {
	if _, err := (Value{}).MarshalText(); err == nil {
		t.Fatal("the zero value serialized")
	}
	invalid := Value{kind: ValueString, text: "\xff"}
	if invalid.Valid() {
		t.Fatal("the fixture is not invalid, so this test asserts nothing")
	}
	if text, err := invalid.MarshalText(); err == nil {
		t.Fatalf("an invalid string serialized as %q", text)
	}
	if text, err := (Value{kind: ValueAtom, text: "\xff"}).MarshalText(); err == nil {
		t.Fatalf("an invalid atom serialized as %q", text)
	}
}
