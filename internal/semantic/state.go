package semantic

import (
	"bytes"
	"fmt"
	"slices"
	"sort"
)

// FieldDeclaration fixes a field's scalar type independently of whether the
// field must be present when an entity first enters a state.
type FieldDeclaration struct {
	Name                   FieldName
	Kind                   ValueKind
	RequiredAtConstruction bool
}

// EntityDeclaration fixes the closed fields admitted for one entity kind.
// NewSchema clones and normalizes Fields; callers may freely reuse this input.
type EntityDeclaration struct {
	Kind   EntityKind
	Fields []FieldDeclaration
}

// RelationDeclaration fixes a relation kind and its directed endpoint kinds.
type RelationDeclaration struct {
	Kind     RelationKind
	FromKind EntityKind
	ToKind   EntityKind
}

// SchemaDeclaration is a normalized, immutable schema source declaration.
type SchemaDeclaration struct {
	entities  []EntityDeclaration
	relations []RelationDeclaration
}

// EntityDeclarations returns a deep copy in canonical order.
func (d SchemaDeclaration) EntityDeclarations() []EntityDeclaration {
	return cloneEntityDeclarations(d.entities)
}

// RelationDeclarations returns a copy in canonical order.
func (d SchemaDeclaration) RelationDeclarations() []RelationDeclaration {
	return slices.Clone(d.relations)
}

// Schema is an immutable validated schema and its canonical identity.
type Schema struct {
	declaration SchemaDeclaration
	canonical   []byte
	digest      SchemaDigest
}

// NewSchema validates, clones, and canonically sorts a closed graph schema.
func NewSchema(entities []EntityDeclaration, relations []RelationDeclaration) (Schema, error) {
	normalizedEntities := cloneEntityDeclarations(entities)
	sort.Slice(normalizedEntities, func(i, j int) bool {
		return normalizedEntities[i].Kind < normalizedEntities[j].Kind
	})

	for i := range normalizedEntities {
		declaration := &normalizedEntities[i]
		if !validSemanticName(string(declaration.Kind)) {
			return Schema{}, fmt.Errorf("entity kind is empty or invalid UTF-8")
		}
		if i > 0 && normalizedEntities[i-1].Kind == declaration.Kind {
			return Schema{}, fmt.Errorf("duplicate entity kind %q", declaration.Kind)
		}
		sort.Slice(declaration.Fields, func(i, j int) bool {
			return declaration.Fields[i].Name < declaration.Fields[j].Name
		})
		for fieldIndex, field := range declaration.Fields {
			if !validSemanticName(string(field.Name)) {
				return Schema{}, fmt.Errorf("field name is empty or invalid UTF-8")
			}
			if !validValueKind(field.Kind) {
				return Schema{}, fmt.Errorf("field %q has unknown value kind %d", field.Name, field.Kind)
			}
			if fieldIndex > 0 && declaration.Fields[fieldIndex-1].Name == field.Name {
				return Schema{}, fmt.Errorf("duplicate field %q on entity kind %q", field.Name, declaration.Kind)
			}
		}
	}

	normalizedRelations := slices.Clone(relations)
	sort.Slice(normalizedRelations, func(i, j int) bool {
		a, b := normalizedRelations[i], normalizedRelations[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.FromKind != b.FromKind {
			return a.FromKind < b.FromKind
		}
		return a.ToKind < b.ToKind
	})
	for i, relation := range normalizedRelations {
		if !validSemanticName(string(relation.Kind)) ||
			!validSemanticName(string(relation.FromKind)) ||
			!validSemanticName(string(relation.ToKind)) {
			return Schema{}, fmt.Errorf("relation declaration contains an empty or invalid UTF-8 name")
		}
		if i > 0 && normalizedRelations[i-1].Kind == relation.Kind {
			return Schema{}, fmt.Errorf("duplicate relation kind %q", relation.Kind)
		}
		if !containsEntityKind(normalizedEntities, relation.FromKind) {
			return Schema{}, fmt.Errorf("relation %q has undeclared from kind %q", relation.Kind, relation.FromKind)
		}
		if !containsEntityKind(normalizedEntities, relation.ToKind) {
			return Schema{}, fmt.Errorf("relation %q has undeclared to kind %q", relation.Kind, relation.ToKind)
		}
	}

	declaration := SchemaDeclaration{entities: normalizedEntities, relations: normalizedRelations}
	canonical, err := encodeSchema(declaration)
	if err != nil {
		return Schema{}, fmt.Errorf("canonicalize schema: %w", err)
	}
	return Schema{
		declaration: declaration,
		canonical:   canonical,
		digest:      SchemaDigest(canonicalDigest(canonical)),
	}, nil
}

// Declaration returns an immutable normalized declaration value.
func (s Schema) Declaration() SchemaDeclaration {
	return SchemaDeclaration{
		entities:  cloneEntityDeclarations(s.declaration.entities),
		relations: slices.Clone(s.declaration.relations),
	}
}

// CanonicalBytes returns a copy of the v1 schema bytes.
func (s Schema) CanonicalBytes() []byte {
	return bytes.Clone(s.canonical)
}

// Digest returns the content identity of the canonical schema.
func (s Schema) Digest() SchemaDigest {
	return s.digest
}

// EntityRef identifies an entity kind and stable source or synthetic identity.
type EntityRef struct {
	Kind EntityKind
	ID   EntityID
}

// Entity is an immutable typed field map.
type Entity struct {
	ref    EntityRef
	fields map[FieldName]Value
}

// NewEntity validates and copies an entity reference and field map. Schema
// membership and field types are validated when the entity enters a State.
func NewEntity(ref EntityRef, fields map[FieldName]Value) (Entity, error) {
	if !validSemanticName(string(ref.Kind)) {
		return Entity{}, fmt.Errorf("entity kind is empty or invalid UTF-8")
	}
	if _, err := decodeDigest(string(ref.ID)); err != nil {
		return Entity{}, fmt.Errorf("entity ID: %w", err)
	}
	cloned := make(map[FieldName]Value, len(fields))
	for _, name := range sortedFieldNames(fields) {
		value := fields[name]
		if !validSemanticName(string(name)) {
			return Entity{}, fmt.Errorf("field name is empty or invalid UTF-8")
		}
		if !value.Valid() {
			return Entity{}, fmt.Errorf("field %q has invalid value", name)
		}
		cloned[name] = value
	}
	return Entity{ref: ref, fields: cloned}, nil
}

// Ref returns the entity's stable typed reference.
func (e Entity) Ref() EntityRef {
	return e.ref
}

// Field returns a typed value and a presence bit. Absence is not a scalar.
func (e Entity) Field(name FieldName) (Value, bool) {
	value, ok := e.fields[name]
	return value, ok
}

// Fields returns a copy of all present fields.
func (e Entity) Fields() map[FieldName]Value {
	return cloneFields(e.fields)
}

// Relation is an explicit, directed typed graph edge.
type Relation struct {
	Kind RelationKind
	From EntityRef
	To   EntityRef
}

// State is an immutable typed entity graph within one input lineage.
type State struct {
	schema    Schema
	lineage   InputLineageID
	entities  []Entity
	relations []Relation
	canonical []byte
	digest    StateDigest
}

// NewState validates representation and declared types only. Transformation
// predicates and consumer completeness belong to later boundaries.
func NewState(schema Schema, lineage InputLineageID, entities []Entity, relations []Relation) (State, error) {
	if len(schema.canonical) == 0 {
		return State{}, fmt.Errorf("schema is not initialized")
	}
	if _, err := decodeDigest(string(lineage)); err != nil {
		return State{}, fmt.Errorf("input lineage ID: %w", err)
	}

	clonedEntities := cloneEntities(entities)
	sort.Slice(clonedEntities, func(i, j int) bool {
		return compareEntityRefs(clonedEntities[i].ref, clonedEntities[j].ref) < 0
	})
	for i, entity := range clonedEntities {
		if i > 0 && entity.ref == clonedEntities[i-1].ref {
			return State{}, fmt.Errorf("duplicate entity reference")
		}
		declaration, ok := schema.entityDeclaration(entity.ref.Kind)
		if !ok {
			return State{}, fmt.Errorf("entity kind %q is not declared", entity.ref.Kind)
		}
		if _, err := decodeDigest(string(entity.ref.ID)); err != nil {
			return State{}, fmt.Errorf("entity ID: %w", err)
		}
		if err := validateEntityFields(entity, declaration); err != nil {
			return State{}, err
		}
	}

	clonedRelations := slices.Clone(relations)
	sort.Slice(clonedRelations, func(i, j int) bool {
		return compareRelations(clonedRelations[i], clonedRelations[j]) < 0
	})
	for i, relation := range clonedRelations {
		if !validSemanticName(string(relation.Kind)) {
			return State{}, fmt.Errorf("relation kind is empty or invalid UTF-8")
		}
		if i > 0 && relation == clonedRelations[i-1] {
			return State{}, fmt.Errorf("duplicate relation")
		}
		declaration, ok := schema.relationDeclaration(relation.Kind)
		if !ok {
			return State{}, fmt.Errorf("relation kind %q is not declared", relation.Kind)
		}
		if relation.From.Kind != declaration.FromKind || relation.To.Kind != declaration.ToKind {
			return State{}, fmt.Errorf("relation %q endpoint kinds do not match declaration", relation.Kind)
		}
		if !containsEntityRef(clonedEntities, relation.From) || !containsEntityRef(clonedEntities, relation.To) {
			return State{}, fmt.Errorf("relation %q endpoint is missing", relation.Kind)
		}
	}

	state := State{
		schema:    schema,
		lineage:   lineage,
		entities:  clonedEntities,
		relations: clonedRelations,
	}
	canonical, err := encodeState(state)
	if err != nil {
		return State{}, fmt.Errorf("canonicalize state: %w", err)
	}
	state.canonical = canonical
	state.digest = StateDigest(canonicalDigest(canonical))
	return state, nil
}

// Schema returns the immutable schema value associated with the state.
func (s State) Schema() Schema {
	return s.schema
}

// InputLineageID returns the lineage that namespaces source identities.
func (s State) InputLineageID() InputLineageID {
	return s.lineage
}

// Entity returns a defensive copy of the requested entity.
func (s State) Entity(ref EntityRef) (Entity, bool) {
	for _, entity := range s.entities {
		if entity.ref == ref {
			return cloneEntity(entity), true
		}
	}
	return Entity{}, false
}

// Entities returns defensive copies in canonical order.
func (s State) Entities() []Entity {
	return cloneEntities(s.entities)
}

// Relations returns a copy in canonical order.
func (s State) Relations() []Relation {
	return slices.Clone(s.relations)
}

// CanonicalBytes returns a copy of the v1 state bytes.
func (s State) CanonicalBytes() []byte {
	return bytes.Clone(s.canonical)
}

// Digest returns the content identity of the canonical state.
func (s State) Digest() StateDigest {
	return s.digest
}

func (s Schema) entityDeclaration(kind EntityKind) (EntityDeclaration, bool) {
	for _, declaration := range s.declaration.entities {
		if declaration.Kind == kind {
			return declaration, true
		}
	}
	return EntityDeclaration{}, false
}

func (s Schema) relationDeclaration(kind RelationKind) (RelationDeclaration, bool) {
	for _, declaration := range s.declaration.relations {
		if declaration.Kind == kind {
			return declaration, true
		}
	}
	return RelationDeclaration{}, false
}

func validateEntityFields(entity Entity, declaration EntityDeclaration) error {
	for _, name := range sortedFieldNames(entity.fields) {
		value := entity.fields[name]
		field, ok := findFieldDeclaration(declaration.Fields, name)
		if !ok {
			return fmt.Errorf("field %q is not declared for entity kind %q", name, entity.ref.Kind)
		}
		if value.Kind() != field.Kind {
			return fmt.Errorf("field %q has kind %d, want %d", name, value.Kind(), field.Kind)
		}
	}
	for _, field := range declaration.Fields {
		if field.RequiredAtConstruction {
			if _, ok := entity.fields[field.Name]; !ok {
				return fmt.Errorf("required field %q is absent", field.Name)
			}
		}
	}
	return nil
}

func findFieldDeclaration(fields []FieldDeclaration, name FieldName) (FieldDeclaration, bool) {
	index, ok := slices.BinarySearchFunc(fields, name, func(field FieldDeclaration, target FieldName) int {
		return compare(string(field.Name), string(target))
	})
	if !ok {
		return FieldDeclaration{}, false
	}
	return fields[index], true
}

func containsEntityKind(declarations []EntityDeclaration, kind EntityKind) bool {
	_, ok := slices.BinarySearchFunc(declarations, kind, func(declaration EntityDeclaration, target EntityKind) int {
		return compare(string(declaration.Kind), string(target))
	})
	return ok
}

func containsEntityRef(entities []Entity, ref EntityRef) bool {
	_, ok := slices.BinarySearchFunc(entities, ref, func(entity Entity, target EntityRef) int {
		return compareEntityRefs(entity.ref, target)
	})
	return ok
}

func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func cloneEntityDeclarations(input []EntityDeclaration) []EntityDeclaration {
	result := make([]EntityDeclaration, len(input))
	for i, declaration := range input {
		result[i] = EntityDeclaration{
			Kind:   declaration.Kind,
			Fields: slices.Clone(declaration.Fields),
		}
	}
	return result
}

func cloneFields(input map[FieldName]Value) map[FieldName]Value {
	result := make(map[FieldName]Value, len(input))
	for name, value := range input {
		result[name] = value
	}
	return result
}

func cloneEntity(entity Entity) Entity {
	return Entity{ref: entity.ref, fields: cloneFields(entity.fields)}
}

func cloneEntities(input []Entity) []Entity {
	result := make([]Entity, len(input))
	for i, entity := range input {
		result[i] = cloneEntity(entity)
	}
	return result
}
