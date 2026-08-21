package semantic

import (
	"bytes"
	"fmt"
	"slices"
	"sort"
)

// OperationKind is the complete structural-operation subset supported by the
// walking skeleton. Its numeric order is also the canonical operation rank.
type OperationKind uint8

const (
	OperationInsert OperationKind = iota + 1
	OperationRelate
	OperationUpdate
	OperationUnrelate
	OperationDelete
)

// OperationInvariantCode is the closed patch-legality vocabulary ratified for
// the walking skeleton.
type OperationInvariantCode string

const (
	OperationEntityIdentityCollision OperationInvariantCode = "OP_ENTITY_IDENTITY_COLLISION"
	OperationUpdateTargetNotFound    OperationInvariantCode = "OP_UPDATE_TARGET_NOT_FOUND"
	OperationDeleteTargetNotFound    OperationInvariantCode = "OP_DELETE_TARGET_NOT_FOUND"
	OperationBeforeImageMismatch     OperationInvariantCode = "OP_BEFORE_IMAGE_MISMATCH"
	OperationRelationAlreadyPresent  OperationInvariantCode = "OP_RELATION_ALREADY_PRESENT"
	OperationRelationEndpointMissing OperationInvariantCode = "OP_RELATION_ENDPOINT_MISSING"
	OperationRelationNotFound        OperationInvariantCode = "OP_RELATION_NOT_FOUND"
	OperationDanglingRelation        OperationInvariantCode = "OP_DANGLING_RELATION"
)

// OperationFailure is a deterministic rejection of one operation in an
// otherwise materialized patch. Rejections never expose the staged candidate.
type OperationFailure struct {
	code OperationInvariantCode
}

// Code returns the exact closed operation-invariant code.
func (f OperationFailure) Code() OperationInvariantCode { return f.code }

// FieldImage distinguishes an absent field from a present typed scalar.
type FieldImage struct {
	present bool
	value   Value
}

// AbsentField constructs an explicit absent before-image.
func AbsentField() FieldImage { return FieldImage{} }

// PresentField constructs a present before-image. NewPatch rejects an invalid
// scalar if a zero or otherwise malformed Value is supplied.
func PresentField(value Value) FieldImage { return FieldImage{present: true, value: value} }

// Present reports whether this image contains a scalar value.
func (i FieldImage) Present() bool { return i.present }

// Value returns the scalar and its presence marker.
func (i FieldImage) Value() (Value, bool) { return i.value, i.present }

// FieldUpdate records the exact expected image and emitted value for one
// field. Fields not named by the update remain unchanged.
type FieldUpdate struct {
	Name   FieldName
	Before FieldImage
	After  Value
}

// Insert is the immutable payload of an insert operation.
type Insert struct {
	entity Entity
}

// Entity returns a defensive copy of the inserted entity.
func (i Insert) Entity() Entity { return cloneEntity(i.entity) }

// Relate is the immutable payload of a relation operation.
type Relate struct {
	relation Relation
}

// Relation returns the complete directed relation value.
func (r Relate) Relation() Relation { return r.relation }

// Update is the immutable payload of an update operation.
type Update struct {
	target EntityRef
	fields []FieldUpdate
}

// Target returns the entity whose fields are updated.
func (u Update) Target() EntityRef { return u.target }

// Fields returns a defensive copy of the exact field changes.
func (u Update) Fields() []FieldUpdate { return slices.Clone(u.fields) }

// Delete is the immutable payload of a delete operation.
type Delete struct {
	entity Entity
}

// Entity returns a defensive copy of the entity being deleted.
func (d Delete) Entity() Entity { return cloneEntity(d.entity) }

// Unrelate is the immutable payload of an unrelate operation.
type Unrelate struct {
	relation Relation
}

// Relation returns the complete directed relation value being removed.
func (u Unrelate) Relation() Relation { return u.relation }

// Operation is the closed tagged union of structural operations.
type Operation struct {
	kind     OperationKind
	insert   *Insert
	relate   *Relate
	update   *Update
	delete   *Delete
	unrelate *Unrelate
}

// InsertOperation constructs an insert operation. NewPatch performs complete
// validation before the operation enters a canonical patch.
func InsertOperation(entity Entity) Operation {
	return Operation{kind: OperationInsert, insert: &Insert{entity: cloneEntity(entity)}}
}

// RelateOperation constructs a directed relation operation.
func RelateOperation(relation Relation) Operation {
	return Operation{kind: OperationRelate, relate: &Relate{relation: relation}}
}

// UpdateOperation constructs an update with exact field before-images and
// present after-values. NewPatch clones and validates the supplied slice.
func UpdateOperation(target EntityRef, fields []FieldUpdate) Operation {
	return Operation{kind: OperationUpdate, update: &Update{target: target, fields: slices.Clone(fields)}}
}

// DeleteOperation constructs a delete operation with complete entity before-image.
func DeleteOperation(entity Entity) Operation {
	return Operation{kind: OperationDelete, delete: &Delete{entity: cloneEntity(entity)}}
}

// UnrelateOperation constructs an unrelate operation.
func UnrelateOperation(relation Relation) Operation {
	return Operation{kind: OperationUnrelate, unrelate: &Unrelate{relation: relation}}
}

// Kind returns the closed operation variant.
func (o Operation) Kind() OperationKind { return o.kind }

// Insert returns a defensive copy of the insert payload when this is an
// insert operation.
func (o Operation) Insert() (Insert, bool) {
	if o.kind != OperationInsert || o.insert == nil {
		return Insert{}, false
	}
	return Insert{entity: cloneEntity(o.insert.entity)}, true
}

// Relate returns the relation payload when this is a relate operation.
func (o Operation) Relate() (Relate, bool) {
	if o.kind != OperationRelate || o.relate == nil {
		return Relate{}, false
	}
	return *o.relate, true
}

// Update returns a defensive copy of the update payload when this is an
// update operation.
func (o Operation) Update() (Update, bool) {
	if o.kind != OperationUpdate || o.update == nil {
		return Update{}, false
	}
	return Update{target: o.update.target, fields: slices.Clone(o.update.fields)}, true
}

// Delete returns a defensive copy of the delete payload when this is a
// delete operation.
func (o Operation) Delete() (Delete, bool) {
	if o.kind != OperationDelete || o.delete == nil {
		return Delete{}, false
	}
	return Delete{entity: cloneEntity(o.delete.entity)}, true
}

// Unrelate returns the relation payload when this is an unrelate operation.
func (o Operation) Unrelate() (Unrelate, bool) {
	if o.kind != OperationUnrelate || o.unrelate == nil {
		return Unrelate{}, false
	}
	return *o.unrelate, true
}

// Patch is an immutable, content-addressed atomic structural proposal.
type Patch struct {
	schemaDigest SchemaDigest
	operations   []Operation
	canonical    []byte
	digest       PatchDigest
}

// NewPatch validates and defensively copies the supported operation subset.
func NewPatch(schema Schema, operations []Operation) (Patch, error) {
	if len(schema.canonical) == 0 {
		return Patch{}, fmt.Errorf("patch schema is not initialized")
	}
	if len(operations) == 0 {
		return Patch{}, fmt.Errorf("patch contains no operations")
	}
	normalized := cloneOperations(operations)
	for index := range normalized {
		if normalized[index].update != nil {
			slices.SortFunc(normalized[index].update.fields, func(a, b FieldUpdate) int {
				return compare(string(a.Name), string(b.Name))
			})
		}
	}
	for index := range normalized {
		if err := validateOperation(normalized[index]); err != nil {
			return Patch{}, fmt.Errorf("operation %d: %w", index, err)
		}
		if err := validateOperationAgainstSchema(schema, normalized[index]); err != nil {
			return Patch{}, fmt.Errorf("operation %d: %w", index, err)
		}
	}
	sort.Slice(normalized, func(i, j int) bool {
		return compareOperations(normalized[i], normalized[j]) < 0
	})
	canonical, err := encodePatch(schema.Digest(), normalized)
	if err != nil {
		return Patch{}, fmt.Errorf("canonicalize patch: %w", err)
	}
	return Patch{
		schemaDigest: schema.Digest(),
		operations:   normalized,
		canonical:    canonical,
		digest:       PatchDigest(canonicalDigest(canonical)),
	}, nil
}

// SchemaDigest returns the immutable schema identity against which every
// operation was validated.
func (p Patch) SchemaDigest() SchemaDigest { return p.schemaDigest }

// Operations returns defensive copies in canonical patch order.
func (p Patch) Operations() []Operation { return cloneOperations(p.operations) }

// CanonicalBytes returns a copy of the complete v1 patch bytes.
func (p Patch) CanonicalBytes() []byte { return bytes.Clone(p.canonical) }

// Digest returns the content identity of the complete atomic patch.
func (p Patch) Digest() PatchDigest { return p.digest }

// AcceptedPatchReceipt is immutable proof that one exact patch committed from
// one predecessor digest to one result digest. Only ApplyPatch can construct a
// non-zero accepted receipt.
type AcceptedPatchReceipt struct {
	accepted          bool
	patchDigest       PatchDigest
	predecessorDigest StateDigest
	resultDigest      StateDigest
}

// PatchDigest returns the patch committed by this receipt.
func (r AcceptedPatchReceipt) PatchDigest() PatchDigest { return r.patchDigest }

// PredecessorStateDigest returns the authoritative state before application.
func (r AcceptedPatchReceipt) PredecessorStateDigest() StateDigest { return r.predecessorDigest }

// ResultStateDigest returns the authoritative accepted result state.
func (r AcceptedPatchReceipt) ResultStateDigest() StateDigest { return r.resultDigest }

// PatchOutcome separates an immutable state frontier from an optional
// protected rejection and the success-only accepted-application receipt.
type PatchOutcome struct {
	state   State
	failure *OperationFailure
	receipt *AcceptedPatchReceipt
}

// State returns the accepted result on success or the exact input state on any
// protected rejection or Go error.
func (o PatchOutcome) State() State { return o.state }

// Failure returns a defensive copy of the protected operation rejection.
func (o PatchOutcome) Failure() *OperationFailure {
	if o.failure == nil {
		return nil
	}
	result := *o.failure
	return &result
}

// Receipt returns immutable accepted-application evidence only after success.
func (o PatchOutcome) Receipt() (AcceptedPatchReceipt, bool) {
	if o.receipt == nil {
		return AcceptedPatchReceipt{}, false
	}
	return *o.receipt, true
}

// ApplyPatch validates operations against an isolated candidate and returns it
// only after every operation and final graph reconstruction succeeds.
func ApplyPatch(predecessor State, patch Patch) (PatchOutcome, error) {
	base := PatchOutcome{state: predecessor}
	if err := validateStatePatchLink(predecessor, patch); err != nil {
		return base, err
	}
	entities := predecessor.Entities()
	relations := predecessor.Relations()

	for _, operation := range patch.operations {
		switch operation.kind {
		case OperationInsert:
			entity := operation.insert.entity
			if findEntityIndex(entities, entity.ref) >= 0 {
				return rejectedPatchOutcome(predecessor, OperationEntityIdentityCollision), nil
			}
			entities = append(entities, cloneEntity(entity))
		case OperationRelate:
			relation := operation.relate.relation
			if slices.Contains(relations, relation) {
				return rejectedPatchOutcome(predecessor, OperationRelationAlreadyPresent), nil
			}
			if findEntityIndex(entities, relation.From) < 0 || findEntityIndex(entities, relation.To) < 0 {
				return rejectedPatchOutcome(predecessor, OperationRelationEndpointMissing), nil
			}
			relations = append(relations, relation)
		case OperationUpdate:
			index := findEntityIndex(entities, operation.update.target)
			if index < 0 {
				return rejectedPatchOutcome(predecessor, OperationUpdateTargetNotFound), nil
			}
			fields := cloneFields(entities[index].fields)
			for _, change := range operation.update.fields {
				actual, present := fields[change.Name]
				if present != change.Before.present || (present && !actual.Equal(change.Before.value)) {
					return rejectedPatchOutcome(predecessor, OperationBeforeImageMismatch), nil
				}
			}
			for _, change := range operation.update.fields {
				fields[change.Name] = change.After
			}
			entities[index] = Entity{ref: entities[index].ref, fields: fields}
		case OperationDelete:
			deleted := operation.delete.entity
			index := findEntityIndex(entities, deleted.ref)
			if index < 0 {
				return rejectedPatchOutcome(predecessor, OperationDeleteTargetNotFound), nil
			}
			if !entitiesEqual(entities[index], deleted) {
				return rejectedPatchOutcome(predecessor, OperationBeforeImageMismatch), nil
			}
			entities = slices.Delete(entities, index, index+1)
		case OperationUnrelate:
			relation := operation.unrelate.relation
			index := slices.Index(relations, relation)
			if index < 0 {
				return rejectedPatchOutcome(predecessor, OperationRelationNotFound), nil
			}
			relations = slices.Delete(relations, index, index+1)
		default:
			return base, fmt.Errorf("apply patch: unknown operation kind %d", operation.kind)
		}
	}

	for _, rel := range relations {
		if findEntityIndex(entities, rel.From) < 0 || findEntityIndex(entities, rel.To) < 0 {
			return rejectedPatchOutcome(predecessor, OperationDanglingRelation), nil
		}
	}

	candidate, err := NewState(predecessor.Schema(), predecessor.InputLineageID(), entities, relations)
	if err != nil {
		return base, fmt.Errorf("apply patch candidate: %w", err)
	}
	receipt := acceptedPatchReceipt(patch.Digest(), predecessor.Digest(), candidate.Digest())
	return PatchOutcome{state: candidate, receipt: &receipt}, nil
}

// UndoPatch verifies the accepted after-image and applies the inverse in
// reverse canonical operation order. A mismatch returns the supplied current
// state, never a partially undone candidate.
func UndoPatch(current State, patch Patch, receipt AcceptedPatchReceipt) (PatchOutcome, error) {
	base := PatchOutcome{state: current}
	if err := validateStatePatchLink(current, patch); err != nil {
		return base, err
	}
	if err := validateAcceptedReceipt(current, patch, receipt); err != nil {
		return base, err
	}
	entities := current.Entities()
	relations := current.Relations()

	for operationIndex := len(patch.operations) - 1; operationIndex >= 0; operationIndex-- {
		operation := patch.operations[operationIndex]
		switch operation.kind {
		case OperationUpdate:
			index := findEntityIndex(entities, operation.update.target)
			if index < 0 {
				return rejectedPatchOutcome(current, OperationUpdateTargetNotFound), nil
			}
			fields := cloneFields(entities[index].fields)
			for _, change := range operation.update.fields {
				actual, present := fields[change.Name]
				if !present || !actual.Equal(change.After) {
					return rejectedPatchOutcome(current, OperationBeforeImageMismatch), nil
				}
			}
			for _, change := range operation.update.fields {
				if change.Before.present {
					fields[change.Name] = change.Before.value
				} else {
					delete(fields, change.Name)
				}
			}
			entities[index] = Entity{ref: entities[index].ref, fields: fields}
		case OperationRelate:
			relation := operation.relate.relation
			index := slices.Index(relations, relation)
			if index < 0 {
				return rejectedPatchOutcome(current, OperationBeforeImageMismatch), nil
			}
			relations = slices.Delete(relations, index, index+1)
		case OperationInsert:
			inserted := operation.insert.entity
			index := findEntityIndex(entities, inserted.ref)
			if index < 0 || !entitiesEqual(entities[index], inserted) {
				return rejectedPatchOutcome(current, OperationBeforeImageMismatch), nil
			}
			for _, relation := range relations {
				if relation.From == inserted.ref || relation.To == inserted.ref {
					return rejectedPatchOutcome(current, OperationBeforeImageMismatch), nil
				}
			}
			entities = slices.Delete(entities, index, index+1)
		case OperationDelete:
			deleted := operation.delete.entity
			if findEntityIndex(entities, deleted.ref) >= 0 {
				return rejectedPatchOutcome(current, OperationBeforeImageMismatch), nil
			}
			entities = append(entities, cloneEntity(deleted))
		case OperationUnrelate:
			relation := operation.unrelate.relation
			if slices.Contains(relations, relation) {
				return rejectedPatchOutcome(current, OperationBeforeImageMismatch), nil
			}
			if findEntityIndex(entities, relation.From) < 0 || findEntityIndex(entities, relation.To) < 0 {
				return rejectedPatchOutcome(current, OperationBeforeImageMismatch), nil
			}
			relations = append(relations, relation)
		default:
			return base, fmt.Errorf("undo patch: unknown operation kind %d", operation.kind)
		}
	}

	predecessor, err := NewState(current.Schema(), current.InputLineageID(), entities, relations)
	if err != nil {
		return base, fmt.Errorf("undo patch predecessor: %w", err)
	}
	if predecessor.Digest() != receipt.predecessorDigest {
		return base, fmt.Errorf("undo patch: reconstructed predecessor digest does not match accepted receipt")
	}
	return PatchOutcome{state: predecessor}, nil
}

func acceptedPatchReceipt(patch PatchDigest, predecessor, result StateDigest) AcceptedPatchReceipt {
	return AcceptedPatchReceipt{accepted: true, patchDigest: patch, predecessorDigest: predecessor, resultDigest: result}
}

func rejectedPatchOutcome(state State, code OperationInvariantCode) PatchOutcome {
	return PatchOutcome{state: state, failure: operationFailure(code)}
}

func validateStatePatchLink(state State, patch Patch) error {
	if len(state.canonical) == 0 {
		return fmt.Errorf("patch state is not initialized")
	}
	if len(patch.canonical) == 0 || patch.digest == "" || patch.schemaDigest == "" {
		return fmt.Errorf("patch is not initialized")
	}
	if state.Schema().Digest() != patch.schemaDigest {
		return fmt.Errorf("patch schema does not match state schema")
	}
	return nil
}

func validateAcceptedReceipt(current State, patch Patch, receipt AcceptedPatchReceipt) error {
	if !receipt.accepted {
		return fmt.Errorf("undo patch requires an accepted-application receipt")
	}
	if _, err := decodeDigest(string(receipt.patchDigest)); err != nil {
		return fmt.Errorf("accepted receipt patch digest: %w", err)
	}
	if _, err := decodeDigest(string(receipt.predecessorDigest)); err != nil {
		return fmt.Errorf("accepted receipt predecessor digest: %w", err)
	}
	if _, err := decodeDigest(string(receipt.resultDigest)); err != nil {
		return fmt.Errorf("accepted receipt result digest: %w", err)
	}
	if receipt.patchDigest != patch.Digest() {
		return fmt.Errorf("accepted receipt patch digest does not match patch")
	}
	if receipt.resultDigest != current.Digest() {
		return fmt.Errorf("accepted receipt result digest does not match current state")
	}
	return nil
}

func operationFailure(code OperationInvariantCode) *OperationFailure {
	return &OperationFailure{code: code}
}

func findEntityIndex(entities []Entity, ref EntityRef) int {
	for index := range entities {
		if entities[index].ref == ref {
			return index
		}
	}
	return -1
}

func entitiesEqual(a, b Entity) bool {
	if a.ref != b.ref || len(a.fields) != len(b.fields) {
		return false
	}
	for name, value := range a.fields {
		other, ok := b.fields[name]
		if !ok || !value.Equal(other) {
			return false
		}
	}
	return true
}

func cloneOperations(input []Operation) []Operation {
	result := make([]Operation, len(input))
	for index, operation := range input {
		result[index] = Operation{kind: operation.kind}
		if operation.insert != nil {
			result[index].insert = &Insert{entity: cloneEntity(operation.insert.entity)}
		}
		if operation.relate != nil {
			relate := *operation.relate
			result[index].relate = &relate
		}
		if operation.update != nil {
			result[index].update = &Update{target: operation.update.target, fields: slices.Clone(operation.update.fields)}
		}
		if operation.delete != nil {
			result[index].delete = &Delete{entity: cloneEntity(operation.delete.entity)}
		}
		if operation.unrelate != nil {
			unrelate := *operation.unrelate
			result[index].unrelate = &unrelate
		}
	}
	return result
}

func validateOperation(operation Operation) error {
	switch operation.kind {
	case OperationInsert:
		if operation.insert == nil || operation.relate != nil || operation.update != nil || operation.delete != nil || operation.unrelate != nil {
			return fmt.Errorf("insert operation has invalid union payload")
		}
		entity := operation.insert.entity
		if _, err := NewEntity(entity.ref, entity.fields); err != nil {
			return fmt.Errorf("insert entity: %w", err)
		}
	case OperationRelate:
		if operation.insert != nil || operation.relate == nil || operation.update != nil || operation.delete != nil || operation.unrelate != nil {
			return fmt.Errorf("relate operation has invalid union payload")
		}
		relation := operation.relate.relation
		if !validSemanticName(string(relation.Kind)) {
			return fmt.Errorf("relation kind is empty or invalid UTF-8")
		}
		if err := validateEntityRef(relation.From); err != nil {
			return fmt.Errorf("relation from: %w", err)
		}
		if err := validateEntityRef(relation.To); err != nil {
			return fmt.Errorf("relation to: %w", err)
		}
	case OperationUpdate:
		if operation.insert != nil || operation.relate != nil || operation.update == nil || operation.delete != nil || operation.unrelate != nil {
			return fmt.Errorf("update operation has invalid union payload")
		}
		if err := validateEntityRef(operation.update.target); err != nil {
			return fmt.Errorf("update target: %w", err)
		}
		if len(operation.update.fields) == 0 {
			return fmt.Errorf("update contains no field changes")
		}
		seen := make(map[FieldName]struct{}, len(operation.update.fields))
		for _, change := range operation.update.fields {
			if !validSemanticName(string(change.Name)) {
				return fmt.Errorf("update field name is empty or invalid UTF-8")
			}
			if _, duplicate := seen[change.Name]; duplicate {
				return fmt.Errorf("duplicate update field %q", change.Name)
			}
			seen[change.Name] = struct{}{}
			if change.Before.present && !change.Before.value.Valid() {
				return fmt.Errorf("update field %q has invalid present before-image", change.Name)
			}
			if !change.After.Valid() {
				return fmt.Errorf("update field %q has invalid after-image", change.Name)
			}
		}
	case OperationDelete:
		if operation.insert != nil || operation.relate != nil || operation.update != nil || operation.delete == nil || operation.unrelate != nil {
			return fmt.Errorf("delete operation has invalid union payload")
		}
		entity := operation.delete.entity
		if _, err := NewEntity(entity.ref, entity.fields); err != nil {
			return fmt.Errorf("delete entity: %w", err)
		}
	case OperationUnrelate:
		if operation.insert != nil || operation.relate != nil || operation.update != nil || operation.delete != nil || operation.unrelate == nil {
			return fmt.Errorf("unrelate operation has invalid union payload")
		}
		relation := operation.unrelate.relation
		if !validSemanticName(string(relation.Kind)) {
			return fmt.Errorf("relation kind is empty or invalid UTF-8")
		}
		if err := validateEntityRef(relation.From); err != nil {
			return fmt.Errorf("unrelate relation from: %w", err)
		}
		if err := validateEntityRef(relation.To); err != nil {
			return fmt.Errorf("unrelate relation to: %w", err)
		}
	default:
		return fmt.Errorf("unknown operation kind %d", operation.kind)
	}
	return nil
}

func validateOperationAgainstSchema(schema Schema, operation Operation) error {
	switch operation.kind {
	case OperationInsert:
		declaration, ok := schema.entityDeclaration(operation.insert.entity.ref.Kind)
		if !ok {
			return fmt.Errorf("insert entity kind is not declared by patch schema")
		}
		if err := validateEntityFields(operation.insert.entity, declaration); err != nil {
			return fmt.Errorf("insert entity is incompatible with patch schema: %w", err)
		}
	case OperationRelate:
		relation := operation.relate.relation
		declaration, ok := schema.relationDeclaration(relation.Kind)
		if !ok {
			return fmt.Errorf("relation kind is not declared by patch schema")
		}
		if relation.From.Kind != declaration.FromKind || relation.To.Kind != declaration.ToKind {
			return fmt.Errorf("relation endpoint kinds do not match patch schema")
		}
	case OperationUpdate:
		declaration, ok := schema.entityDeclaration(operation.update.target.Kind)
		if !ok {
			return fmt.Errorf("update target kind is not declared by patch schema")
		}
		for _, change := range operation.update.fields {
			field, ok := findFieldDeclaration(declaration.Fields, change.Name)
			if !ok {
				return fmt.Errorf("update field %q is not declared by patch schema", change.Name)
			}
			if change.Before.present && change.Before.value.Kind() != field.Kind {
				return fmt.Errorf("update field %q before-image kind does not match patch schema", change.Name)
			}
			if change.After.Kind() != field.Kind {
				return fmt.Errorf("update field %q after-image kind does not match patch schema", change.Name)
			}
		}
	case OperationDelete:
		declaration, ok := schema.entityDeclaration(operation.delete.entity.ref.Kind)
		if !ok {
			return fmt.Errorf("delete entity kind is not declared by patch schema")
		}
		if err := validateEntityFields(operation.delete.entity, declaration); err != nil {
			return fmt.Errorf("delete entity is incompatible with patch schema: %w", err)
		}
	case OperationUnrelate:
		relation := operation.unrelate.relation
		declaration, ok := schema.relationDeclaration(relation.Kind)
		if !ok {
			return fmt.Errorf("unrelate relation kind is not declared by patch schema")
		}
		if relation.From.Kind != declaration.FromKind || relation.To.Kind != declaration.ToKind {
			return fmt.Errorf("unrelate relation endpoint kinds do not match patch schema")
		}
	default:
		return fmt.Errorf("unknown operation kind %d", operation.kind)
	}
	return nil
}

func compareOperations(a, b Operation) int {
	if a.kind != b.kind {
		if a.kind < b.kind {
			return -1
		}
		return 1
	}
	left, leftErr := encodeOperationPayloadBytes(a)
	right, rightErr := encodeOperationPayloadBytes(b)
	if leftErr != nil || rightErr != nil {
		panic("semantic: validated operation is not canonically encodable")
	}
	return bytes.Compare(left, right)
}

func validateEntityRef(ref EntityRef) error {
	if !validSemanticName(string(ref.Kind)) {
		return fmt.Errorf("entity kind is empty or invalid UTF-8")
	}
	if _, err := decodeDigest(string(ref.ID)); err != nil {
		return fmt.Errorf("entity ID: %w", err)
	}
	return nil
}
