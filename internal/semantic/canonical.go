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

// Expression encoding table (v1)
//
//	expression      tag ‖ compilerVersion ‖ schemaDigest ‖ node
//	node            kindByte ‖ operands, selected by kind:
//	                  literal            value
//	                  field, exists      fieldPath
//	                  not, all, any,     uint64(argCount) ‖ node...
//	                  equal, less, add
//
// The compiler version and schema digest lead because Type() is a fact about
// (schema, expression) rather than about the expression alone, so the bytes must fix what
// determined it -- the same reason compiledProfile carries both. Nested nodes carry no tag of
// their own; the tag is written once at the top. Kind bytes are append-only.

// Compiler artifact encoding table (v1):
//
//   ruleset:      tag, sorted complete transformation declarations, sorted
//                 compiler-derived invariant declarations, sorted checkpoint
//                 declarations
//   compiler input: tag, schema digest, ruleset digest, compiler-semantics
//                 version, sorted complete profile source declarations
//   plan:         tag, schema digest, ruleset digest, compiler-semantics
//                 version, dependency-ordered compiled transformations
//                 (complete source declaration, derived reads/writes/accesses,
//                 dependencies, level, derived invariants), prefix-ordered
//                 checkpoints, required provenance policy
//   profile:      tag, compiler-semantics version, schema digest, normalized
//                 complete profile declaration, sorted implication proofs
//   compile failure: tag, compiler-input digest, INVALID_PLAN kind, sorted
//                 diagnostics(code, subject key, detail key)
//
// Patch artifact encoding table (v1):
//
//   patch:        tag, schema digest, operation count, operations in canonical
//                 rank/key order
//   insert:       Insert tag, complete entity(kind, ID, sorted fields)
//   relate:       Relate tag, relation(kind, from ref, to ref)
//   update:       Update tag, target ref, field count, fields sorted by name;
//                 each field contains its explicit before presence marker and
//                 optional typed value followed by its present after value
//
// Execution artifact encoding table (v1):
//
//   provenance policy: tag, closed policy token
//   input identity:     tag, initial-state digest, world ID
//   semantic run:       tag, input ID, plan ID
//   executor identity:  tag, backend token, version digest
//   execution identity: tag, semantic-run ID, counted canonical executor
//                       identity bytes, provenance-policy ID
//   synthetic entity:   tag, lineage ID, entity kind, rule ID, sorted
//                       progenitor EntityIDs, typed semantic output key
//   invariant results:  tag, results sorted by declaration key; each contains
//                       key, scope, boundary, verdict, code, sorted entity and
//                       fact references
//   journal entry:      tag, rule ID, predecessor/result state digests, patch
//                       digest, sorted fact evidence, counted complete
//                       invariant-result-set bytes
//   journal prefix:     tag, semantic-run ID, provenance-policy ID, accepted
//                       entry digests in execution order
//   protected failure:  tag, run ID, failure kind, protected-code union and
//                       code, rule/predecessor, invariant results, sorted safe
//                       references, optional proposed-patch digest
//   integrity failure:  tag, run ID, failure kind and integrity code, artifact
//                       kind/ref, optional verified-prefix/expected/observed
//                       links, sorted artifact references
//
// Checkpoint artifact encoding table (v1):
//
//   checkpoint ID:       tag, plan ID, canonical checkpoint declaration key
//   checkpoint claim:    tag, semantic-run ID, checkpoint ID, state digest,
//                       journal-prefix digest, invariant-result-set digest,
//                       provenance-policy ID
//   checkpoint manifest: tag, plan ID, checkpoint ID, declaration key and
//                       boundary, semantic-run/input/initial-state/world/policy
//                       replay links, state/journal-prefix/invariant-result-set
//                       claim links
//
// Assessment artifact encoding table (v1):
//
//   assessment ID:      tag, checkpoint artifact ID, profile ID
//   assessment:         tag, assessment-semantics version, checkpoint artifact
//                       ID, profile ID, verdict token ("ready"/"needs_input"),
//                       selected entities in canonical (kind, ID) order; each
//                       entity contains its ref and every normalized
//                       requirement result in canonical requirement order
//                       (code, one-byte satisfied(1)/missing(2) marker, sorted
//                       fact references(entity ref, field)); trailing count of
//                       assessment-level safe evidence references (always zero
//                       in v1; the field is part of the ratified record shape)
//
// Tags and semantic strings are uint64-big-endian length-prefixed exact UTF-8
// bytes. Counts are uint64 big endian. Int64 values use big-endian two's
// complement. Digests are 32 raw bytes decoded from validated lowercase
// sha256:<hex> strings. Optional/Boolean markers are exactly one byte, 0 or 1.

const (
	lineageRootDomainTag        = "maiden-lane.lineage-root.v1"
	sourceEntityDomainTag       = "maiden-lane.source-entity-id.v1"
	schemaDomainTag             = "maiden-lane.schema.v1"
	stateDomainTag              = "maiden-lane.state.v1"
	worldDomainTag              = "maiden-lane.world.v1"
	rulesetDomainTag            = "maiden-lane.ruleset.v1"
	compilationInputDomainTag   = "maiden-lane.compilation-input.v1"
	planDomainTag               = "maiden-lane.plan.v1"
	compiledProfileDomainTag    = "maiden-lane.compiled-profile.v1"
	compilationFailureDomainTag = "maiden-lane.compilation-failure.v1"
	patchDomainTag              = "maiden-lane.patch.v1"
	provenancePolicyDomainTag   = "maiden-lane.provenance-policy.v1"
	inputIdentityDomainTag      = "maiden-lane.input-id.v1"
	semanticRunDomainTag        = "maiden-lane.semantic-run-id.v1"
	executorIdentityDomainTag   = "maiden-lane.executor-identity.v1"
	executionIdentityDomainTag  = "maiden-lane.execution-id.v1"
	syntheticEntityDomainTag    = "maiden-lane.synthetic-entity.v1"
	invariantResultsDomainTag   = "maiden-lane.invariant-results.v1"
	journalEntryDomainTag       = "maiden-lane.journal-entry.v1"
	journalPrefixDomainTag      = "maiden-lane.journal-prefix.v1"
	expressionDomainTag         = "maiden-lane.expression.v1"
	protectedFailureDomainTag   = "maiden-lane.protected-invariant-failure.v1"
	artifactFailureDomainTag    = "maiden-lane.artifact-integrity-failure.v1"
	checkpointIDDomainTag       = "maiden-lane.checkpoint-id.v1"
	checkpointClaimDomainTag    = "maiden-lane.checkpoint-artifact-id.v1"
	checkpointArtifactDomainTag = "maiden-lane.checkpoint-artifact.v1"
	assessmentIDDomainTag       = "maiden-lane.assessment-id.v1"
	assessmentDomainTag         = "maiden-lane.readiness-assessment.v1"
	corpusDomainTag             = "maiden-lane.replay-corpus.v1"
	comparisonPolicyDomainTag   = "maiden-lane.comparison-policy.v1"
	comparisonIDDomainTag       = "maiden-lane.comparison-id.v1"
)

// assessmentSemanticsVersion pins the meaning of readiness evaluation (closed
// scope selection, universal aggregation, field-presence atoms, vacuous-empty
// readiness) independently of the byte-format version carried by the domain
// tag. Changing evaluation semantics must change this token.
const assessmentSemanticsVersion = "maiden-lane.assessment-semantics.v1"

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

// fail poisons the encoder, so an exhaustive switch can refuse an unrecognised case instead
// of writing something plausible. Without it a default arm has no way to say "no".
func (e *canonicalEncoder) fail(err error) {
	if e.err == nil {
		e.err = err
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
		fieldNames := sortedFieldNames(entity.fields)
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

func encodePatch(schema SchemaDigest, operations []Operation) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(patchDomainTag)
	encoder.digest(string(schema))
	encoder.uint64(uint64(len(operations)))
	for _, operation := range operations {
		encoder.byte(byte(operation.kind))
		encodeOperationPayload(&encoder, operation)
	}
	return encoder.bytes()
}

func encodeOperationPayloadBytes(operation Operation) ([]byte, error) {
	var encoder canonicalEncoder
	encodeOperationPayload(&encoder, operation)
	return encoder.bytes()
}

func encodeOperationPayload(encoder *canonicalEncoder, operation Operation) {
	switch operation.kind {
	case OperationInsert:
		encodeEntity(encoder, operation.insert.entity)
	case OperationRelate:
		encodeRelation(encoder, operation.relate.relation)
	case OperationUpdate:
		encodeEntityRef(encoder, operation.update.target)
		encoder.uint64(uint64(len(operation.update.fields)))
		for _, change := range operation.update.fields {
			encoder.string(string(change.Name))
			encoder.optional(change.Before.present, func() {
				encoder.value(change.Before.value)
			})
			encoder.value(change.After)
		}
	default:
		if encoder.err == nil {
			encoder.err = fmt.Errorf("unknown operation kind %d", operation.kind)
		}
	}
}

func encodeEntity(encoder *canonicalEncoder, entity Entity) {
	encodeEntityRef(encoder, entity.ref)
	fieldNames := sortedFieldNames(entity.fields)
	encoder.uint64(uint64(len(fieldNames)))
	for _, name := range fieldNames {
		encoder.string(string(name))
		encoder.value(entity.fields[name])
	}
}

func encodeEntityRef(encoder *canonicalEncoder, ref EntityRef) {
	encoder.string(string(ref.Kind))
	encoder.digest(string(ref.ID))
}

func encodeRelation(encoder *canonicalEncoder, relation Relation) {
	encoder.string(string(relation.Kind))
	encodeEntityRef(encoder, relation.From)
	encodeEntityRef(encoder, relation.To)
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

func sortedFieldNames(fields map[FieldName]Value) []FieldName {
	names := make([]FieldName, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func encodeRuleset(rules normalizedRuleset) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(rulesetDomainTag)
	encoder.uint64(uint64(len(rules.transformations)))
	for _, transformation := range rules.transformations {
		encodeTransformationDeclaration(&encoder, transformation)
	}
	invariants := make([]InvariantDeclaration, 0)
	for _, transformation := range rules.transformations {
		switch transformation.Operator {
		case OperatorFormRelatedEntity:
			if transformation.Form != nil {
				invariants = append(invariants, formInvariants(transformation.ID, transformation.Form.GroupingField)...)
			}
		case OperatorAggregateRelatedFields:
			if transformation.Aggregate != nil {
				invariants = append(invariants, aggregateInvariants(transformation.ID, transformation.Aggregate)...)
			}
		}
	}
	sort.Slice(invariants, func(i, j int) bool { return invariants[i].key < invariants[j].key })
	encoder.uint64(uint64(len(invariants)))
	for _, invariant := range invariants {
		encodeInvariantDeclaration(&encoder, invariant)
	}
	encodeCheckpoints(&encoder, rules.checkpoints)
	return encoder.bytes()
}

func encodeCompilationInput(schema SchemaDigest, rules RulesetDigest, profiles []ProfileDeclaration, version CompilerSemanticsVersion) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(compilationInputDomainTag)
	encoder.digest(string(schema))
	encoder.digest(string(rules))
	encoder.string(string(version))
	encoder.uint64(uint64(len(profiles)))
	for _, profile := range profiles {
		encodeProfileDeclaration(&encoder, profile)
	}
	return encoder.bytes()
}

func encodePlan(schema SchemaDigest, rules RulesetDigest, version CompilerSemanticsVersion, transformations []CompiledTransformation, checkpoints []CheckpointDeclaration) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(planDomainTag)
	encoder.digest(string(schema))
	encoder.digest(string(rules))
	encoder.string(string(version))
	encoder.uint64(uint64(len(transformations)))
	for _, transformation := range transformations {
		encodeTransformationDeclaration(&encoder, transformation.declaration)
		encodeFieldPaths(&encoder, transformation.reads)
		encodeFieldPaths(&encoder, transformation.writes)
		encoder.uint64(uint64(len(transformation.accesses)))
		for _, access := range transformation.accesses {
			encoder.byte(byte(access.Kind))
			encoder.byte(byte(access.Mode))
			encoder.string(string(access.EntityKind))
			encoder.string(string(access.RelationKind))
			encoder.string(string(access.Field))
		}
		encoder.uint64(uint64(len(transformation.dependencies)))
		for _, dependency := range transformation.dependencies {
			encoder.string(string(dependency))
		}
		encoder.uint64(transformation.level)
		encoder.uint64(uint64(len(transformation.invariants)))
		for _, invariant := range transformation.invariants {
			encodeInvariantDeclaration(&encoder, invariant)
		}
	}
	encodeCheckpoints(&encoder, checkpoints)
	encoder.string("changes.v1")
	return encoder.bytes()
}

func encodeInvariantDeclaration(encoder *canonicalEncoder, invariant InvariantDeclaration) {
	encoder.string(invariant.key)
	encoder.string(string(invariant.code))
	encoder.byte(byte(invariant.scope))
	encodeFieldPaths(encoder, invariant.reads)
	encoder.string(string(invariant.appliesAfter))
}

func encodeCompiledProfile(profile CompiledProfile) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(compiledProfileDomainTag)
	encoder.string(string(profile.compilerVersion))
	encoder.digest(string(profile.schemaDigest))
	encodeProfileDeclaration(&encoder, profile.declaration)
	encoder.uint64(uint64(len(profile.proofs)))
	for _, proof := range profile.proofs {
		encoder.string(string(proof.target))
		encoder.byte(byte(proof.kind))
	}
	return encoder.bytes()
}

func encodeCompilationFailure(input CompilationInputDigest, diagnostics []CompilationDiagnostic) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(compilationFailureDomainTag)
	encoder.digest(string(input))
	encoder.string("INVALID_PLAN")
	encoder.uint64(uint64(len(diagnostics)))
	for _, diagnostic := range diagnostics {
		encoder.string(string(diagnostic.code))
		encoder.string(diagnostic.subject)
		encoder.string(diagnostic.detail)
	}
	return encoder.bytes()
}

func encodeTransformationDeclaration(encoder *canonicalEncoder, transformation TransformationDeclaration) {
	encoder.string(string(transformation.ID))
	encoder.byte(byte(transformation.Operator))
	encodeFieldPaths(encoder, transformation.DeclaredReads)
	encodeFieldPaths(encoder, transformation.DeclaredWrites)
	encoder.uint64(uint64(len(transformation.After)))
	for _, dependency := range transformation.After {
		encoder.string(string(dependency))
	}
	encoder.optional(transformation.Form != nil, func() {
		form := transformation.Form
		encoder.string(string(form.SourceKind))
		encoder.uint64(uint64(len(form.Sources)))
		for _, source := range form.Sources {
			encoder.string(string(source.Kind))
			encoder.string(source.CanonicalSourceKey)
		}
		encoder.string(string(form.OutputKind))
		encoder.string(string(form.OutputSlot))
		encoder.string(string(form.GroupingField))
		encoder.uint64(form.SourceCount)
		encoder.uint64(uint64(len(form.CopiedFields)))
		for _, copied := range form.CopiedFields {
			encoder.string(string(copied.Source))
			encoder.string(string(copied.Destination))
		}
		encoder.string(string(form.RelationKind))
		encoder.optional(form.OutputKey != nil, func() {
			encoder.byte(byte(form.OutputKey.Kind))
			encoder.string(string(form.OutputKey.Field))
		})
	})
	encoder.optional(transformation.Aggregate != nil, func() {
		aggregate := transformation.Aggregate
		encoder.string(string(aggregate.Target.Rule))
		encoder.string(string(aggregate.Target.Slot))
		encoder.string(string(aggregate.RelationKind))
		encoder.string(string(aggregate.SourceKind))
		encodeFieldPaths(encoder, aggregate.RequiredSourceTuple)
		encodePredicates(encoder, aggregate.Predicates)
		encoder.string(string(aggregate.Anchor.Source))
		encoder.string(string(aggregate.Anchor.Destination))
		encoder.uint64(uint64(len(aggregate.Reductions)))
		for _, reduction := range aggregate.Reductions {
			encoder.byte(byte(reduction.Kind))
			encoder.string(string(reduction.Source))
			encoder.string(string(reduction.Destination))
		}
		encodePredicates(encoder, aggregate.ResultPredicates)
	})
}

func encodePredicates(encoder *canonicalEncoder, predicates []AggregatePredicate) {
	encoder.uint64(uint64(len(predicates)))
	for _, predicate := range predicates {
		encoder.byte(byte(predicate.Kind))
		encodeFieldPaths(encoder, predicate.Fields)
	}
}

func encodeFieldPaths(encoder *canonicalEncoder, fields []FieldPath) {
	encoder.uint64(uint64(len(fields)))
	for _, field := range fields {
		encoder.string(string(field))
	}
}

func encodeCheckpoints(encoder *canonicalEncoder, checkpoints []CheckpointDeclaration) {
	encoder.uint64(uint64(len(checkpoints)))
	for _, checkpoint := range checkpoints {
		encoder.string(string(checkpoint.Key))
		encoder.string(string(checkpoint.After))
	}
}

func encodeProfileDeclaration(encoder *canonicalEncoder, profile ProfileDeclaration) {
	encoder.string(string(profile.Key))
	encoder.byte(byte(profile.Scope.Kind))
	encoder.string(string(profile.Scope.EntityKind))
	encoder.byte(byte(profile.Aggregation))
	encoder.uint64(uint64(len(profile.Requirements)))
	for _, requirement := range profile.Requirements {
		encoder.string(string(requirement.Code))
		encoder.byte(byte(requirement.Kind))
		encoder.string(string(requirement.Field))
	}
	encoder.uint64(uint64(len(profile.Implies)))
	for _, target := range profile.Implies {
		encoder.string(string(target))
	}
}

func encodeProvenancePolicy(policy ProvenancePolicy) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(provenancePolicyDomainTag)
	if policy != ChangesProvenance {
		return nil, fmt.Errorf("unknown provenance policy %d", policy)
	}
	encoder.string("changes.v1")
	return encoder.bytes()
}

func encodeInputIdentity(state StateDigest, world WorldID) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(inputIdentityDomainTag)
	encoder.digest(string(state))
	encoder.digest(string(world))
	return encoder.bytes()
}

func encodeSemanticRunIdentity(input InputID, plan PlanID) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(semanticRunDomainTag)
	encoder.digest(string(input))
	encoder.digest(string(plan))
	return encoder.bytes()
}

func encodeExecutorIdentity(identity ExecutorIdentity) ([]byte, error) {
	if !validExecutorIdentity(identity) {
		return nil, fmt.Errorf("invalid executor identity")
	}
	var encoder canonicalEncoder
	encoder.tag(executorIdentityDomainTag)
	encoder.string(identity.backend)
	encoder.digest(string(identity.version))
	return encoder.bytes()
}

func encodeExecutionIdentity(run SemanticRunID, executor ExecutorIdentity, policy ProvenancePolicyID) ([]byte, error) {
	identity, err := encodeExecutorIdentity(executor)
	if err != nil {
		return nil, err
	}
	var encoder canonicalEncoder
	encoder.tag(executionIdentityDomainTag)
	encoder.digest(string(run))
	encoder.uint64(uint64(len(identity)))
	if encoder.err == nil {
		_, encoder.err = encoder.buffer.Write(identity)
	}
	encoder.digest(string(policy))
	return encoder.bytes()
}

func encodeSyntheticEntityIdentity(lineage InputLineageID, kind EntityKind, rule RuleID, progenitors []EntityRef, outputKey Value) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(syntheticEntityDomainTag)
	encoder.digest(string(lineage))
	encoder.string(string(kind))
	encoder.string(string(rule))
	encoder.uint64(uint64(len(progenitors)))
	for _, progenitor := range progenitors {
		encoder.digest(string(progenitor.ID))
	}
	encoder.value(outputKey)
	return encoder.bytes()
}

func encodeInvariantResults(results []InvariantResult) ([]byte, error) {
	normalized := cloneInvariantResults(results)
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].declarationKey < normalized[j].declarationKey })
	for i := 1; i < len(normalized); i++ {
		if normalized[i-1].declarationKey == normalized[i].declarationKey {
			return nil, fmt.Errorf("duplicate invariant result declaration key")
		}
	}
	var encoder canonicalEncoder
	encoder.tag(invariantResultsDomainTag)
	encoder.uint64(uint64(len(normalized)))
	for _, result := range normalized {
		encodeInvariantResult(&encoder, result)
	}
	return encoder.bytes()
}

func encodeInvariantResult(encoder *canonicalEncoder, result InvariantResult) {
	encoder.string(result.declarationKey)
	encoder.byte(byte(result.scope))
	encoder.string(string(result.boundary))
	encoder.optional(result.passed, func() {})
	encoder.string(string(result.code))
	encoder.uint64(uint64(len(result.entities)))
	for _, ref := range result.entities {
		encodeEntityRef(encoder, ref)
	}
	encoder.uint64(uint64(len(result.facts)))
	for _, fact := range result.facts {
		encodeEntityRef(encoder, fact.entity)
		encoder.string(string(fact.field))
	}
}

func encodeJournalEntry(entry JournalEntry) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(journalEntryDomainTag)
	encoder.string(string(entry.rule))
	encoder.digest(string(entry.predecessor))
	encoder.digest(string(entry.result))
	encoder.digest(string(entry.patch.digest))
	encoder.uint64(uint64(len(entry.evidence)))
	for _, fact := range entry.evidence {
		encodeEntityRef(&encoder, fact.entity)
		encoder.string(string(fact.field))
	}
	results, err := encodeInvariantResults(entry.invariantResults)
	if err != nil {
		return nil, err
	}
	encoder.uint64(uint64(len(results)))
	if encoder.err == nil {
		_, encoder.err = encoder.buffer.Write(results)
	}
	return encoder.bytes()
}

func encodeJournalPrefix(run SemanticRunID, policy ProvenancePolicyID, entries []JournalEntry) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(journalPrefixDomainTag)
	encoder.digest(string(run))
	encoder.digest(string(policy))
	encoder.uint64(uint64(len(entries)))
	for _, entry := range entries {
		encoder.digest(string(entry.digest))
	}
	return encoder.bytes()
}

func encodeCheckpointID(plan PlanID, checkpoint CheckpointKey) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(checkpointIDDomainTag)
	encoder.digest(string(plan))
	encoder.string(string(checkpoint))
	return encoder.bytes()
}

func encodeCheckpointArtifactID(run SemanticRunID, checkpoint CheckpointID, state StateDigest, journal JournalPrefixDigest, invariants InvariantResultDigest, policy ProvenancePolicyID) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(checkpointClaimDomainTag)
	encoder.digest(string(run))
	encoder.digest(string(checkpoint))
	encoder.digest(string(state))
	encoder.digest(string(journal))
	encoder.digest(string(invariants))
	encoder.digest(string(policy))
	return encoder.bytes()
}

func encodeCheckpointArtifact(artifact CheckpointArtifact) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(checkpointArtifactDomainTag)
	encoder.digest(string(artifact.planID))
	encoder.digest(string(artifact.checkpointID))
	encoder.string(string(artifact.checkpoint.Key))
	encoder.string(string(artifact.checkpoint.After))
	encoder.digest(string(artifact.semanticRunID))
	encoder.digest(string(artifact.inputID))
	encoder.digest(string(artifact.initialStateDigest))
	encoder.digest(string(artifact.worldID))
	encoder.digest(string(artifact.policyID))
	encoder.digest(string(artifact.stateDigest))
	encoder.digest(string(artifact.journalPrefixDigest))
	encoder.digest(string(artifact.invariantResultDigest))
	return encoder.bytes()
}

func encodeAssessmentID(checkpoint CheckpointArtifactID, profile ProfileID) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(assessmentIDDomainTag)
	encoder.digest(string(checkpoint))
	encoder.digest(string(profile))
	return encoder.bytes()
}

func encodeAssessment(assessment Assessment) ([]byte, error) {
	if !validReadinessVerdict(assessment.verdict) {
		return nil, fmt.Errorf("unknown readiness verdict %q", assessment.verdict)
	}
	var encoder canonicalEncoder
	encoder.tag(assessmentDomainTag)
	encoder.string(assessmentSemanticsVersion)
	encoder.digest(string(assessment.checkpointArtifactID))
	encoder.digest(string(assessment.profileID))
	encoder.string(string(assessment.verdict))
	encoder.uint64(uint64(len(assessment.entities)))
	for _, entity := range assessment.entities {
		encodeEntityRef(&encoder, entity.entity)
		encoder.uint64(uint64(len(entity.results)))
		for _, result := range entity.results {
			if !validRequirementResultKind(result.result) {
				return nil, fmt.Errorf("unknown requirement result kind %d", result.result)
			}
			encoder.string(string(result.code))
			encoder.byte(byte(result.result))
			encoder.uint64(uint64(len(result.facts)))
			for _, fact := range result.facts {
				encodeEntityRef(&encoder, fact.entity)
				encoder.string(string(fact.field))
			}
		}
	}
	// Assessment-level safe evidence references are part of the ratified v1
	// record shape but no v1 evaluation produces any, so the count is always
	// zero. A future producer would fill this list without a format change.
	encoder.uint64(0)
	return encoder.bytes()
}

func encodeProtectedFailure(report ProtectedInvariantFailureReport) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(protectedFailureDomainTag)
	encoder.digest(string(report.semanticRunID))
	encoder.string(string(ProtectedInvariantFailed))
	encoder.byte(byte(report.codeKind))
	if report.codeKind == protectedOperationCode {
		encoder.string(string(report.operationCode))
	} else {
		encoder.string(string(report.invariantCode))
	}
	encoder.string(string(report.rule))
	encoder.digest(string(report.predecessor))
	results, err := encodeInvariantResults(report.results)
	if err != nil {
		return nil, err
	}
	encoder.uint64(uint64(len(results)))
	if encoder.err == nil {
		_, encoder.err = encoder.buffer.Write(results)
	}
	encoder.uint64(uint64(len(report.entities)))
	for _, ref := range report.entities {
		encodeEntityRef(&encoder, ref)
	}
	encoder.uint64(uint64(len(report.invariantRefs)))
	for _, ref := range report.invariantRefs {
		encoder.string(ref.declarationKey)
	}
	encoder.uint64(uint64(len(report.facts)))
	for _, fact := range report.facts {
		encodeEntityRef(&encoder, fact.entity)
		encoder.string(string(fact.field))
	}
	encoder.optional(report.patchDigest != nil, func() { encoder.digest(string(*report.patchDigest)) })
	return encoder.bytes()
}

func encodeArtifactIntegrityFailure(report ArtifactIntegrityFailureReport) ([]byte, error) {
	var encoder canonicalEncoder
	encoder.tag(artifactFailureDomainTag)
	encoder.digest(string(report.semanticRunID))
	encoder.string(string(ArtifactIntegrityFailed))
	encoder.string(string(report.code))
	encoder.byte(byte(report.artifactKind))
	encodeArtifactRef(&encoder, report.artifact)
	encoder.optional(report.lastState != nil, func() { encoder.digest(string(*report.lastState)) })
	encoder.optional(report.lastCheckpoint != nil, func() { encoder.digest(string(*report.lastCheckpoint)) })
	encoder.optional(report.expected != nil, func() { encoder.digest(string(*report.expected)) })
	encoder.optional(report.observed != nil, func() { encoder.digest(string(*report.observed)) })
	encoder.uint64(uint64(len(report.references)))
	for _, reference := range report.references {
		encodeArtifactRef(&encoder, reference)
	}
	return encoder.bytes()
}

func encodeArtifactRef(encoder *canonicalEncoder, reference ArtifactRef) {
	encoder.byte(byte(reference.kind))
	encoder.digest(string(reference.digest))
}
