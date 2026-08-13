package semantic

import (
	"bytes"
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

// Production break caught: retaining caller maps or exposing state entity
// slices would allow an already-created state to change beneath its identity.
func TestStateDefensivelyCopiesConstructorInputsAndGetterResults(t *testing.T) {
	fields := map[FieldName]Value{"assignment_key": mustString(t, "X")}
	driver := mustEntity(t, "driver", testDriverAID, fields)
	entities := []Entity{driver}
	state, err := NewState(fixtureSchemaForStateTests(t), testLineageID, entities, nil)
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	before := state.CanonicalBytes()

	fields["assignment_key"] = mustString(t, "mutated")
	entities[0] = Entity{}
	got := state.Entities()
	got[0] = Entity{}
	returned, ok := state.Entity(driver.Ref())
	if !ok {
		t.Fatal("driver missing")
	}
	returnedFields := returned.Fields()
	returnedFields["assignment_key"] = mustString(t, "getter-mutated")

	if !bytes.Equal(before, state.CanonicalBytes()) {
		t.Fatal("state mutated through caller-owned input or getter result")
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
