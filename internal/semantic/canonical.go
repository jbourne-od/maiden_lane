package semantic

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// Leaf canonical encoding table (v1):
//
//   lineage root: tag, namespace string, root-key string
//   source ID:    tag, lineage digest, entity-kind string, source-key string
//   schema:       tag, sorted entities(kind, sorted fields(name, kind byte,
//                 required marker)), sorted relations(kind, from-kind, to-kind)
//   state:        tag, schema digest, lineage digest, sorted entities(kind,
//                 ID digest, sorted fields(name, typed value)), sorted
//                 relations(kind, from ref(kind+digest), to ref(kind+digest))
//   world:        tag, sorted closed references(kind byte, content digest)
//
// Tags and semantic strings are uint64-big-endian length-prefixed exact UTF-8
// bytes. Counts are uint64 big endian. Int64 values use big-endian two's
// complement. Digests are 32 raw bytes decoded from validated lowercase
// sha256:<hex> strings. Optional/Boolean markers are exactly one byte, 0 or 1.

const (
	lineageRootDomainTag  = "maiden-lane.lineage-root.v1"
	sourceEntityDomainTag = "maiden-lane.source-entity-id.v1"
	schemaDomainTag       = "maiden-lane.schema.v1"
	stateDomainTag        = "maiden-lane.state.v1"
	worldDomainTag        = "maiden-lane.world.v1"
)

// contentHasher hashes bytes whose semantic meaning and canonical order have
// already been fixed by this package.
type contentHasher interface {
	HashCanonical([]byte) Digest
}

type sha256V1Hasher struct{}

func (sha256V1Hasher) HashCanonical(data []byte) Digest {
	sum := sha256.Sum256(data)
	return Digest("sha256:" + hex.EncodeToString(sum[:]))
}

type canonicalEncoder struct {
	buffer bytes.Buffer
	err    error
}

func (e *canonicalEncoder) tag(value string) {
	if e.err != nil {
		return
	}
	if value == "" {
		e.err = fmt.Errorf("canonical tag is empty")
		return
	}
	for _, b := range []byte(value) {
		if b > 0x7f {
			e.err = fmt.Errorf("canonical tag is not ASCII")
			return
		}
	}
	e.string(value)
}

func (e *canonicalEncoder) uint64(value uint64) {
	if e.err != nil {
		return
	}
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, e.err = e.buffer.Write(encoded[:])
}

func (e *canonicalEncoder) int64(value int64) {
	e.uint64(uint64(value))
}

func (e *canonicalEncoder) optional(present bool, write func()) {
	if e.err != nil {
		return
	}
	if !present {
		e.byte(0)
		return
	}
	e.byte(1)
	write()
}

func (e *canonicalEncoder) string(value string) {
	if e.err != nil {
		return
	}
	if !utf8.ValidString(value) {
		e.err = fmt.Errorf("canonical string is not valid UTF-8")
		return
	}
	e.uint64(uint64(len(value)))
	if e.err == nil {
		_, e.err = e.buffer.WriteString(value)
	}
}

func (e *canonicalEncoder) digest(value string) {
	if e.err != nil {
		return
	}
	raw, err := decodeDigest(value)
	if err != nil {
		e.err = err
		return
	}
	_, e.err = e.buffer.Write(raw[:])
}

func (e *canonicalEncoder) byte(value byte) {
	if e.err == nil {
		e.err = e.buffer.WriteByte(value)
	}
}

func (e *canonicalEncoder) value(value Value) {
	if !value.Valid() {
		if e.err == nil {
			e.err = fmt.Errorf("canonical value has invalid kind %d", value.Kind())
		}
		return
	}
	e.byte(byte(value.Kind()))
	switch value.Kind() {
	case ValueString, ValueAtom:
		e.string(value.text)
	case ValueInt64:
		e.int64(value.integer)
	}
}

func (e *canonicalEncoder) bytes() ([]byte, error) {
	if e.err != nil {
		return nil, e.err
	}
	return bytes.Clone(e.buffer.Bytes()), nil
}

func decodeDigest(value string) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return result, fmt.Errorf("digest must use sha256:<64 lowercase hex> form")
	}
	hexValue := strings.TrimPrefix(value, "sha256:")
	if strings.ToLower(hexValue) != hexValue {
		return result, fmt.Errorf("digest hexadecimal must be lowercase")
	}
	decoded, err := hex.DecodeString(hexValue)
	if err != nil || len(decoded) != sha256.Size {
		return result, fmt.Errorf("digest must use sha256:<64 lowercase hex> form")
	}
	copy(result[:], decoded)
	return result, nil
}

func canonicalDigest(data []byte) Digest {
	var hasher contentHasher = sha256V1Hasher{}
	return hasher.HashCanonical(data)
}

// NewInputLineageID derives a stable identity from a validated logical
// lineage-root declaration, not from a mutable observation snapshot.
func NewInputLineageID(namespace, rootKey string) (InputLineageID, error) {
	canonical, err := lineageRootCanonicalBytes(namespace, rootKey)
	if err != nil {
		return "", err
	}
	return InputLineageID(canonicalDigest(canonical)), nil
}

func lineageRootCanonicalBytes(namespace, rootKey string) ([]byte, error) {
	if !validSemanticName(namespace) {
		return nil, fmt.Errorf("lineage namespace is empty or invalid UTF-8")
	}
	if !validSemanticName(rootKey) {
		return nil, fmt.Errorf("lineage root key is empty or invalid UTF-8")
	}
	var encoder canonicalEncoder
	encoder.tag(lineageRootDomainTag)
	encoder.string(namespace)
	encoder.string(rootKey)
	return encoder.bytes()
}

// SourceEntityID deterministically namespaces an exact canonical source key.
// It returns the zero identity if a caller bypasses constructors and supplies
// malformed named values; NewEntity also rejects that zero at its boundary.
func SourceEntityID(lineage InputLineageID, kind EntityKind, canonicalSourceKey string) EntityID {
	canonical, err := sourceEntityIDCanonicalBytes(lineage, kind, canonicalSourceKey)
	if err != nil {
		return ""
	}
	return EntityID(canonicalDigest(canonical))
}

func sourceEntityIDCanonicalBytes(lineage InputLineageID, kind EntityKind, canonicalSourceKey string) ([]byte, error) {
	if _, err := decodeDigest(string(lineage)); err != nil {
		return nil, fmt.Errorf("input lineage ID: %w", err)
	}
	if !validSemanticName(string(kind)) {
		return nil, fmt.Errorf("entity kind is empty or invalid UTF-8")
	}
	if !validSemanticName(canonicalSourceKey) {
		return nil, fmt.Errorf("canonical source key is empty or invalid UTF-8")
	}
	var encoder canonicalEncoder
	encoder.tag(sourceEntityDomainTag)
	encoder.digest(string(lineage))
	encoder.string(string(kind))
	encoder.string(canonicalSourceKey)
	return encoder.bytes()
}

// WorldReferenceKind is the closed v1 union of pinned external semantic
// inputs. Storage location and transport representation are deliberately not
// part of this value.
type WorldReferenceKind uint8

const (
	WorldReferenceSnapshot WorldReferenceKind = iota + 1
	WorldReferenceConfiguration
)

// WorldReference identifies one immutable snapshot or configuration artifact.
type WorldReference struct {
	kind          WorldReferenceKind
	contentDigest Digest
}

// NewWorldReference constructs a validated member of the closed world set.
func NewWorldReference(kind WorldReferenceKind, contentDigest Digest) (WorldReference, error) {
	if !validWorldReferenceKind(kind) {
		return WorldReference{}, fmt.Errorf("unknown world reference kind %d", kind)
	}
	if _, err := decodeDigest(string(contentDigest)); err != nil {
		return WorldReference{}, fmt.Errorf("world content digest: %w", err)
	}
	return WorldReference{kind: kind, contentDigest: contentDigest}, nil
}

// Kind returns the closed snapshot/configuration variant.
func (r WorldReference) Kind() WorldReferenceKind {
	return r.kind
}

// ContentDigest returns the identity of the pinned referenced bytes.
func (r WorldReference) ContentDigest() Digest {
	return r.contentDigest
}

// World is an immutable, canonically ordered set of pinned references.
type World struct {
	references []WorldReference
	canonical  []byte
	id         WorldID
}

// NewWorld validates, copies, and normalizes a pinned-world set. An empty set
// remains a real versioned artifact.
func NewWorld(references []WorldReference) (World, error) {
	normalized := slices.Clone(references)
	for _, reference := range normalized {
		if !validWorldReferenceKind(reference.kind) {
			return World{}, fmt.Errorf("unknown world reference kind %d", reference.kind)
		}
		if _, err := decodeDigest(string(reference.contentDigest)); err != nil {
			return World{}, fmt.Errorf("world content digest: %w", err)
		}
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].kind != normalized[j].kind {
			return normalized[i].kind < normalized[j].kind
		}
		return normalized[i].contentDigest < normalized[j].contentDigest
	})
	for i := 1; i < len(normalized); i++ {
		if normalized[i] == normalized[i-1] {
			return World{}, fmt.Errorf("duplicate world reference")
		}
	}

	var encoder canonicalEncoder
	encoder.tag(worldDomainTag)
	encoder.uint64(uint64(len(normalized)))
	for _, reference := range normalized {
		encoder.byte(byte(reference.kind))
		encoder.digest(string(reference.contentDigest))
	}
	canonical, err := encoder.bytes()
	if err != nil {
		return World{}, fmt.Errorf("canonicalize world: %w", err)
	}
	return World{
		references: normalized,
		canonical:  canonical,
		id:         WorldID(canonicalDigest(canonical)),
	}, nil
}

// References returns a copy of the canonical ordered set.
func (w World) References() []WorldReference {
	return slices.Clone(w.references)
}

// CanonicalBytes returns a copy of the v1 world bytes.
func (w World) CanonicalBytes() []byte {
	return bytes.Clone(w.canonical)
}

// ID returns the content identity of the canonical world.
func (w World) ID() WorldID {
	return w.id
}

func validWorldReferenceKind(kind WorldReferenceKind) bool {
	return kind == WorldReferenceSnapshot || kind == WorldReferenceConfiguration
}

func encodeSchema(declaration SchemaDeclaration) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(schemaDomainTag)
	encoder.uint64(uint64(len(declaration.entities)))
	for _, entity := range declaration.entities {
		encoder.string(string(entity.Kind))
		encoder.uint64(uint64(len(entity.Fields)))
		for _, field := range entity.Fields {
			encoder.string(string(field.Name))
			encoder.byte(byte(field.Kind))
			encoder.optional(field.RequiredAtConstruction, func() {})
		}
	}
	encoder.uint64(uint64(len(declaration.relations)))
	for _, relation := range declaration.relations {
		encoder.string(string(relation.Kind))
		encoder.string(string(relation.FromKind))
		encoder.string(string(relation.ToKind))
	}
	return encoder.bytes()
}

func encodeState(state State) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(stateDomainTag)
	encoder.digest(string(state.schema.digest))
	encoder.digest(string(state.lineage))
	encoder.uint64(uint64(len(state.entities)))
	for _, entity := range state.entities {
		encoder.string(string(entity.ref.Kind))
		encoder.digest(string(entity.ref.ID))
		fieldNames := make([]FieldName, 0, len(entity.fields))
		for name := range entity.fields {
			fieldNames = append(fieldNames, name)
		}
		slices.Sort(fieldNames)
		encoder.uint64(uint64(len(fieldNames)))
		for _, name := range fieldNames {
			encoder.string(string(name))
			encoder.value(entity.fields[name])
		}
	}
	encoder.uint64(uint64(len(state.relations)))
	for _, relation := range state.relations {
		encoder.string(string(relation.Kind))
		encoder.string(string(relation.From.Kind))
		encoder.digest(string(relation.From.ID))
		encoder.string(string(relation.To.Kind))
		encoder.digest(string(relation.To.ID))
	}
	return encoder.bytes()
}

func compareEntityRefs(a, b EntityRef) int {
	if result := compare(string(a.Kind), string(b.Kind)); result != 0 {
		return result
	}
	return compare(string(a.ID), string(b.ID))
}

func compareRelations(a, b Relation) int {
	if result := compare(string(a.Kind), string(b.Kind)); result != 0 {
		return result
	}
	if result := compare(string(a.From.ID), string(b.From.ID)); result != 0 {
		return result
	}
	if result := compare(string(a.To.ID), string(b.To.ID)); result != 0 {
		return result
	}
	if result := compare(string(a.From.Kind), string(b.From.Kind)); result != 0 {
		return result
	}
	return compare(string(a.To.Kind), string(b.To.Kind))
}
