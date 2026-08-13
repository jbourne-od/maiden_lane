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
)

// OperationInvariantCode is the closed patch-legality vocabulary ratified for
// the walking skeleton.
type OperationInvariantCode string

const (
	OperationEntityIdentityCollision OperationInvariantCode = "OP_ENTITY_IDENTITY_COLLISION"
	OperationUpdateTargetNotFound    OperationInvariantCode = "OP_UPDATE_TARGET_NOT_FOUND"
	OperationBeforeImageMismatch     OperationInvariantCode = "OP_BEFORE_IMAGE_MISMATCH"
	OperationRelationAlreadyPresent  OperationInvariantCode = "OP_RELATION_ALREADY_PRESENT"
	OperationRelationEndpointMissing OperationInvariantCode = "OP_RELATION_ENDPOINT_MISSING"
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

// Operation is the closed tagged union of Insert, Relate, and Update.
type Operation struct {
	kind   OperationKind
	insert *Insert
	relate *Relate
	update *Update
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

// Patch is an immutable, content-addressed atomic structural proposal.
type Patch struct {
	operations []Operation
	canonical  []byte
	digest     PatchDigest
}

// NewPatch validates and defensively copies the supported operation subset.
func NewPatch(operations []Operation) (Patch, error) {
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
	}
	sort.Slice(normalized, func(i, j int) bool {
		return compareOperations(normalized[i], normalized[j]) < 0
	})
	canonical, err := encodePatch(normalized)
	if err != nil {
		return Patch{}, fmt.Errorf("canonicalize patch: %w", err)
	}
	return Patch{
		operations: normalized,
		canonical:  canonical,
		digest:     PatchDigest(canonicalDigest(canonical)),
	}, nil
}

// Operations returns defensive copies in canonical patch order.
func (p Patch) Operations() []Operation { return cloneOperations(p.operations) }

// CanonicalBytes returns a copy of the complete v1 patch bytes.
func (p Patch) CanonicalBytes() []byte { return bytes.Clone(p.canonical) }

// Digest returns the content identity of the complete atomic patch.
func (p Patch) Digest() PatchDigest { return p.digest }

// ApplyPatch validates operations against an isolated candidate and returns it
// only after every operation and final graph reconstruction succeeds.
func ApplyPatch(predecessor State, patch Patch) (State, *OperationFailure) {
	entities := predecessor.Entities()
	relations := predecessor.Relations()

	for _, operation := range patch.operations {
		switch operation.kind {
		case OperationInsert:
			entity := operation.insert.entity
			if findEntityIndex(entities, entity.ref) >= 0 {
				return predecessor, operationFailure(OperationEntityIdentityCollision)
			}
			entities = append(entities, cloneEntity(entity))
		case OperationRelate:
			relation := operation.relate.relation
			if slices.Contains(relations, relation) {
				return predecessor, operationFailure(OperationRelationAlreadyPresent)
			}
			if findEntityIndex(entities, relation.From) < 0 || findEntityIndex(entities, relation.To) < 0 {
				return predecessor, operationFailure(OperationRelationEndpointMissing)
			}
			relations = append(relations, relation)
		case OperationUpdate:
			index := findEntityIndex(entities, operation.update.target)
			if index < 0 {
				return predecessor, operationFailure(OperationUpdateTargetNotFound)
			}
			fields := cloneFields(entities[index].fields)
			for _, change := range operation.update.fields {
				actual, present := fields[change.Name]
				if present != change.Before.present || (present && !actual.Equal(change.Before.value)) {
					return predecessor, operationFailure(OperationBeforeImageMismatch)
				}
				fields[change.Name] = change.After
			}
			entities[index] = Entity{ref: entities[index].ref, fields: fields}
		}
	}

	candidate, err := NewState(predecessor.Schema(), predecessor.InputLineageID(), entities, relations)
	if err != nil {
		panic(fmt.Sprintf("semantic: validated patch produced invalid state: %v", err))
	}
	return candidate, nil
}

// UndoPatch verifies the accepted after-image and applies the inverse in
// reverse canonical operation order. A mismatch returns the supplied current
// state, never a partially undone candidate.
func UndoPatch(current State, patch Patch) (State, *OperationFailure) {
	entities := current.Entities()
	relations := current.Relations()

	for operationIndex := len(patch.operations) - 1; operationIndex >= 0; operationIndex-- {
		operation := patch.operations[operationIndex]
		switch operation.kind {
		case OperationUpdate:
			index := findEntityIndex(entities, operation.update.target)
			if index < 0 {
				return current, operationFailure(OperationUpdateTargetNotFound)
			}
			fields := cloneFields(entities[index].fields)
			for _, change := range operation.update.fields {
				actual, present := fields[change.Name]
				if !present || !actual.Equal(change.After) {
					return current, operationFailure(OperationBeforeImageMismatch)
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
				return current, operationFailure(OperationBeforeImageMismatch)
			}
			relations = slices.Delete(relations, index, index+1)
		case OperationInsert:
			inserted := operation.insert.entity
			index := findEntityIndex(entities, inserted.ref)
			if index < 0 || !entitiesEqual(entities[index], inserted) {
				return current, operationFailure(OperationBeforeImageMismatch)
			}
			for _, relation := range relations {
				if relation.From == inserted.ref || relation.To == inserted.ref {
					return current, operationFailure(OperationBeforeImageMismatch)
				}
			}
			entities = slices.Delete(entities, index, index+1)
		}
	}

	predecessor, err := NewState(current.Schema(), current.InputLineageID(), entities, relations)
	if err != nil {
		panic(fmt.Sprintf("semantic: validated inverse produced invalid state: %v", err))
	}
	return predecessor, nil
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
	}
	return result
}

func validateOperation(operation Operation) error {
	switch operation.kind {
	case OperationInsert:
		if operation.insert == nil || operation.relate != nil || operation.update != nil {
			return fmt.Errorf("insert operation has invalid union payload")
		}
		entity := operation.insert.entity
		if _, err := NewEntity(entity.ref, entity.fields); err != nil {
			return fmt.Errorf("insert entity: %w", err)
		}
	case OperationRelate:
		if operation.insert != nil || operation.relate == nil || operation.update != nil {
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
		if operation.insert != nil || operation.relate != nil || operation.update == nil {
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
