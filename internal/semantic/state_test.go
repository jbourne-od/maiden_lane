package semantic

import (
	"bytes"
	"strings"
	"testing"
)

const (
	testLineageID InputLineageID = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	testDriverAID EntityID       = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	testDriverBID EntityID       = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	testTeamID    EntityID       = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
)

func fixtureSchemaForStateTests(t *testing.T) Schema {
	t.Helper()
	schema, err := NewSchema(
		[]EntityDeclaration{
			{
				Kind: "driver",
				Fields: []FieldDeclaration{
					{Name: "assignment_key", Kind: ValueString, RequiredAtConstruction: false},
					{Name: "hos_anchor", Kind: ValueAtom, RequiredAtConstruction: false},
					{Name: "hos_elapsed_hours", Kind: ValueInt64, RequiredAtConstruction: false},
					{Name: "hos_driving_hours", Kind: ValueInt64, RequiredAtConstruction: false},
				},
			},
			{
				Kind: "team",
				Fields: []FieldDeclaration{
					{Name: "assignment_key", Kind: ValueString, RequiredAtConstruction: true},
					{Name: "aggregation_anchor", Kind: ValueAtom, RequiredAtConstruction: false},
					{Name: "elapsed_duration_hours", Kind: ValueInt64, RequiredAtConstruction: false},
					{Name: "driving_duration_hours", Kind: ValueInt64, RequiredAtConstruction: false},
				},
			},
		},
		[]RelationDeclaration{{Kind: "member", FromKind: "team", ToKind: "driver"}},
	)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	return schema
}

func mustString(t *testing.T, value string) Value {
	t.Helper()
	result, err := NewStringValue(value)
	if err != nil {
		t.Fatalf("NewStringValue(%q): %v", value, err)
	}
	return result
}

func mustAtom(t *testing.T, value string) Value {
	t.Helper()
	result, err := NewAtomValue(value)
	if err != nil {
		t.Fatalf("NewAtomValue(%q): %v", value, err)
	}
	return result
}

func mustEntity(t *testing.T, kind EntityKind, id EntityID, fields map[FieldName]Value) Entity {
	t.Helper()
	entity, err := NewEntity(EntityRef{Kind: kind, ID: id}, fields)
	if err != nil {
		t.Fatalf("NewEntity: %v", err)
	}
	return entity
}

// Production break caught: requiring optional driver observations at the
// representation boundary would make the lawful pre-T2 state unrepresentable.
func TestNewStateAllowsMissingOptionalDriverHOS(t *testing.T) {
	schema := fixtureSchemaForStateTests(t)
	driver := mustEntity(t, "driver", testDriverAID, map[FieldName]Value{
		"assignment_key": mustString(t, "assignment-X"),
	})
	state, err := NewState(schema, testLineageID, []Entity{driver}, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	if _, ok := state.Entity(driver.Ref()); !ok {
		t.Fatal("driver missing")
	}
}

// Production break caught: skipping schema type checks would admit a string
// into the integer HOS field and poison later typed reductions.
func TestNewStateRejectsWrongTypedField(t *testing.T) {
	_, err := NewState(fixtureSchemaForStateTests(t), testLineageID, []Entity{
		mustEntity(t, "driver", testDriverAID, map[FieldName]Value{
			"hos_elapsed_hours": mustString(t, "ten"),
		}),
	}, nil)
	if err == nil {
		t.Fatal("NewState accepted wrong field type")
	}
}

// Production break caught: iterating invalid entity fields as a Go map would
// make the first reported field depend on randomized map iteration order.
func TestNewEntityReportsInvalidFieldsInCanonicalOrder(t *testing.T) {
	fields := map[FieldName]Value{
		"z_invalid": {},
		"a_invalid": {},
	}
	for range 100 {
		_, err := NewEntity(EntityRef{Kind: "driver", ID: testDriverAID}, fields)
		if err == nil {
			t.Fatal("NewEntity accepted invalid fields")
		}
		if !strings.Contains(err.Error(), `field "a_invalid"`) {
			t.Fatalf("NewEntity error = %q; want canonical first field a_invalid", err)
		}
	}
}

// Production break caught: iterating out-of-schema entity fields as a Go map
// would make identical invalid states return different first errors.
func TestNewStateReportsInvalidFieldsInCanonicalOrder(t *testing.T) {
	driver := mustEntity(t, "driver", testDriverAID, map[FieldName]Value{
		"z_unknown": mustString(t, "Z"),
		"a_unknown": mustString(t, "A"),
	})
	for range 100 {
		_, err := NewState(fixtureSchemaForStateTests(t), testLineageID, []Entity{driver}, nil)
		if err == nil {
			t.Fatal("NewState accepted unknown fields")
		}
		if !strings.Contains(err.Error(), `field "a_unknown"`) {
			t.Fatalf("NewState error = %q; want canonical first field a_unknown", err)
		}
	}
}

// Production break caught: accepting undeclared fields would create hidden
// semantic inputs outside the closed schema.
func TestNewStateRejectsUnknownField(t *testing.T) {
	_, err := NewState(fixtureSchemaForStateTests(t), testLineageID, []Entity{
		mustEntity(t, "driver", testDriverAID, map[FieldName]Value{
			"secret_field": mustString(t, "hidden"),
		}),
	}, nil)
	if err == nil {
		t.Fatal("NewState accepted undeclared field")
	}
}

// Production break caught: ignoring RequiredAtConstruction would admit a team
// without the one representation-level required field declared by its schema.
func TestNewStateRejectsMissingRequiredField(t *testing.T) {
	team := mustEntity(t, "team", testTeamID, nil)
	_, err := NewState(fixtureSchemaForStateTests(t), testLineageID, []Entity{team}, nil)
	if err == nil {
		t.Fatal("NewState accepted entity missing required field")
	}
}

// Production break caught: treating presence as truthiness would make a
// present empty string indistinguishable from an absent optional field.
func TestEntityDistinguishesAbsentFromPresentEmptyField(t *testing.T) {
	driver := mustEntity(t, "driver", testDriverAID, map[FieldName]Value{
		"assignment_key": mustString(t, ""),
	})
	state, err := NewState(fixtureSchemaForStateTests(t), testLineageID, []Entity{driver}, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	got, ok := state.Entity(driver.Ref())
	if !ok {
		t.Fatal("driver missing")
	}
	present, ok := got.Field("assignment_key")
	if !ok {
		t.Fatal("present empty assignment_key reported absent")
	}
	text, ok := present.String()
	if !ok || text != "" {
		t.Fatalf("present empty field = %q, %v; want empty, true", text, ok)
	}
	if _, ok := got.Field("hos_anchor"); ok {
		t.Fatal("absent hos_anchor reported present")
	}
}

// Production break caught: embedding T2's HOS predicates in state construction
// would reject a representable observation before its use-specific boundary.
func TestNewStateDoesNotEnforceTeamHOSRuleSemantics(t *testing.T) {
	driver := mustEntity(t, "driver", testDriverAID, map[FieldName]Value{
		"hos_elapsed_hours": NewInt64Value(-1),
		"hos_driving_hours": NewInt64Value(2),
	})
	if _, err := NewState(fixtureSchemaForStateTests(t), testLineageID, []Entity{driver}, nil); err != nil {
		t.Fatalf("NewState enforced suffix HOS semantics: %v", err)
	}
}

// Production break caught: allowing duplicate entity kinds makes field lookup
// and canonical schema meaning depend on declaration order.
func TestNewSchemaRejectsDuplicateEntityDeclaration(t *testing.T) {
	_, err := NewSchema(
		[]EntityDeclaration{{Kind: "driver"}, {Kind: "driver"}},
		nil,
	)
	if err == nil {
		t.Fatal("NewSchema accepted duplicate entity kind")
	}
}

// Production break caught: allowing duplicate fields makes a field's type and
// required marker depend on declaration order.
func TestNewSchemaRejectsDuplicateFieldDeclaration(t *testing.T) {
	_, err := NewSchema([]EntityDeclaration{{
		Kind: "driver",
		Fields: []FieldDeclaration{
			{Name: "assignment_key", Kind: ValueString},
			{Name: "assignment_key", Kind: ValueAtom},
		},
	}}, nil)
	if err == nil {
		t.Fatal("NewSchema accepted duplicate field")
	}
}

// Production break caught: allowing duplicate relation kinds makes endpoint
// typing ambiguous and declaration-order dependent.
func TestNewSchemaRejectsDuplicateRelationDeclaration(t *testing.T) {
	_, err := NewSchema(
		[]EntityDeclaration{{Kind: "driver"}, {Kind: "team"}},
		[]RelationDeclaration{
			{Kind: "member", FromKind: "team", ToKind: "driver"},
			{Kind: "member", FromKind: "driver", ToKind: "team"},
		},
	)
	if err == nil {
		t.Fatal("NewSchema accepted duplicate relation kind")
	}
}

// Production break caught: a relation declaration referencing an undeclared
// endpoint kind would make schema validation incomplete.
func TestNewSchemaRejectsUndeclaredRelationEndpointKind(t *testing.T) {
	_, err := NewSchema(
		[]EntityDeclaration{{Kind: "driver"}},
		[]RelationDeclaration{{Kind: "member", FromKind: "team", ToKind: "driver"}},
	)
	if err == nil {
		t.Fatal("NewSchema accepted undeclared relation endpoint kind")
	}
}

// Production break caught: accepting an unknown relation kind would bypass
// the schema's closed graph vocabulary.
func TestNewStateRejectsUnknownRelationKind(t *testing.T) {
	team, driver := relatedFixtureEntities(t)
	_, err := NewState(fixtureSchemaForStateTests(t), testLineageID, []Entity{team, driver}, []Relation{{
		Kind: "unknown",
		From: team.Ref(),
		To:   driver.Ref(),
	}})
	if err == nil {
		t.Fatal("NewState accepted unknown relation kind")
	}
}

// Production break caught: ignoring declared endpoint direction would accept
// driver --member--> team despite the schema fixing team -> driver.
func TestNewStateRejectsReversedRelationEndpointKinds(t *testing.T) {
	team, driver := relatedFixtureEntities(t)
	_, err := NewState(fixtureSchemaForStateTests(t), testLineageID, []Entity{team, driver}, []Relation{{
		Kind: "member",
		From: driver.Ref(),
		To:   team.Ref(),
	}})
	if err == nil {
		t.Fatal("NewState accepted reversed member relation")
	}
}

// Production break caught: accepting duplicate relations would turn a set into
// an insertion-order-sensitive multiset.
func TestNewStateRejectsDuplicateRelation(t *testing.T) {
	team, driver := relatedFixtureEntities(t)
	relation := Relation{Kind: "member", From: team.Ref(), To: driver.Ref()}
	_, err := NewState(
		fixtureSchemaForStateTests(t),
		testLineageID,
		[]Entity{team, driver},
		[]Relation{relation, relation},
	)
	if err == nil {
		t.Fatal("NewState accepted duplicate relation")
	}
}

// Production break caught: accepting duplicate entity references would make
// state lookup depend on caller insertion order.
func TestNewStateRejectsDuplicateEntityReference(t *testing.T) {
	driver := mustEntity(t, "driver", testDriverAID, nil)
	_, err := NewState(
		fixtureSchemaForStateTests(t),
		testLineageID,
		[]Entity{driver, driver},
		nil,
	)
	if err == nil {
		t.Fatal("NewState accepted duplicate entity reference")
	}
}

// Production break caught: accepting a relation whose endpoint is absent from
// state would violate typed graph referential integrity at construction.
func TestNewStateRejectsMissingRelationEndpoint(t *testing.T) {
	team, driver := relatedFixtureEntities(t)
	_, err := NewState(fixtureSchemaForStateTests(t), testLineageID, []Entity{team}, []Relation{{
		Kind: "member",
		From: team.Ref(),
		To:   driver.Ref(),
	}})
	if err == nil {
		t.Fatal("NewState accepted relation with missing endpoint")
	}
}

// Production break caught: shallow-copying an Entity's private field map,
// exposing it through an entity getter, or returning live canonical bytes
// would let a state's semantic values diverge from its cached identity.
func TestStateDefensivelyCopiesConstructorInputsAndGetterResults(t *testing.T) {
	fields := map[FieldName]Value{"assignment_key": mustString(t, "X")}
	driver := mustEntity(t, "driver", testDriverAID, fields)
	entities := []Entity{driver}
	state, err := NewState(fixtureSchemaForStateTests(t), testLineageID, entities, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	wantCanonical := state.CanonicalBytes()
	wantDigest := state.Digest()

	fields["assignment_key"] = mustString(t, "mutated")
	driver.fields["assignment_key"] = mustString(t, "driver-mutated")
	entities[0] = Entity{}
	got := state.Entities()
	got[0].fields["assignment_key"] = mustString(t, "entities-getter-mutated")
	returned, ok := state.Entity(driver.Ref())
	if !ok {
		t.Fatal("driver missing")
	}
	returned.fields["assignment_key"] = mustString(t, "entity-getter-mutated")
	returnedFields := returned.Fields()
	returnedFields["assignment_key"] = mustString(t, "getter-mutated")
	canonical := state.CanonicalBytes()
	canonical[0] ^= 0xff

	stored, ok := state.Entity(driver.Ref())
	if !ok {
		t.Fatal("driver missing after mutation attempts")
	}
	assignment, ok := stored.Field("assignment_key")
	if !ok {
		t.Fatal("assignment_key missing after mutation attempts")
	}
	text, ok := assignment.String()
	if !ok || text != "X" {
		t.Fatalf("stored assignment_key = %q, %v; want X, true", text, ok)
	}
	if !bytes.Equal(wantCanonical, state.CanonicalBytes()) || state.Digest() != wantDigest {
		t.Fatal("state identity changed through caller-owned input or getter result")
	}
}

// Production break caught: retaining schema declaration input or returning
// shallow declaration/canonical getters would mutate an accepted schema.
func TestSchemaDefensivelyCopiesConstructorInputsAndGetterResults(t *testing.T) {
	fields := []FieldDeclaration{{Name: "assignment_key", Kind: ValueString}}
	entities := []EntityDeclaration{{Kind: "driver", Fields: fields}}
	relations := []RelationDeclaration{{Kind: "member", FromKind: "driver", ToKind: "driver"}}
	schema, err := NewSchema(entities, relations)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	wantCanonical := schema.CanonicalBytes()
	wantDigest := schema.Digest()

	fields[0].Name = "input-fields-mutated"
	entities[0].Kind = "input-entities-mutated"
	entities[0].Fields[0].Name = "input-nested-mutated"
	relations[0].Kind = "input-relations-mutated"
	declaration := schema.Declaration()
	declaration.entities[0].Kind = "declaration-mutated"
	declaration.entities[0].Fields[0].Name = "declaration-nested-mutated"
	declaration.relations[0].Kind = "declaration-relation-mutated"
	entityDeclarations := schema.Declaration().EntityDeclarations()
	entityDeclarations[0].Fields[0].Name = "entity-getter-mutated"
	relationDeclarations := schema.Declaration().RelationDeclarations()
	relationDeclarations[0].Kind = "relation-getter-mutated"
	canonical := schema.CanonicalBytes()
	canonical[0] ^= 0xff

	stored := schema.Declaration()
	if stored.entities[0].Kind != "driver" || stored.entities[0].Fields[0].Name != "assignment_key" {
		t.Fatalf("stored entity declaration mutated: %+v", stored.entities[0])
	}
	if stored.relations[0].Kind != "member" {
		t.Fatalf("stored relation declaration mutated: %+v", stored.relations[0])
	}
	if !bytes.Equal(wantCanonical, schema.CanonicalBytes()) || schema.Digest() != wantDigest {
		t.Fatal("schema identity changed through caller-owned input or getter result")
	}
}

func relatedFixtureEntities(t *testing.T) (Entity, Entity) {
	t.Helper()
	team := mustEntity(t, "team", testTeamID, map[FieldName]Value{
		"assignment_key": mustString(t, "X"),
	})
	driver := mustEntity(t, "driver", testDriverAID, nil)
	return team, driver
}
