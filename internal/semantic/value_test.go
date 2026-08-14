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
