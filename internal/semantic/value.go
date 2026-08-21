package semantic

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Distinct named string types prevent identities from different semantic
// layers from being interchanged accidentally, even though every digest uses
// the same external sha256:<hex> representation.
type (
	Digest                   string
	InputLineageID           string
	EntityID                 string
	EntityKind               string
	FieldName                string
	RelationKind             string
	RuleID                   string
	CheckpointKey            string
	SchemaDigest             string
	RulesetDigest            string
	CompilationInputDigest   string
	StateDigest              string
	PlanID                   string
	InputID                  string
	WorldID                  string
	SemanticRunID            string
	ProvenancePolicyID       string
	ExecutionID              string
	PatchDigest              string
	JournalEntryDigest       string
	JournalPrefixDigest      string
	InvariantResultDigest    string
	CheckpointID             string
	CheckpointArtifactID     string
	CheckpointArtifactDigest string
	ProfileID                string
	AssessmentID             string
	AssessmentDigest         string
	CorpusID                 string
	ComparisonPolicyID       string
	ComparisonID             string
	CompilationFailureDigest string
	FailureReportDigest      string
	CompilerSemanticsVersion string
)

// ExecutorIdentity is a canonical technical identity for one executor build.
// Backend is deliberately syntax-only; certification is a separate concern.
type ExecutorIdentity struct {
	backend string
	version Digest
}

// NewExecutorIdentity validates the narrow v1 backend token and immutable
// executor-version digest used by execution identity.
func NewExecutorIdentity(backend string, version Digest) (ExecutorIdentity, error) {
	if !validExecutorBackend(backend) {
		return ExecutorIdentity{}, fmt.Errorf("executor backend must match [a-z0-9][a-z0-9.-]*")
	}
	if _, err := decodeDigest(string(version)); err != nil {
		return ExecutorIdentity{}, fmt.Errorf("executor version: %w", err)
	}
	return ExecutorIdentity{backend: backend, version: version}, nil
}

// Backend returns the canonical technical backend token.
func (i ExecutorIdentity) Backend() string { return i.backend }

// Version returns the immutable executor build digest.
func (i ExecutorIdentity) Version() Digest { return i.version }

func validExecutorBackend(value string) bool {
	if value == "" || ((value[0] < 'a' || value[0] > 'z') && (value[0] < '0' || value[0] > '9')) {
		return false
	}
	for _, b := range []byte(value) {
		if (b < 'a' || b > 'z') && (b < '0' || b > '9') && b != '.' && b != '-' {
			return false
		}
	}
	return true
}

// ValueKind identifies one of the closed scalar variants supported by the
// initial semantic format.
type ValueKind uint8

const (
	ValueString ValueKind = iota + 1
	ValueAtom
	ValueInt64
	ValueTimestamp
	ValueDuration
	ValueDecimal
	ValueDate
)

// Value is an immutable scalar. String and atom bytes are retained exactly;
// v1 intentionally performs no Unicode normalization.
type Value struct {
	kind    ValueKind
	text    string
	integer int64
}

// MarshalText and UnmarshalText exist because a Value travels inside a stored declaration.
func (v Value) MarshalText() ([]byte, error) {
	if !v.Valid() {
		return nil, fmt.Errorf("value is invalid and cannot be serialized")
	}
	switch v.kind {
	case ValueString:
		return []byte("string:" + v.text), nil
	case ValueAtom:
		return []byte("atom:" + v.text), nil
	case ValueInt64:
		return []byte("int64:" + strconv.FormatInt(v.integer, 10)), nil
	case ValueTimestamp:
		return []byte("timestamp:" + v.text), nil
	case ValueDuration:
		return []byte("duration:" + strconv.FormatInt(v.integer, 10)), nil
	case ValueDecimal:
		return []byte("decimal:" + v.text), nil
	case ValueDate:
		return []byte("date:" + v.text), nil
	default:
		return nil, fmt.Errorf("value has no kind and cannot be serialized")
	}
}

// UnmarshalText rebuilds a Value through the same constructors an author would use.
func (v *Value) UnmarshalText(data []byte) error {
	text := string(data)
	// The first colon only: a string value may contain as many more as it likes.
	kind, payload, separated := strings.Cut(text, ":")
	if !separated {
		return fmt.Errorf("value text carries no kind")
	}
	switch kind {
	case "string":
		result, err := NewStringValue(payload)
		if err != nil {
			return err
		}
		*v = result
	case "atom":
		result, err := NewAtomValue(payload)
		if err != nil {
			return err
		}
		*v = result
	case "int64":
		number, err := strconv.ParseInt(payload, 10, 64)
		if err != nil {
			return fmt.Errorf("int64 value is not a base-ten integer: %w", err)
		}
		*v = NewInt64Value(number)
	case "timestamp":
		result, err := NewTimestampValue(payload)
		if err != nil {
			return err
		}
		*v = result
	case "duration":
		number, err := strconv.ParseInt(payload, 10, 64)
		if err != nil {
			return fmt.Errorf("duration value is not a base-ten integer: %w", err)
		}
		*v = NewDurationValue(number)
	case "decimal":
		result, err := NewDecimalValue(payload)
		if err != nil {
			return err
		}
		*v = result
	case "date":
		result, err := NewDateValue(payload)
		if err != nil {
			return err
		}
		*v = result
	default:
		return fmt.Errorf("value kind %q is not in the closed vocabulary", kind)
	}
	return nil
}

// NewStringValue constructs a validated UTF-8 string value.
func NewStringValue(value string) (Value, error) {
	if !utf8.ValidString(value) {
		return Value{}, fmt.Errorf("string value is not valid UTF-8")
	}
	return Value{kind: ValueString, text: value}, nil
}

// NewAtomValue constructs a validated UTF-8 atom value. Empty atoms remain
// representable; rules that use an atom decide whether emptiness is lawful.
func NewAtomValue(value string) (Value, error) {
	if !utf8.ValidString(value) {
		return Value{}, fmt.Errorf("atom value is not valid UTF-8")
	}
	return Value{kind: ValueAtom, text: value}, nil
}

// NewInt64Value constructs an exact signed 64-bit integer value.
func NewInt64Value(value int64) Value {
	return Value{kind: ValueInt64, integer: value}
}

// NewTimestampValue constructs an exact UTC RFC3339 instant value.
func NewTimestampValue(value string) (Value, error) {
	ts, err := parseTimestamp(value)
	if err != nil {
		return Value{}, fmt.Errorf("timestamp value: %w", err)
	}
	return Value{kind: ValueTimestamp, text: ts.String(), integer: ts.unixNano}, nil
}

// NewDurationValue constructs an exact duration value in seconds.
func NewDurationValue(seconds int64) Value {
	return Value{kind: ValueDuration, integer: seconds}
}

// NewDecimalValue constructs an exact validated fixed-point decimal value.
func NewDecimalValue(value string) (Value, error) {
	d, err := parseDecimal(value)
	if err != nil {
		return Value{}, fmt.Errorf("decimal value: %w", err)
	}
	return Value{kind: ValueDecimal, text: d.String()}, nil
}

// NewDateValue constructs an exact ISO-8601 calendar date value (YYYY-MM-DD).
func NewDateValue(value string) (Value, error) {
	d, err := parseDate(value)
	if err != nil {
		return Value{}, fmt.Errorf("date value: %w", err)
	}
	return Value{kind: ValueDate, text: d.String(), integer: d.daysSinceEpoch}, nil
}

// Kind returns the closed scalar variant.
func (v Value) Kind() ValueKind {
	return v.kind
}

// Valid reports whether the value is one of the closed, canonically encodable
// scalar variants.
func (v Value) Valid() bool {
	switch v.kind {
	case ValueString, ValueAtom:
		return utf8.ValidString(v.text)
	case ValueInt64, ValueDuration:
		return true
	case ValueTimestamp:
		_, err := parseTimestamp(v.text)
		return err == nil
	case ValueDecimal:
		_, err := parseDecimal(v.text)
		return err == nil
	case ValueDate:
		_, err := parseDate(v.text)
		return err == nil
	default:
		return false
	}
}

// String returns exact string or atom bytes and reports false for other kinds.
func (v Value) String() (string, bool) {
	if v.kind != ValueString && v.kind != ValueAtom {
		return "", false
	}
	return v.text, true
}

// Int64 returns the integer and reports false for other kinds.
func (v Value) Int64() (int64, bool) {
	if v.kind != ValueInt64 {
		return 0, false
	}
	return v.integer, true
}

// Timestamp returns the RFC3339 timestamp string and reports false for other kinds.
func (v Value) Timestamp() (string, bool) {
	if v.kind != ValueTimestamp {
		return "", false
	}
	return v.text, true
}

// Duration returns duration in seconds and reports false for other kinds.
func (v Value) Duration() (int64, bool) {
	if v.kind != ValueDuration {
		return 0, false
	}
	return v.integer, true
}

// Decimal returns normalized decimal string and reports false for other kinds.
func (v Value) Decimal() (string, bool) {
	if v.kind != ValueDecimal {
		return "", false
	}
	return v.text, true
}

// Date returns ISO-8601 date string and reports false for other kinds.
func (v Value) Date() (string, bool) {
	if v.kind != ValueDate {
		return "", false
	}
	return v.text, true
}

// Equal compares the closed variant and its exact semantic value.
func (v Value) Equal(other Value) bool {
	if v.kind != other.kind {
		return false
	}
	switch v.kind {
	case ValueString, ValueAtom, ValueTimestamp, ValueDecimal, ValueDate:
		return v.text == other.text
	case ValueInt64, ValueDuration:
		return v.integer == other.integer
	default:
		return false
	}
}

func validValueKind(kind ValueKind) bool {
	return kind >= ValueString && kind <= ValueDate
}

func validSemanticName(value string) bool {
	return value != "" && utf8.ValidString(value)
}
