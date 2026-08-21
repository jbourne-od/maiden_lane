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
)

// Value is an immutable scalar. String and atom bytes are retained exactly;
// v1 intentionally performs no Unicode normalization.
type Value struct {
	kind    ValueKind
	text    string
	integer int64
}

// MarshalText and UnmarshalText exist because a Value travels inside a stored declaration.
//
// TEXT RATHER THAN JSON, deliberately. The kernel's import allowlist excludes encoding/json,
// and it should: a transport encoding in the kernel is exactly the coupling Inviolate 12
// forbids. encoding.TextMarshaler is an interface, not a dependency -- json, YAML and any
// other codec pick it up on their own -- so the kernel describes its value in one canonical
// text form and owes nothing to any particular transport.
//
// THIS IS NOT A CANONICAL DECODER and does not weaken the encoders-only rule. Canonical bytes
// identify artifacts and are deliberately one-way. A declaration is an authored INPUT that the
// plan store round-trips and recompiles, checking the reproduced identity against the stored
// one; what could not be allowed is a Value that serializes to something recompilation does
// not reproduce.
//
// Which is what happened. Value's fields are unexported and it had no marshaller, so
// encoding/json wrote `{}` and returned NO ERROR. Before the select-and-assign operator no
// declaration contained a Value at all -- neither frozen operator carries one -- so the first
// authored rule with a literal was writable to Postgres and unreadable afterwards: the row
// stored, every read recompiled to an UnsupportedOperator diagnostic, GetPlan became a
// permanent 500, and the worker treated the storage error as transient and retried it forever.
//
// Marshal REFUSES an invalid value rather than emitting a form that cannot be read back, so a
// declaration that could not survive storage is rejected at write time instead of at every
// read.
//
// It consults Valid(), not the kind alone, and an earlier version consulted the kind while
// this comment claimed otherwise. Value{kind: ValueString, text: "\xff"} would then marshal;
// encoding/json substitutes U+FFFD for the invalid byte on the way out, and UnmarshalText
// accepts what comes back -- so the value round-trips into a DIFFERENT value with no error at
// any layer, which is worse than failing to read back. Unreachable from production, because
// such a Value is constructible only inside this package, and fixed anyway: mapping a value
// by its kind while ignoring its content is the precise defect the compiler and the evaluator
// shipped once already, and this would have been a third mapping doing it.
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
	default:
		return nil, fmt.Errorf("value has no kind and cannot be serialized")
	}
}

// UnmarshalText rebuilds a Value through the same constructors an author would use, so a
// stored value that is no longer legal -- invalid UTF-8, an empty atom, an out-of-range
// integer -- refuses here rather than becoming a Value the constructors would never produce.
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
