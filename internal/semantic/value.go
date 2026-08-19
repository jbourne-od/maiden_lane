package semantic

import (
	"fmt"
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
)

// Value is an immutable scalar. String and atom bytes are retained exactly;
// v1 intentionally performs no Unicode normalization.
type Value struct {
	kind    ValueKind
	text    string
	integer int64
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
	case ValueInt64:
		return true
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

// Equal compares the closed variant and its exact semantic value.
func (v Value) Equal(other Value) bool {
	if v.kind != other.kind {
		return false
	}
	switch v.kind {
	case ValueString, ValueAtom:
		return v.text == other.text
	case ValueInt64:
		return v.integer == other.integer
	default:
		return false
	}
}

func validValueKind(kind ValueKind) bool {
	return kind == ValueString || kind == ValueAtom || kind == ValueInt64
}

func validSemanticName(value string) bool {
	return value != "" && utf8.ValidString(value)
}
